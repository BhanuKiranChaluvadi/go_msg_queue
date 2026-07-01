package appointments

import (
	"context"
	"fmt"
	"sort"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// Overview is the full picture of one appointment: the appointment itself plus
// its notes and prescriptions.
type Overview struct {
	Appointment   domain.Appointment    `json:"appointment"`
	Notes         []domain.Note         `json:"notes"`
	Prescriptions []domain.Prescription `json:"prescriptions"`
}

// AppointmentOverview returns an appointment with its notes and prescriptions.
// Only the two participants — the booking patient and the appointment's doctor —
// may view it; anyone else is forbidden. The appointment is tenant-scoped, so a
// caller from another tenant sees a not-found.
func (s *Service) AppointmentOverview(ctx context.Context, appointmentID string) (Overview, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return Overview{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}

	appt, err := s.appointments.Get(ctx, actor.TenantID, appointmentID)
	if err != nil {
		return Overview{}, err // ErrNotFound (incl. cross-tenant)
	}
	if actor.ID != appt.DoctorID && actor.ID != appt.PatientID {
		return Overview{}, fmt.Errorf("%w: not a participant in this appointment", domain.ErrForbidden)
	}

	notes, err := s.notes.ListByAppointment(ctx, actor.TenantID, appt.ID)
	if err != nil {
		return Overview{}, err
	}
	prescriptions, err := s.prescriptions.ListByAppointment(ctx, actor.TenantID, appt.ID)
	if err != nil {
		return Overview{}, err
	}

	sort.Slice(notes, func(i, j int) bool {
		if notes[i].CreatedAt.Equal(notes[j].CreatedAt) {
			return notes[i].ID < notes[j].ID
		}
		return notes[i].CreatedAt.Before(notes[j].CreatedAt)
	})
	sort.Slice(prescriptions, func(i, j int) bool {
		if prescriptions[i].IssuedAt.Equal(prescriptions[j].IssuedAt) {
			return prescriptions[i].ID < prescriptions[j].ID
		}
		return prescriptions[i].IssuedAt.Before(prescriptions[j].IssuedAt)
	})

	return Overview{Appointment: appt, Notes: notes, Prescriptions: prescriptions}, nil
}
