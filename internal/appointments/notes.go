package appointments

import (
	"context"
	"fmt"
	"strings"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// AddNoteInput is a doctor's manual note for an appointment.
type AddNoteInput struct {
	AppointmentID string
	Text          string
}

// AddNote attaches a manual, complete note to one of the calling doctor's
// appointments and emits a note_added event. A doctor may only annotate their
// own appointments.
func (s *Service) AddNote(ctx context.Context, in AddNoteInput) (domain.Note, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Note{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	if strings.TrimSpace(in.Text) == "" {
		return domain.Note{}, fmt.Errorf("%w: note text is required", domain.ErrValidation)
	}

	appt, err := s.appointments.Get(ctx, actor.TenantID, in.AppointmentID)
	if err != nil {
		return domain.Note{}, err // ErrNotFound for an unknown appointment
	}
	if appt.DoctorID != actor.ID {
		return domain.Note{}, fmt.Errorf("%w: not the appointment's doctor", domain.ErrForbidden)
	}

	note := domain.Note{
		ID:            s.ids.NewID(),
		TenantID:      actor.TenantID,
		AppointmentID: appt.ID,
		Text:          in.Text,
		Source:        domain.NoteSourceManual,
		Status:        domain.NoteComplete,
		CreatedAt:     s.clock.Now(),
	}
	if err := s.notes.Create(ctx, note); err != nil {
		return domain.Note{}, err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  actor.TenantID,
		Type:      domain.EventNoteAdded,
		ActorID:   actor.ID,
		EntityRef: appt.ID,
		Payload: map[string]any{
			"noteId":        note.ID,
			"noteText":      note.Text,
			"appointmentId": appt.ID,
			"patientId":     appt.PatientID,
		},
	})
	return note, nil
}
