package appointments

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

func overviewFixtures() (*Service, *memory.AppointmentStore, *memory.NoteStore, *memory.PrescriptionStore) {
	apptStore := memory.NewAppointmentStore()
	noteStore := memory.NewNoteStore()
	rxStore := memory.NewPrescriptionStore()
	svc := NewService(Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  apptStore,
		Notes:         noteStore,
		Prescriptions: rxStore,
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("ov-"),
		Events:        &capturePublisher{},
	})
	return svc, apptStore, noteStore, rxStore
}

func pharmacistCtx(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RolePharmacist})
}

// seedOverview creates an appointment for doc-1/pat-1 with two out-of-order notes
// and one prescription, and returns the service.
func seedOverview(t *testing.T) (*Service, *memory.NoteStore) {
	t.Helper()
	svc, apptStore, noteStore, rxStore := overviewFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1", Status: domain.AppointmentScheduled})

	ctx := context.Background()
	_ = noteStore.Create(ctx, domain.Note{ID: "n-late", TenantID: "hosp-A", AppointmentID: "appt-1", Text: "second", CreatedAt: testNow.Add(2 * time.Hour)})
	_ = noteStore.Create(ctx, domain.Note{ID: "n-early", TenantID: "hosp-A", AppointmentID: "appt-1", Text: "first", CreatedAt: testNow.Add(1 * time.Hour)})
	_ = rxStore.Create(ctx, domain.Prescription{ID: "rx-1", TenantID: "hosp-A", AppointmentID: "appt-1", PatientID: "pat-1", Medication: "Aspirin", IssuedAt: testNow, ExpiresAt: testNow.Add(24 * time.Hour), Status: domain.PrescriptionActive})
	return svc, noteStore
}

func TestAppointmentOverview_ParticipantsSeeEverything(t *testing.T) {
	svc, _ := seedOverview(t)

	for _, ctx := range []context.Context{doctorCtx("hosp-A", "doc-1"), patientCtx("hosp-A", "pat-1")} {
		ov, err := svc.AppointmentOverview(ctx, "appt-1")
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		if ov.Appointment.ID != "appt-1" {
			t.Errorf("appointment = %+v", ov.Appointment)
		}
		// Notes are returned oldest-first.
		if len(ov.Notes) != 2 || ov.Notes[0].ID != "n-early" || ov.Notes[1].ID != "n-late" {
			t.Errorf("notes = %v, want [n-early n-late]", noteIDs(ov.Notes))
		}
		if len(ov.Prescriptions) != 1 || ov.Prescriptions[0].ID != "rx-1" {
			t.Errorf("prescriptions = %v, want [rx-1]", ov.Prescriptions)
		}
	}
}

func TestAppointmentOverview_ReportsEffectivePrescriptionStatus(t *testing.T) {
	svc, apptStore, _, rxStore := overviewFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1", Status: domain.AppointmentScheduled})

	// Issued and expired before the fake clock's "now", but still stored as active
	// because nothing sweeps it. The overview must report it as expired.
	_ = rxStore.Create(context.Background(), domain.Prescription{
		ID: "rx-exp", TenantID: "hosp-A", AppointmentID: "appt-1", PatientID: "pat-1",
		Medication: "Old", IssuedAt: testNow.Add(-48 * time.Hour), ExpiresAt: testNow.Add(-24 * time.Hour),
		Status: domain.PrescriptionActive,
	})

	ov, err := svc.AppointmentOverview(doctorCtx("hosp-A", "doc-1"), "appt-1")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(ov.Prescriptions) != 1 || ov.Prescriptions[0].Status != domain.PrescriptionExpired {
		t.Errorf("prescription status = %+v, want expired", ov.Prescriptions)
	}
}

func TestAppointmentOverview_NonParticipantsForbidden(t *testing.T) {
	svc, _ := seedOverview(t)
	tests := map[string]context.Context{
		"other patient": patientCtx("hosp-A", "pat-2"),
		"other doctor":  doctorCtx("hosp-A", "doc-2"),
		"pharmacist":    pharmacistCtx("hosp-A", "pharm-1"),
	}
	for name, ctx := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.AppointmentOverview(ctx, "appt-1"); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("err = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestAppointmentOverview_UnknownAndCrossTenant(t *testing.T) {
	svc, _ := seedOverview(t)
	// Unknown appointment.
	if _, err := svc.AppointmentOverview(doctorCtx("hosp-A", "doc-1"), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown err = %v, want ErrNotFound", err)
	}
	// Cross-tenant: the participant id exists but the appointment is in hosp-A.
	if _, err := svc.AppointmentOverview(doctorCtx("hosp-B", "doc-1"), "appt-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
}

func TestAppointmentOverview_NoActorForbidden(t *testing.T) {
	svc, _ := seedOverview(t)
	if _, err := svc.AppointmentOverview(context.Background(), "appt-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func noteIDs(ns []domain.Note) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}
