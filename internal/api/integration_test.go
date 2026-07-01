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

	"medconnect/internal/analytics"
	"medconnect/internal/appointments"
	"medconnect/internal/audit"
	"medconnect/internal/clinical"
	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/transcription"
	"medconnect/internal/webhooks"
)

// captureReceiver records webhook POST bodies.
type captureReceiver struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *captureReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, b)
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (c *captureReceiver) eventTypes() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for _, b := range c.bodies {
		var p map[string]any
		if json.Unmarshal(b, &p) == nil {
			if s, ok := p["eventType"].(string); ok {
				out[s]++
			}
		}
	}
	return out
}

// fullServer wires the entire application (all services + embedded workers) with
// fake external transcription and webhook endpoints, for black-box journey tests.
type fullServer struct {
	srv     *Server
	disp    *webhooks.Dispatcher
	recv    *captureReceiver
	recvURL string
	cleanup func()
}

func newFullServer(t *testing.T) *fullServer {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	idgen := platform.NewFakeIDGen("req-")
	clock := platform.NewFakeClock(apiTestNow)
	eventStore := events.NewStore()
	publisher := events.NewPublisher(eventStore, platform.SystemClock{}, idgen)

	apptStore := memory.NewAppointmentStore()
	noteStore := memory.NewNoteStore()
	rxStore := memory.NewPrescriptionStore()
	appts := appointments.NewService(appointments.Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  apptStore,
		Notes:         noteStore,
		Prescriptions: rxStore,
		Clock:         clock,
		IDGen:         platform.NewFakeIDGen("ap-"),
		Events:        publisher,
	})
	clinicalSvc := clinical.NewService(clinical.Deps{
		Diagnoses:     memory.NewDiagnosisStore(),
		Appointments:  apptStore,
		Notes:         noteStore,
		Prescriptions: rxStore,
		Clock:         clock,
		IDGen:         platform.NewFakeIDGen("dx-"),
		Events:        publisher,
	})

	registry := webhooks.NewRegistry(memory.NewWebhookStore(), platform.NewFakeIDGen("wh-"))
	disp := webhooks.NewDispatcher(webhooks.Config{
		Workers: 2, QueueSize: 16, MaxAttempts: 3,
		BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, Logger: discard,
	})
	disp.Start()
	publisher.Subscribe(webhooks.NewSubscriber(registry, disp, discard))

	// Fake transcription server streams a complete note.
	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range []struct {
			seq   int
			text  string
			final bool
		}{{0, "Patient is ", false}, {1, "stable.", true}} {
			w.Write([]byte("data: {\"sequence\":" + itoa(c.seq) + ",\"text\":\"" + c.text + "\",\"isFinal\":" + btoa(c.final) + "}\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	mgr := transcription.NewManager(transcription.Config{
		Notes: appts, Source: fixedSource{url: sse.URL}, Timeout: 2 * time.Second, Logger: discard,
	})

	recv := &captureReceiver{}
	recvSrv := httptest.NewServer(recv)

	srv := &Server{
		Logger:        discard,
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: "secret",
		Resolver:      apiTestResolver(),
		Appointments:  appts,
		Webhooks:      registry,
		Transcription: mgr,
		Clinical:      clinicalSvc,
		Audit:         audit.NewService(eventStore),
		Analytics:     analytics.NewService(eventStore),
	}
	return &fullServer{
		srv:     srv,
		disp:    disp,
		recv:    recv,
		recvURL: recvSrv.URL,
		cleanup: func() {
			mgr.Stop(context.Background())
			disp.Stop(context.Background())
			sse.Close()
			recvSrv.Close()
		},
	}
}

func itoa(n int) string { return string(rune('0' + n)) }
func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestFullFlow exercises the whole system through the HTTP API, across every
// implemented feature, for one patient journey.
func TestFullFlow(t *testing.T) {
	f := newFullServer(t)
	defer f.cleanup()
	srv := f.srv

	// 1. Patient subscribes to live updates.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/webhooks", "hosp-A", "pat-a",
		webhookReq{URL: f.recvURL, EventTypes: []domain.EventType{domain.EventNoteAdded, domain.EventPrescriptionAdded}}); rec.Code != http.StatusCreated {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}

	// 2-3. Doctor publishes a slot; patient books it.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)

	// 4. Doctor dictates a note via transcription -> stored + note_added -> webhook.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/transcription", "hosp-A", "doc-a", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("transcription: %d", rec.Code)
	}

	// 5. Doctor issues a prescription -> prescription_added -> webhook.
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("prescription: %d", rec.Code)
	}
	var rx domain.Prescription
	_ = json.Unmarshal(rec.Body.Bytes(), &rx)

	// Both webhook deliveries land.
	waitFor(t, func() bool { return f.disp.Stats().Delivered == 2 })
	if types := f.recv.eventTypes(); types["note_added"] != 1 || types["prescription_added"] != 1 {
		t.Errorf("webhook events = %v, want one note_added and one prescription_added", types)
	}

	// 6. Pharmacist lists active prescriptions and dispatches.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions?status=active", "hosp-A", "pharm-a", nil); rec.Code != http.StatusOK {
		t.Fatalf("list active: %d", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/prescriptions/"+rx.ID+"/dispatch", "hosp-A", "pharm-a", nil); rec.Code != http.StatusOK {
		t.Fatalf("dispatch: %d %s", rec.Code, rec.Body.String())
	}

	// 7. Doctor diagnoses the patient.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Hypertension"}); rec.Code != http.StatusCreated {
		t.Fatalf("diagnose: %d", rec.Code)
	}

	// 8. Appointment overview shows the dictation note and the prescription.
	rec = doRequest(t, srv, http.MethodGet, "/v1/appointments/"+apptID, "hosp-A", "pat-a", nil)
	var ov appointments.Overview
	_ = json.Unmarshal(rec.Body.Bytes(), &ov)
	if len(ov.Notes) != 1 || ov.Notes[0].Source != domain.NoteSourceDictation || len(ov.Prescriptions) != 1 {
		t.Errorf("appointment overview = %d notes / %d rx, want 1 dictation note + 1 rx", len(ov.Notes), len(ov.Prescriptions))
	}

	// 9. Patient overview: diagnosis active; prescription dispatched (inactive now).
	rec = doRequest(t, srv, http.MethodGet, "/v1/patients/pat-a/overview", "hosp-A", "pat-a", nil)
	var pov clinical.PatientOverview
	_ = json.Unmarshal(rec.Body.Bytes(), &pov)
	if len(pov.Diagnoses) != 1 {
		t.Errorf("overview diagnoses = %d, want 1", len(pov.Diagnoses))
	}
	if len(pov.ActivePrescriptions) != 0 {
		t.Errorf("overview active prescriptions = %d, want 0 (dispatched)", len(pov.ActivePrescriptions))
	}

	// 10. Audit trail captures the mutations.
	rec = doRequest(t, srv, http.MethodGet, "/v1/audit?patientId=pat-a", "hosp-A", "doc-a", nil)
	var recs []audit.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &recs)
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.Type] = true
	}
	for _, want := range []string{"appointment_booked", "note_added", "prescription_added", "prescription_dispatched", "diagnosis_added"} {
		if !seen[want] {
			t.Errorf("audit missing %q; saw %v", want, seen)
		}
	}

	// 11. Analytics reflects the usage.
	rec = doRequest(t, srv, http.MethodGet, "/v1/analytics", "hosp-A", "doc-a", nil)
	var sum analytics.Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &sum)
	if sum.TotalAppointments != 1 || sum.ActivePatients != 1 || sum.PrescriptionCounts.Issued != 1 || sum.PrescriptionCounts.Dispatched != 1 {
		t.Errorf("analytics = %+v, want 1/1/{1,1}", sum)
	}
}
