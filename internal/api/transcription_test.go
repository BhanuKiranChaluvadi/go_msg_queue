package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"medconnect/internal/appointments"
	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/transcription"
	"medconnect/internal/webhooks"
)

// stubStarter records Start calls without doing any streaming.
type stubStarter struct {
	mu    sync.Mutex
	calls int
	ret   bool
}

func (s *stubStarter) Start(_, _, _ string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.ret
}

func (s *stubStarter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestStartTranscription_AuthOwnershipAndConflict(t *testing.T) {
	srv := newTestServer()
	starter := &stubStarter{ret: true}
	srv.Transcription = starter

	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	path := "/v1/appointments/" + apptID + "/transcription"

	// unauth / patient / cross-tenant / non-owning doctor all fail before Start.
	if rec := doRequest(t, srv, http.MethodPost, path, "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth = %d, want 401", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "pat-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("patient = %d, want 403", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-B", "doc-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant = %d, want 404", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a2", nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-owning doctor = %d, want 403", rec.Code)
	}
	if starter.count() != 0 {
		t.Errorf("Start called %d times before a valid request, want 0", starter.count())
	}

	// Owning doctor starts it -> 202.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("owner = %d, want 202", rec.Code)
	}
	if starter.count() != 1 {
		t.Errorf("Start called %d times, want 1", starter.count())
	}

	// When already in progress, Start returns false -> 409.
	starter.ret = false
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a", nil); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}
}

// fixedSource returns a request to a fixed URL, ignoring the appointment id.
type fixedSource struct{ url string }

func (f fixedSource) Request(ctx context.Context, _ string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
}

func newTranscribingServer(t *testing.T, chunks []transcription.Chunk) (*Server, func()) {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	idgen := platform.NewFakeIDGen("req-")
	clock := platform.NewFakeClock(apiTestNow)
	publisher := events.NewPublisher(events.NewStore(), platform.SystemClock{}, idgen)
	appts := appointments.NewService(appointments.Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  memory.NewAppointmentStore(),
		Notes:         memory.NewNoteStore(),
		Prescriptions: memory.NewPrescriptionStore(),
		Clock:         clock,
		IDGen:         platform.NewFakeIDGen("ts-"),
		Events:        publisher,
	})

	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"appointmentId\":%q,\"sequence\":%d,\"text\":%q,\"isFinal\":%t}\n\n",
				c.AppointmentID, c.Sequence, c.Text, c.IsFinal)
			if fl != nil {
				fl.Flush()
			}
		}
	}))

	mgr := transcription.NewManager(transcription.Config{
		Notes:   appts,
		Source:  fixedSource{url: sse.URL},
		Timeout: 2 * time.Second,
		Logger:  discard,
	})

	srv := &Server{
		Logger:        discard,
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: "secret",
		Resolver:      apiTestResolver(),
		Appointments:  appts,
		Webhooks:      webhooks.NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-")),
		Transcription: mgr,
	}
	cleanup := func() {
		mgr.Stop(context.Background())
		sse.Close()
	}
	return srv, cleanup
}

func TestStartTranscription_EndToEndStoresDictatedNote(t *testing.T) {
	srv, cleanup := newTranscribingServer(t, []transcription.Chunk{
		{Sequence: 0, Text: "Patient does "},
		{Sequence: 1, Text: "not "},
		{Sequence: 2, Text: "report pain.", IsFinal: true},
	})
	defer cleanup()

	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)

	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/transcription", "hosp-A", "doc-a", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("start transcription: %d %s", rec.Code, rec.Body.String())
	}

	// The dictated note appears on the appointment overview once assembled.
	var overview appointments.Overview
	waitFor(t, func() bool {
		rec := doRequest(t, srv, http.MethodGet, "/v1/appointments/"+apptID, "hosp-A", "doc-a", nil)
		if rec.Code != http.StatusOK {
			return false
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &overview)
		return len(overview.Notes) == 1
	})

	note := overview.Notes[0]
	if note.Source != domain.NoteSourceDictation || note.Status != domain.NoteComplete {
		t.Errorf("note = %+v, want dictation/complete", note)
	}
	if note.Text != "Patient does not report pain." {
		t.Errorf("note text = %q", note.Text)
	}
}
