package appointments

import (
	"context"
	"errors"
	"testing"

	"medconnect/internal/domain"
)

func TestStoreDictatedNote_Complete(t *testing.T) {
	svc, apptStore, noteStore, pub := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	note, err := svc.StoreDictatedNote(doctorCtx("hosp-A", "doc-1"), "appt-1", "full transcript", true, nil)
	if err != nil {
		t.Fatalf("StoreDictatedNote: %v", err)
	}
	if note.Source != domain.NoteSourceDictation || note.Status != domain.NoteComplete {
		t.Errorf("note = %+v, want dictation/complete", note)
	}
	stored, _ := noteStore.ListByAppointment(context.Background(), "hosp-A", "appt-1")
	if len(stored) != 1 {
		t.Errorf("stored = %d, want 1", len(stored))
	}
	if got := pub.byType(domain.EventNoteAdded); len(got) != 1 {
		t.Errorf("note_added events = %d, want 1", len(got))
	}
	if got := pub.byType(domain.EventNoteIncomplete); len(got) != 0 {
		t.Errorf("note_incomplete events = %d, want 0", len(got))
	}
}

func TestStoreDictatedNote_Incomplete(t *testing.T) {
	svc, apptStore, noteStore, pub := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	note, err := svc.StoreDictatedNote(doctorCtx("hosp-A", "doc-1"), "appt-1", "partial", false, []int{2})
	if err != nil {
		t.Fatalf("StoreDictatedNote: %v", err)
	}
	if note.Status != domain.NoteIncomplete || len(note.Missing) != 1 || note.Missing[0] != 2 {
		t.Errorf("note = %+v, want incomplete with missing [2]", note)
	}
	stored, _ := noteStore.ListByAppointment(context.Background(), "hosp-A", "appt-1")
	if len(stored) != 1 || stored[0].Status != domain.NoteIncomplete {
		t.Errorf("stored = %+v, want one incomplete note", stored)
	}
	// Emits note_incomplete, NOT note_added.
	if got := pub.byType(domain.EventNoteAdded); len(got) != 0 {
		t.Errorf("note_added events = %d, want 0 for an incomplete note", len(got))
	}
	inc := pub.byType(domain.EventNoteIncomplete)
	if len(inc) != 1 {
		t.Fatalf("note_incomplete events = %d, want 1", len(inc))
	}
	if inc[0].Payload["noteId"] != note.ID {
		t.Errorf("event payload = %+v", inc[0].Payload)
	}
}

func TestStoreDictatedNote_OwnershipAndTenant(t *testing.T) {
	svc, apptStore, _, _ := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	if _, err := svc.StoreDictatedNote(doctorCtx("hosp-A", "doc-2"), "appt-1", "x", true, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-owner err = %v, want ErrForbidden", err)
	}
	if _, err := svc.StoreDictatedNote(doctorCtx("hosp-B", "doc-1"), "appt-1", "x", true, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
	if _, err := svc.StoreDictatedNote(context.Background(), "appt-1", "x", true, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("no-actor err = %v, want ErrForbidden", err)
	}
}

func TestDoctorAppointment(t *testing.T) {
	svc, apptStore, _, _ := noteFixtures()
	seedAppt(t, apptStore, domain.Appointment{ID: "appt-1", TenantID: "hosp-A", DoctorID: "doc-1", PatientID: "pat-1"})

	if _, err := svc.DoctorAppointment(doctorCtx("hosp-A", "doc-1"), "appt-1"); err != nil {
		t.Errorf("owner: %v", err)
	}
	if _, err := svc.DoctorAppointment(doctorCtx("hosp-A", "doc-2"), "appt-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-owner err = %v, want ErrForbidden", err)
	}
	if _, err := svc.DoctorAppointment(doctorCtx("hosp-A", "doc-1"), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown err = %v, want ErrNotFound", err)
	}
}
