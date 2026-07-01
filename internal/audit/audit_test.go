package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/tenancy"
)

var base = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

func doctorCtx(tenant string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: "doc-1", TenantID: tenant, Role: domain.RoleDoctor})
}

func seed(store *events.Store) {
	store.Append(domain.Event{ID: "e1", TenantID: "hosp-A", Type: domain.EventAppointmentBooked, ActorID: "pat-1", EntityRef: "appt-1", Timestamp: base.Add(1 * time.Hour), Payload: map[string]any{"patientId": "pat-1"}})
	store.Append(domain.Event{ID: "e2", TenantID: "hosp-A", Type: domain.EventNoteAdded, ActorID: "doc-1", EntityRef: "appt-1", Timestamp: base.Add(2 * time.Hour), Payload: map[string]any{"patientId": "pat-1", "noteId": "n1"}})
	store.Append(domain.Event{ID: "e3", TenantID: "hosp-A", Type: domain.EventNoteAdded, ActorID: "doc-1", EntityRef: "appt-2", Timestamp: base.Add(3 * time.Hour), Payload: map[string]any{"patientId": "pat-2"}})
	store.Append(domain.Event{ID: "b1", TenantID: "hosp-B", Type: domain.EventNoteAdded, ActorID: "doc-9", EntityRef: "appt-9", Timestamp: base.Add(1 * time.Hour), Payload: map[string]any{"patientId": "pat-1"}})
}

func TestAudit_QueryByPatient(t *testing.T) {
	store := events.NewStore()
	seed(store)
	svc := NewService(store)

	recs, err := svc.Query(doctorCtx("hosp-A"), Query{PatientID: "pat-1"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (pat-1 in hosp-A)", len(recs))
	}
	// Ordered oldest-first, and carry who + when.
	if recs[0].EventID != "e1" || recs[1].EventID != "e2" {
		t.Errorf("order = %s,%s, want e1,e2", recs[0].EventID, recs[1].EventID)
	}
	if recs[0].ActorID != "pat-1" || recs[0].Timestamp.IsZero() {
		t.Errorf("record missing actor/timestamp: %+v", recs[0])
	}
}

func TestAudit_FilterByTypeAndTime(t *testing.T) {
	store := events.NewStore()
	seed(store)
	svc := NewService(store)

	// Only note_added events for pat-1.
	recs, _ := svc.Query(doctorCtx("hosp-A"), Query{PatientID: "pat-1", Types: []domain.EventType{domain.EventNoteAdded}})
	if len(recs) != 1 || recs[0].EventID != "e2" {
		t.Errorf("type filter = %+v, want [e2]", recs)
	}

	// Time window excluding the first hour.
	recs, _ = svc.Query(doctorCtx("hosp-A"), Query{From: base.Add(2 * time.Hour)})
	for _, r := range recs {
		if r.Timestamp.Before(base.Add(2 * time.Hour)) {
			t.Errorf("record %s before From bound", r.EventID)
		}
	}
}

func TestAudit_TenantIsolation(t *testing.T) {
	store := events.NewStore()
	seed(store)
	svc := NewService(store)

	// hosp-A does not see hosp-B's event b1 (also pat-1).
	recs, _ := svc.Query(doctorCtx("hosp-A"), Query{PatientID: "pat-1"})
	for _, r := range recs {
		if r.EventID == "b1" {
			t.Error("cross-tenant leak: hosp-A saw hosp-B event")
		}
	}
	// hosp-B sees only its own.
	recs, _ = svc.Query(doctorCtx("hosp-B"), Query{})
	if len(recs) != 1 || recs[0].EventID != "b1" {
		t.Errorf("hosp-B audit = %+v, want [b1]", recs)
	}
}

func TestAudit_NoTenantForbidden(t *testing.T) {
	svc := NewService(events.NewStore())
	if _, err := svc.Query(context.Background(), Query{}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
