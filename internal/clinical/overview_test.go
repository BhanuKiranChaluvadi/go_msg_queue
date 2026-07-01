package clinical

import (
	"context"
	"errors"
	"testing"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
)

func overviewFixtures() (*Service, *memory.DiagnosisStore, *memory.AppointmentStore, *memory.NoteStore, *memory.PrescriptionStore) {
	diag := memory.NewDiagnosisStore()
	appt := memory.NewAppointmentStore()
	note := memory.NewNoteStore()
	rx := memory.NewPrescriptionStore()
	svc := NewService(Deps{
		Diagnoses:     diag,
		Appointments:  appt,
		Notes:         note,
		Prescriptions: rx,
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("dx-"),
		Events:        &capturePublisher{},
	})
	return svc, diag, appt, note, rx
}

func patientCtx(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(), tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RolePatient})
}

func pharmacistCtx(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(), tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RolePharmacist})
}

func at(h int) time.Time { return testNow.Add(time.Duration(h) * time.Hour) }

// seedHistory creates a patient history: a diagnosis (t1..dismissed t3), a
// prescription (issued t1, expires t5, dispatched t3), and an appointment (t1)
// with a note (t2).
func seedHistory(t *testing.T, diag *memory.DiagnosisStore, appt *memory.AppointmentStore, note *memory.NoteStore, rx *memory.PrescriptionStore) {
	t.Helper()
	ctx := context.Background()
	dismissed := at(3)
	dispatched := at(3)
	_ = diag.Create(ctx, domain.Diagnosis{ID: "dx-1", TenantID: "hosp-A", PatientID: "pat-1", Disease: "Flu", DiagnosedAt: at(1), DismissedAt: &dismissed})
	_ = rx.Create(ctx, domain.Prescription{ID: "rx-1", TenantID: "hosp-A", AppointmentID: "appt-1", PatientID: "pat-1", Medication: "Aspirin", IssuedAt: at(1), ExpiresAt: at(5), DispatchedAt: &dispatched, Status: domain.PrescriptionDispatched})
	_ = appt.Create(ctx, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1", Start: at(1), CreatedAt: at(1), Status: domain.AppointmentScheduled})
	_ = note.Create(ctx, domain.Note{ID: "n-1", TenantID: "hosp-A", AppointmentID: "appt-1", Text: "note", CreatedAt: at(2)})
}

func TestOverview_PointInTime(t *testing.T) {
	svc, diag, appt, note, rx := overviewFixtures()
	seedHistory(t, diag, appt, note, rx)
	ctx := doctorCtx("hosp-A", "doc-1")

	// Before anything existed.
	ov, err := svc.Overview(ctx, "pat-1", at(0))
	if err != nil {
		t.Fatalf("overview t0: %v", err)
	}
	if len(ov.Diagnoses) != 0 || len(ov.ActivePrescriptions) != 0 || len(ov.Appointments) != 0 {
		t.Errorf("t0 overview = %+v, want all empty", ov)
	}

	// At t2: diagnosis active, prescription active, appointment with its note.
	ov, _ = svc.Overview(ctx, "pat-1", at(2))
	if len(ov.Diagnoses) != 1 {
		t.Errorf("t2 diagnoses = %d, want 1 (active, not yet dismissed)", len(ov.Diagnoses))
	}
	if len(ov.ActivePrescriptions) != 1 {
		t.Errorf("t2 prescriptions = %d, want 1 (active, not yet dispatched)", len(ov.ActivePrescriptions))
	}
	if len(ov.Appointments) != 1 || len(ov.Appointments[0].Notes) != 1 {
		t.Errorf("t2 appointments = %+v, want 1 appt with 1 note", ov.Appointments)
	}

	// At t4: diagnosis dismissed and prescription dispatched (both inactive);
	// the appointment and note remain.
	ov, _ = svc.Overview(ctx, "pat-1", at(4))
	if len(ov.Diagnoses) != 0 {
		t.Errorf("t4 diagnoses = %d, want 0 (dismissed at t3)", len(ov.Diagnoses))
	}
	if len(ov.ActivePrescriptions) != 0 {
		t.Errorf("t4 prescriptions = %d, want 0 (dispatched at t3)", len(ov.ActivePrescriptions))
	}
	if len(ov.Appointments) != 1 || len(ov.Appointments[0].Notes) != 1 {
		t.Errorf("t4 appointments = %+v, want 1 appt with 1 note", ov.Appointments)
	}
}

func TestOverview_DefaultsToNow(t *testing.T) {
	svc, diag, appt, note, rx := overviewFixtures()
	seedHistory(t, diag, appt, note, rx)
	// Fake clock is testNow (== t0), before the seeded history, so a zero "at"
	// resolves to now and yields an empty overview.
	ov, err := svc.Overview(doctorCtx("hosp-A", "doc-1"), "pat-1", time.Time{})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if !ov.At.Equal(testNow) {
		t.Errorf("At = %v, want defaulted to now (%v)", ov.At, testNow)
	}
}

func TestOverview_Authorization(t *testing.T) {
	svc, diag, appt, note, rx := overviewFixtures()
	seedHistory(t, diag, appt, note, rx)

	// Patient can view their own.
	if _, err := svc.Overview(patientCtx("hosp-A", "pat-1"), "pat-1", at(2)); err != nil {
		t.Errorf("self view: %v", err)
	}
	// A different patient cannot.
	if _, err := svc.Overview(patientCtx("hosp-A", "pat-2"), "pat-1", at(2)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("other patient err = %v, want ErrForbidden", err)
	}
	// A doctor can view any patient.
	if _, err := svc.Overview(doctorCtx("hosp-A", "doc-9"), "pat-1", at(2)); err != nil {
		t.Errorf("doctor view: %v", err)
	}
	// A pharmacist cannot.
	if _, err := svc.Overview(pharmacistCtx("hosp-A", "pharm-1"), "pat-1", at(2)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("pharmacist err = %v, want ErrForbidden", err)
	}
	// Cross-tenant doctor sees an empty overview (tenant-scoped reads).
	ov, err := svc.Overview(doctorCtx("hosp-B", "doc-1"), "pat-1", at(2))
	if err != nil {
		t.Fatalf("cross-tenant: %v", err)
	}
	if len(ov.Diagnoses) != 0 || len(ov.ActivePrescriptions) != 0 || len(ov.Appointments) != 0 {
		t.Errorf("cross-tenant overview = %+v, want empty", ov)
	}
}
