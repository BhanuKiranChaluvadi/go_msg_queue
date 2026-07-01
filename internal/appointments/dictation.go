package appointments

import (
	"context"
	"fmt"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// DoctorAppointment returns the appointment if the calling doctor owns it, used
// to validate a request (e.g. starting transcription) before doing work.
func (s *Service) DoctorAppointment(ctx context.Context, appointmentID string) (domain.Appointment, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Appointment{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	appt, err := s.appointments.Get(ctx, actor.TenantID, appointmentID)
	if err != nil {
		return domain.Appointment{}, err
	}
	if appt.DoctorID != actor.ID {
		return domain.Appointment{}, fmt.Errorf("%w: not the appointment's doctor", domain.ErrForbidden)
	}
	return appt, nil
}

// StoreDictatedNote persists a note assembled from a transcription stream and
// emits the matching event: note_added when the transcript is complete, or
// note_incomplete (never a completed note) when chunks were missing. It is called
// by the background transcription worker with the doctor's identity in ctx.
func (s *Service) StoreDictatedNote(ctx context.Context, appointmentID, text string, complete bool, missing []int) (domain.Note, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Note{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	appt, err := s.appointments.Get(ctx, actor.TenantID, appointmentID)
	if err != nil {
		return domain.Note{}, err
	}
	if appt.DoctorID != actor.ID {
		return domain.Note{}, fmt.Errorf("%w: not the appointment's doctor", domain.ErrForbidden)
	}

	status := domain.NoteComplete
	if !complete {
		status = domain.NoteIncomplete
	}
	note := domain.Note{
		ID:            s.ids.NewID(),
		TenantID:      actor.TenantID,
		AppointmentID: appt.ID,
		Text:          text,
		Source:        domain.NoteSourceDictation,
		Status:        status,
		Missing:       missing,
		CreatedAt:     s.clock.Now(),
	}
	if err := s.notes.Create(ctx, note); err != nil {
		return domain.Note{}, err
	}

	if complete {
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
	} else {
		s.events.Publish(ctx, domain.Event{
			TenantID:  actor.TenantID,
			Type:      domain.EventNoteIncomplete,
			ActorID:   actor.ID,
			EntityRef: appt.ID,
			Payload: map[string]any{
				"noteId":        note.ID,
				"missing":       missing,
				"appointmentId": appt.ID,
				"patientId":     appt.PatientID,
			},
		})
	}
	return note, nil
}
