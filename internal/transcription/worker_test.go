package transcription

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"medconnect/internal/domain"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeNoteStore records StoreDictatedNote calls.
type fakeNoteStore struct {
	mu    sync.Mutex
	calls []stored
}

type stored struct {
	appointmentID string
	text          string
	complete      bool
	missing       []int
}

func (f *fakeNoteStore) StoreDictatedNote(_ context.Context, appointmentID, text string, complete bool, missing []int) (domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, stored{appointmentID, text, complete, missing})
	return domain.Note{ID: "note-x", Status: domain.NoteComplete}, nil
}

func (f *fakeNoteStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeNoteStore) last() stored {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// urlSource opens the stream at a fixed base URL (the test server).
type urlSource struct{ base string }

func (u urlSource) Request(ctx context.Context, _ string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, u.base, nil)
}

func sseServer(chunks []Chunk, block <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"appointmentId\":%q,\"sequence\":%d,\"text\":%q,\"isFinal\":%t}\n\n",
				c.AppointmentID, c.Sequence, c.Text, c.IsFinal)
			if fl != nil {
				fl.Flush()
			}
		}
		if block != nil {
			<-block // keep the connection open to hold the session in-flight
		}
	}))
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func newManager(base string, notes NoteStore) *Manager {
	return NewManager(Config{
		Notes:   notes,
		Source:  urlSource{base: base},
		Timeout: 2 * time.Second,
		Logger:  quietLogger(),
	})
}

func TestManager_CompleteStreamStoresCompleteNote(t *testing.T) {
	srv := sseServer([]Chunk{
		{AppointmentID: "appt-1", Sequence: 0, Text: "Patient does "},
		{AppointmentID: "appt-1", Sequence: 1, Text: "not "},
		{AppointmentID: "appt-1", Sequence: 2, Text: "report pain.", IsFinal: true},
	}, nil)
	defer srv.Close()

	notes := &fakeNoteStore{}
	m := newManager(srv.URL, notes)
	if !m.Start("hosp-A", "appt-1", "doc-1") {
		t.Fatal("Start should return true")
	}
	waitFor(t, func() bool { return notes.count() == 1 })
	m.Stop(context.Background())

	got := notes.last()
	if !got.complete || got.text != "Patient does not report pain." {
		t.Errorf("stored = %+v, want complete full text", got)
	}
}

func TestManager_GappyStreamStoresIncompleteNote(t *testing.T) {
	// Sequence 1 is missing; final is 2.
	srv := sseServer([]Chunk{
		{AppointmentID: "appt-1", Sequence: 0, Text: "a"},
		{AppointmentID: "appt-1", Sequence: 2, Text: "c", IsFinal: true},
	}, nil)
	defer srv.Close()

	notes := &fakeNoteStore{}
	m := newManager(srv.URL, notes)
	m.Start("hosp-A", "appt-1", "doc-1")
	waitFor(t, func() bool { return notes.count() == 1 })
	m.Stop(context.Background())

	got := notes.last()
	if got.complete {
		t.Error("a gappy stream must store an incomplete note")
	}
	if len(got.missing) != 1 || got.missing[0] != 1 {
		t.Errorf("missing = %v, want [1]", got.missing)
	}
}

func TestManager_DuplicateStartRejected(t *testing.T) {
	block := make(chan struct{})
	// Streams one non-final chunk, then holds the connection open.
	srv := sseServer([]Chunk{{AppointmentID: "appt-1", Sequence: 0, Text: "a"}}, block)
	defer srv.Close()

	notes := &fakeNoteStore{}
	m := newManager(srv.URL, notes)

	if !m.Start("hosp-A", "appt-1", "doc-1") {
		t.Fatal("first Start should return true")
	}
	// While the first session is held open, a second start is rejected.
	waitFor(t, func() bool { return m.isRunning("hosp-A", "appt-1") })
	if m.Start("hosp-A", "appt-1", "doc-1") {
		t.Error("second Start for an in-flight appointment should return false")
	}
	// Release the stream and let the session finish.
	close(block)
	m.Stop(context.Background())
	if notes.count() != 1 {
		t.Errorf("stored notes = %d, want 1", notes.count())
	}
}

// isRunning exposes in-flight state for the duplicate-start test.
func (m *Manager) isRunning(tenantID, appointmentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inflight[tenantID+"/"+appointmentID]
}
