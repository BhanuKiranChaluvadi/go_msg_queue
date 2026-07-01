package api

import (
	"context"
	"encoding/json"
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
	"medconnect/internal/webhooks"
)

// newDeliveringServer wires the full live-updates pipeline: the same event
// publisher feeds a subscriber that enqueues onto a started dispatcher, and the
// Server's webhook registry is the very instance the subscriber queries.
func newDeliveringServer(t *testing.T) (*Server, *webhooks.Dispatcher) {
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
	registry := webhooks.NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-"))

	disp := webhooks.NewDispatcher(webhooks.Config{
		Workers: 2, QueueSize: 16, MaxAttempts: 3,
		BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Logger: discard,
	})
	disp.Start()
	publisher.Subscribe(webhooks.NewSubscriber(registry, disp, discard))

	srv := &Server{
		Logger:        discard,
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: "secret",
		Resolver:      apiTestResolver(),
		Appointments:  appts,
		Webhooks:      registry,
	}
	return srv, disp
}

func TestWebhookDelivery_EndToEnd(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
	)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	srv, disp := newDeliveringServer(t)
	defer disp.Stop(context.Background())

	// pat-a subscribes to note_added, pointing at the receiver.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/webhooks", "hosp-A", "pat-a",
		webhookReq{URL: receiver.URL, EventTypes: []domain.EventType{domain.EventNoteAdded}}); rec.Code != http.StatusCreated {
		t.Fatalf("register webhook: %d %s", rec.Code, rec.Body.String())
	}

	// Book an appointment for doc-a/pat-a, then the doctor adds a note.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/notes", "hosp-A", "doc-a",
		map[string]string{"text": "Reports headache."}); rec.Code != http.StatusCreated {
		t.Fatalf("add note: %d %s", rec.Code, rec.Body.String())
	}

	// The webhook is delivered asynchronously.
	waitFor(t, func() bool { return disp.Stats().Delivered == 1 })

	mu.Lock()
	defer mu.Unlock()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload: %v (%s)", err, body)
	}
	if p["eventType"] != "note_added" || p["patientId"] != "pat-a" || p["appointmentId"] != apptID {
		t.Errorf("payload top-level = %+v", p)
	}
	data, _ := p["data"].(map[string]any)
	if data["noteText"] != "Reports headache." {
		t.Errorf("data = %+v, want noteText", data)
	}
}

// TestWebhookDelivery_NotSubscribedNoDelivery proves a note for a patient with no
// matching subscription produces no delivery.
func TestWebhookDelivery_NotSubscribedNoDelivery(t *testing.T) {
	srv, disp := newDeliveringServer(t)
	defer disp.Stop(context.Background())

	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/notes", "hosp-A", "doc-a",
		map[string]string{"text": "note"}); rec.Code != http.StatusCreated {
		t.Fatalf("add note: %d", rec.Code)
	}

	// Give any (erroneous) delivery a chance to happen, then assert none did.
	time.Sleep(50 * time.Millisecond)
	if s := disp.Stats(); s.Delivered != 0 || s.Dropped != 0 {
		t.Errorf("stats = %+v, want no deliveries", s)
	}
}
