package analytics

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

func booked(store *events.Store, tenant, patient string) {
	store.Append(domain.Event{ID: patient + "-b", TenantID: tenant, Type: domain.EventAppointmentBooked, Timestamp: base, Payload: map[string]any{"patientId": patient}})
}

func TestSummarize_Counts(t *testing.T) {
	store := events.NewStore()
	// hosp-A: pat-1 books twice, pat-2 books once -> 3 appointments, 2 active patients.
	booked(store, "hosp-A", "pat-1")
	store.Append(domain.Event{ID: "p1-b2", TenantID: "hosp-A", Type: domain.EventAppointmentBooked, Timestamp: base, Payload: map[string]any{"patientId": "pat-1"}})
	booked(store, "hosp-A", "pat-2")
	// 2 prescriptions issued, 1 dispatched.
	store.Append(domain.Event{ID: "rx1", TenantID: "hosp-A", Type: domain.EventPrescriptionAdded, Timestamp: base, Payload: map[string]any{"patientId": "pat-1"}})
	store.Append(domain.Event{ID: "rx2", TenantID: "hosp-A", Type: domain.EventPrescriptionAdded, Timestamp: base, Payload: map[string]any{"patientId": "pat-2"}})
	store.Append(domain.Event{ID: "rx1d", TenantID: "hosp-A", Type: domain.EventPrescriptionDispatched, Timestamp: base, Payload: map[string]any{"patientId": "pat-1"}})
	// Non-counted event.
	store.Append(domain.Event{ID: "n1", TenantID: "hosp-A", Type: domain.EventNoteAdded, Timestamp: base, Payload: map[string]any{"patientId": "pat-1"}})

	svc := NewService(store)
	sum, err := svc.Summarize(doctorCtx("hosp-A"))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if sum.TotalAppointments != 3 {
		t.Errorf("totalAppointments = %d, want 3", sum.TotalAppointments)
	}
	if sum.ActivePatients != 2 {
		t.Errorf("activePatients = %d, want 2 (distinct)", sum.ActivePatients)
	}
	if sum.PrescriptionCounts.Issued != 2 || sum.PrescriptionCounts.Dispatched != 1 {
		t.Errorf("prescriptionCounts = %+v, want {2,1}", sum.PrescriptionCounts)
	}
}

func TestSummarize_TenantIsolation(t *testing.T) {
	store := events.NewStore()
	booked(store, "hosp-A", "pat-1")
	booked(store, "hosp-B", "pat-9")
	booked(store, "hosp-B", "pat-8")

	svc := NewService(store)
	a, _ := svc.Summarize(doctorCtx("hosp-A"))
	b, _ := svc.Summarize(doctorCtx("hosp-B"))
	if a.TotalAppointments != 1 || a.ActivePatients != 1 {
		t.Errorf("hosp-A = %+v, want 1 appt / 1 patient", a)
	}
	if b.TotalAppointments != 2 || b.ActivePatients != 2 {
		t.Errorf("hosp-B = %+v, want 2 appts / 2 patients", b)
	}
}

func TestSummarize_EmptyAndNoTenant(t *testing.T) {
	svc := NewService(events.NewStore())
	// Empty tenant -> zero summary.
	sum, err := svc.Summarize(doctorCtx("hosp-A"))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if sum.TotalAppointments != 0 || sum.ActivePatients != 0 {
		t.Errorf("empty summary = %+v, want zeros", sum)
	}
	// No tenant in context -> forbidden.
	if _, err := svc.Summarize(context.Background()); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
