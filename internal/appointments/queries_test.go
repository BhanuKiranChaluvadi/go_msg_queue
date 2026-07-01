package appointments

import (
	"context"
	"errors"
	"testing"

	"medconnect/internal/domain"
	"medconnect/internal/store/memory"
)

// seedAppt inserts an appointment directly into the store for query tests.
func seedAppt(t *testing.T, store *memory.AppointmentStore, a domain.Appointment) {
	t.Helper()
	if err := store.Create(context.Background(), a); err != nil {
		t.Fatalf("seed appt: %v", err)
	}
}

func TestNextAppointments_FilterOrderScope(t *testing.T) {
	svc, _, apptStore, _ := bookingFixtures()

	// doc-1 in hosp-A: future (out of order), a cancelled future one, and a past one.
	seedAppt(t, apptStore, domain.Appointment{ID: "a-late", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "p1", Start: hour(5), Status: domain.AppointmentScheduled})
	seedAppt(t, apptStore, domain.Appointment{ID: "a-soon", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "p2", Start: hour(1), Status: domain.AppointmentScheduled})
	seedAppt(t, apptStore, domain.Appointment{ID: "a-cancelled", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "p3", Start: hour(2), Status: domain.AppointmentCancelled})
	seedAppt(t, apptStore, domain.Appointment{ID: "a-past", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "p4", Start: hour(-2), Status: domain.AppointmentScheduled})
	// A different doctor and a different tenant must not appear.
	seedAppt(t, apptStore, domain.Appointment{ID: "a-otherdoc", TenantID: "hosp-A", DoctorID: "doc-2", PatientID: "p5", Start: hour(1), Status: domain.AppointmentScheduled})
	seedAppt(t, apptStore, domain.Appointment{ID: "a-othertenant", TenantID: "hosp-B", DoctorID: "doc-1", PatientID: "p6", Start: hour(1), Status: domain.AppointmentScheduled})

	got, err := svc.NextAppointments(doctorCtx("hosp-A", "doc-1"))
	if err != nil {
		t.Fatalf("NextAppointments: %v", err)
	}
	want := []string{"a-soon", "a-late"} // future, non-cancelled, soonest first
	if len(got) != len(want) {
		t.Fatalf("got %d appts, want %d: %v", len(got), len(want), apptIDs(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("appt[%d] = %q, want %q: %v", i, got[i].ID, id, apptIDs(got))
		}
	}
}

func TestNextAppointments_NoActorForbidden(t *testing.T) {
	svc, _, _, _ := bookingFixtures()
	if _, err := svc.NextAppointments(context.Background()); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func apptIDs(as []domain.Appointment) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}
