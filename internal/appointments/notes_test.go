package appointments

import (
	"context"
	"errors"
	"testing"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
)

func noteFixtures() (*Service, *memory.AppointmentStore, *memory.NoteStore, *capturePublisher) {
	apptStore := memory.NewAppointmentStore()
	noteStore := memory.NewNoteStore()
	pub := &capturePublisher{}
	svc := NewService(Deps{
		Timeslots:     memory.NewTimeslotStore(),
		Appointments:  apptStore,
		Notes:         noteStore,
		Prescriptions: memory.NewPrescriptionStore(),
		Clock:         platform.NewFakeClock(testNow),
		IDGen:         platform.NewFakeIDGen("nt-"),
		Events:        pub,
	})
	return svc, apptStore, noteStore, pub
}

func TestAddNote_Success(t *testing.T) {
	svc, apptStore, noteStore, pub := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1", Status: domain.AppointmentScheduled})

	note, err := svc.AddNote(doctorCtx("hosp-A", "doc-1"), AddNoteInput{AppointmentID: "appt-1", Text: "Patient reports headache."})
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if note.Source != domain.NoteSourceManual || note.Status != domain.NoteComplete || note.AppointmentID != "appt-1" {
		t.Errorf("note = %+v, want manual/complete/appt-1", note)
	}

	// Persisted under the appointment.
	stored, _ := noteStore.ListByAppointment(context.Background(), "hosp-A", "appt-1")
	if len(stored) != 1 || stored[0].Text != "Patient reports headache." {
		t.Errorf("stored notes = %+v, want one with the text", stored)
	}

	// note_added event carries the brief's fields.
	evs := pub.byType(domain.EventNoteAdded)
	if len(evs) != 1 {
		t.Fatalf("note_added events = %d, want 1", len(evs))
	}
	d := evs[0].Payload
	if d["noteId"] != note.ID || d["appointmentId"] != "appt-1" || d["patientId"] != "pat-1" || d["noteText"] != "Patient reports headache." {
		t.Errorf("event payload = %+v, missing expected fields", d)
	}
}

func TestAddNote_EmptyTextValidation(t *testing.T) {
	svc, apptStore, _, _ := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})
	if _, err := svc.AddNote(doctorCtx("hosp-A", "doc-1"), AddNoteInput{AppointmentID: "appt-1", Text: "   "}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

func TestAddNote_UnknownAppointment(t *testing.T) {
	svc, _, _, _ := noteFixtures()
	if _, err := svc.AddNote(doctorCtx("hosp-A", "doc-1"), AddNoteInput{AppointmentID: "nope", Text: "hi"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddNote_NotOwningDoctorForbidden(t *testing.T) {
	svc, apptStore, _, _ := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})
	// A different doctor cannot annotate doc-1's appointment.
	if _, err := svc.AddNote(doctorCtx("hosp-A", "doc-2"), AddNoteInput{AppointmentID: "appt-1", Text: "hi"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestAddNote_MultiTenantIsolation(t *testing.T) {
	svc, apptStore, noteStore, _ := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	// A doctor with the same id in another hospital cannot see the appointment.
	if _, err := svc.AddNote(doctorCtx("hosp-B", "doc-1"), AddNoteInput{AppointmentID: "appt-1", Text: "hi"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}

	// The valid note lands only in hosp-A.
	if _, err := svc.AddNote(doctorCtx("hosp-A", "doc-1"), AddNoteInput{AppointmentID: "appt-1", Text: "ok"}); err != nil {
		t.Fatalf("hosp-A add: %v", err)
	}
	if got, _ := noteStore.ListByAppointment(context.Background(), "hosp-B", "appt-1"); len(got) != 0 {
		t.Errorf("hosp-B notes = %d, want 0", len(got))
	}
}

func TestAddNote_NoActorForbidden(t *testing.T) {
	svc, _, _, _ := noteFixtures()
	if _, err := svc.AddNote(context.Background(), AddNoteInput{AppointmentID: "appt-1", Text: "hi"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}
