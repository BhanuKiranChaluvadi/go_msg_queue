package clinical

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
)

var testNow = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

type capturePublisher struct {
	mu     sync.Mutex
	events []domain.Event
}

func (c *capturePublisher) Publish(_ context.Context, e domain.Event) domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return e
}

func (c *capturePublisher) byType(t domain.EventType) []domain.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.Event
	for _, e := range c.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func clinicalFixtures() (*Service, *memory.DiagnosisStore, *capturePublisher) {
	diag := memory.NewDiagnosisStore()
	pub := &capturePublisher{}
	svc := NewService(Deps{
		Diagnoses:     diag,
		Appointments:  memory.NewAppointmentStore(),
		Notes:         memory.NewNoteStore(),
		Prescriptions: memory.NewPrescriptionStore(),
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("dx-"),
		Events:        pub,
	})
	return svc, diag, pub
}

func doctorCtx(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RoleDoctor})
}

func TestDiagnose_Success(t *testing.T) {
	svc, store, pub := clinicalFixtures()
	d, err := svc.Diagnose(doctorCtx("hosp-A", "doc-1"), "pat-1", "Hypertension")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.PatientID != "pat-1" || d.Disease != "Hypertension" || d.DismissedAt != nil {
		t.Errorf("diagnosis = %+v", d)
	}
	if _, err := store.Get(context.Background(), "hosp-A", d.ID); err != nil {
		t.Errorf("not stored: %v", err)
	}
	if got := pub.byType(domain.EventDiagnosisAdded); len(got) != 1 || got[0].Payload["disease"] != "Hypertension" {
		t.Errorf("events = %+v, want one diagnosis_added", got)
	}
}

func TestDiagnose_Validation(t *testing.T) {
	svc, _, _ := clinicalFixtures()
	if _, err := svc.Diagnose(doctorCtx("hosp-A", "doc-1"), "pat-1", "  "); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty disease err = %v, want ErrValidation", err)
	}
	if _, err := svc.Diagnose(doctorCtx("hosp-A", "doc-1"), "", "flu"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty patient err = %v, want ErrValidation", err)
	}
	if _, err := svc.Diagnose(context.Background(), "pat-1", "flu"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no-actor err = %v, want ErrForbidden", err)
	}
}

func TestDismissDiagnosis(t *testing.T) {
	svc, store, pub := clinicalFixtures()
	d, _ := svc.Diagnose(doctorCtx("hosp-A", "doc-1"), "pat-1", "Flu")

	if err := svc.DismissDiagnosis(doctorCtx("hosp-A", "doc-1"), "pat-1", d.ID); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	got, _ := store.Get(context.Background(), "hosp-A", d.ID)
	if got.DismissedAt == nil {
		t.Error("DismissedAt should be set")
	}
	if len(pub.byType(domain.EventDiagnosisDismissed)) != 1 {
		t.Error("want one diagnosis_dismissed event")
	}
	// Dismissing again is a conflict.
	if err := svc.DismissDiagnosis(doctorCtx("hosp-A", "doc-1"), "pat-1", d.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-dismiss err = %v, want ErrConflict", err)
	}
}

func TestDismissDiagnosis_UnknownWrongPatientCrossTenant(t *testing.T) {
	svc, _, _ := clinicalFixtures()
	d, _ := svc.Diagnose(doctorCtx("hosp-A", "doc-1"), "pat-1", "Flu")

	if err := svc.DismissDiagnosis(doctorCtx("hosp-A", "doc-1"), "pat-1", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown err = %v, want ErrNotFound", err)
	}
	// Right diagnosis id but wrong patient -> not found (no info leak).
	if err := svc.DismissDiagnosis(doctorCtx("hosp-A", "doc-1"), "pat-2", d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("wrong-patient err = %v, want ErrNotFound", err)
	}
	// Cross-tenant.
	if err := svc.DismissDiagnosis(doctorCtx("hosp-B", "doc-1"), "pat-1", d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
}
