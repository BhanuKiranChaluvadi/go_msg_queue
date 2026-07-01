package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/protocol"
	"medconnect/internal/tenancy"
)

// errComplete stops the SSE read loop early once the transcript is fully assembled.
var errComplete = errors.New("transcription: assembly complete")

// NoteStore persists an assembled dictation note. appointments.Service satisfies
// it; depending on the interface keeps this package decoupled from that one.
type NoteStore interface {
	StoreDictatedNote(ctx context.Context, appointmentID, text string, complete bool, missing []int) (domain.Note, error)
}

// StreamSource builds the request that opens the transcription stream for an
// appointment. It abstracts the external transcription server's URL and auth so
// the real endpoint and a test server are interchangeable.
type StreamSource interface {
	Request(ctx context.Context, appointmentID string) (*http.Request, error)
}

// Config wires a Manager.
type Config struct {
	Client  *protocol.SSEClient
	Notes   NoteStore
	Source  StreamSource
	Timeout time.Duration // bounds a session so a stalled stream can never hang
	Logger  *slog.Logger
}

// Manager runs background transcription sessions: one goroutine per appointment
// consumes the SSE stream, assembles the note, and stores it.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	inflight map[string]bool
	wg       sync.WaitGroup
}

// NewManager builds a Manager with sensible defaults.
func NewManager(cfg Config) *Manager {
	if cfg.Client == nil {
		cfg.Client = &protocol.SSEClient{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Manager{cfg: cfg, inflight: make(map[string]bool)}
}

// Start launches a transcription session for an appointment. It returns false if
// a session for that appointment is already running (idempotent start).
func (m *Manager) Start(tenantID, appointmentID, doctorID string) bool {
	key := tenantID + "/" + appointmentID
	m.mu.Lock()
	if m.inflight[key] {
		m.mu.Unlock()
		return false
	}
	m.inflight[key] = true
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.inflight, key)
			m.mu.Unlock()
		}()
		m.run(tenantID, appointmentID, doctorID)
	}()
	return true
}

// Stop waits for in-flight sessions to finish, bounded by ctx.
func (m *Manager) Stop(ctx context.Context) {
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (m *Manager) run(tenantID, appointmentID, doctorID string) {
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.Timeout)
	defer cancel()

	asm := NewAssembler()

	req, err := m.cfg.Source.Request(ctx, appointmentID)
	if err != nil {
		m.cfg.Logger.Error("transcription: cannot build stream request", "appt", appointmentID, "err", err)
	} else {
		streamErr := m.cfg.Client.Stream(ctx, req, func(data []byte) error {
			var c Chunk
			if json.Unmarshal(data, &c) != nil {
				return nil // skip malformed events rather than aborting the stream
			}
			if c.AppointmentID != "" && c.AppointmentID != appointmentID {
				return nil // ignore chunks for another appointment
			}
			asm.Add(c)
			if asm.IsComplete() {
				return errComplete
			}
			return nil
		})
		if streamErr != nil && !errors.Is(streamErr, errComplete) {
			m.cfg.Logger.Warn("transcription stream ended abnormally", "appt", appointmentID, "err", streamErr)
		}
	}

	// Store whatever was assembled, even on stream error/timeout: a partial stream
	// yields an explicitly incomplete note, never a silently-complete one.
	text, complete, missing := asm.Result()
	storeCtx := tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: doctorID, TenantID: tenantID, Role: domain.RoleDoctor})
	if _, err := m.cfg.Notes.StoreDictatedNote(storeCtx, appointmentID, text, complete, missing); err != nil {
		m.cfg.Logger.Error("transcription: failed to store note", "appt", appointmentID, "err", err)
	}
}
