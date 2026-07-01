package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
)

// fakeEnqueuer captures deliveries for assertions.
type fakeEnqueuer struct {
	mu  sync.Mutex
	got []Delivery
}

func (f *fakeEnqueuer) Enqueue(d Delivery) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, d)
	return true
}

func (f *fakeEnqueuer) deliveries() []Delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Delivery, len(f.got))
	copy(out, f.got)
	return out
}

func regWith(t *testing.T) (*Registry, *fakeEnqueuer, *Subscriber) {
	t.Helper()
	reg := NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-"))
	enq := &fakeEnqueuer{}
	return reg, enq, NewSubscriber(reg, enq, quietLogger())
}

func register(t *testing.T, reg *Registry, tenant, patient, url string, types ...domain.EventType) domain.Webhook {
	t.Helper()
	wh, err := reg.Register(patientCtx(tenant, patient), RegisterInput{URL: url, EventTypes: types})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return wh
}

func noteEvent(tenant, patient string) domain.Event {
	return domain.Event{
		ID: "ev-note", TenantID: tenant, Type: domain.EventNoteAdded,
		ActorID: "doc-1", Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Payload: map[string]any{"noteId": "note-1", "noteText": "Patient reports...", "appointmentId": "appt-1", "patientId": patient},
	}
}

func TestSubscriber_MapsNoteAddedPayload(t *testing.T) {
	reg, enq, sub := regWith(t)
	wh := register(t, reg, "hosp-A", "pat-1", "https://a.test/hook", domain.EventNoteAdded)

	sub.Notify(context.Background(), noteEvent("hosp-A", "pat-1"))

	ds := enq.deliveries()
	if len(ds) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(ds))
	}
	if ds[0].WebhookID != wh.ID || ds[0].URL != wh.URL || ds[0].Secret != wh.Secret || ds[0].EventID != "ev-note" {
		t.Errorf("delivery target = %+v, want webhook %s", ds[0], wh.ID)
	}

	var p map[string]any
	if err := json.Unmarshal(ds[0].Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p["eventId"] != "ev-note" || p["eventType"] != "note_added" || p["appointmentId"] != "appt-1" || p["patientId"] != "pat-1" {
		t.Errorf("payload top-level = %+v", p)
	}
	data, _ := p["data"].(map[string]any)
	if data["noteId"] != "note-1" || data["noteText"] != "Patient reports..." {
		t.Errorf("data = %+v, want noteId/noteText", data)
	}
	if _, ok := p["timestamp"]; !ok {
		t.Error("payload missing timestamp")
	}
}

func TestSubscriber_MapsPrescriptionAdded(t *testing.T) {
	reg, enq, sub := regWith(t)
	register(t, reg, "hosp-A", "pat-1", "https://a.test/hook", domain.EventPrescriptionAdded)

	sub.Notify(context.Background(), domain.Event{
		ID: "ev-rx", TenantID: "hosp-A", Type: domain.EventPrescriptionAdded,
		Timestamp: time.Now(),
		Payload:   map[string]any{"prescriptionId": "rx-1", "medication": "Aspirin 100mg", "expiresAt": "2025-02-15T00:00:00Z", "appointmentId": "appt-1", "patientId": "pat-1"},
	})

	ds := enq.deliveries()
	if len(ds) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(ds))
	}
	var p map[string]any
	_ = json.Unmarshal(ds[0].Payload, &p)
	data, _ := p["data"].(map[string]any)
	if data["prescriptionId"] != "rx-1" || data["medication"] != "Aspirin 100mg" || data["expiresAt"] != "2025-02-15T00:00:00Z" {
		t.Errorf("data = %+v", data)
	}
}

func TestSubscriber_FiltersByPatientEventTypeAndTenant(t *testing.T) {
	reg, enq, sub := regWith(t)
	// pat-1 wants note_added; pat-1 also has a prescription-only sub; pat-2 wants note_added;
	// a hosp-B pat-1 wants note_added.
	whNote := register(t, reg, "hosp-A", "pat-1", "https://a-note.test", domain.EventNoteAdded)
	register(t, reg, "hosp-A", "pat-1", "https://a-rx.test", domain.EventPrescriptionAdded)
	register(t, reg, "hosp-A", "pat-2", "https://a-other.test", domain.EventNoteAdded)
	register(t, reg, "hosp-B", "pat-1", "https://b.test", domain.EventNoteAdded)

	sub.Notify(context.Background(), noteEvent("hosp-A", "pat-1"))

	ds := enq.deliveries()
	if len(ds) != 1 {
		t.Fatalf("deliveries = %d, want exactly 1 (pat-1 note_added in hosp-A)", len(ds))
	}
	if ds[0].WebhookID != whNote.ID {
		t.Errorf("delivered to %s, want the pat-1 note_added sub %s", ds[0].WebhookID, whNote.ID)
	}
}

func TestSubscriber_IgnoresNonDeliverableEvents(t *testing.T) {
	reg, enq, sub := regWith(t)
	register(t, reg, "hosp-A", "pat-1", "https://a.test", domain.EventNoteAdded)

	sub.Notify(context.Background(), domain.Event{
		ID: "ev", TenantID: "hosp-A", Type: domain.EventAppointmentBooked,
		Payload: map[string]any{"patientId": "pat-1"},
	})
	if len(enq.deliveries()) != 0 {
		t.Error("appointment_booked should not produce a webhook delivery")
	}
}

// TestSubscriber_EndToEndOverHTTP proves Subscriber -> real Dispatcher -> HTTP with
// a correct, signature-verifiable payload.
func TestSubscriber_EndToEndOverHTTP(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
		sig  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body, sig = b, r.Header.Get("X-Signature")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-"))
	wh := register(t, reg, "hosp-A", "pat-1", srv.URL, domain.EventNoteAdded)

	disp := NewDispatcher(fastConfig(2, 3))
	disp.Start()
	defer disp.Stop(context.Background())
	sub := NewSubscriber(reg, disp, quietLogger())

	sub.Notify(context.Background(), noteEvent("hosp-A", "pat-1"))

	waitFor(t, func() bool { return disp.Stats().Delivered == 1 })

	mu.Lock()
	defer mu.Unlock()
	if want := Sign(wh.Secret, body); sig != want {
		t.Errorf("signature = %q, want %q", sig, want)
	}
	var p map[string]any
	_ = json.Unmarshal(body, &p)
	if p["eventType"] != "note_added" || p["patientId"] != "pat-1" {
		t.Errorf("received payload = %+v", p)
	}
}
