package appointments

import (
	"context"
	"fmt"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// BookInput is a patient's request to book a specific timeslot. DoctorID must
// match the timeslot's owner; it is required by the API for clarity and is
// validated defensively.
type BookInput struct {
	DoctorID   string
	TimeslotID string
}

// Book reserves an open timeslot for the calling patient. It enforces two
// invariants atomically under a single lock so concurrent attempts cannot both
// win:
//
//   - a timeslot may be booked at most once (no double-booking);
//   - a patient may hold at most one appointment with a given doctor.
//
// On success it flips the slot to booked, creates the appointment, and emits an
// appointment_booked event.
func (s *Service) Book(ctx context.Context, in BookInput) (domain.Appointment, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Appointment{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	tenant := actor.TenantID

	s.mu.Lock()
	defer s.mu.Unlock()

	ts, err := s.timeslots.Get(ctx, tenant, in.TimeslotID)
	if err != nil {
		return domain.Appointment{}, err // ErrNotFound for an unknown slot
	}
	if in.DoctorID != "" && ts.DoctorID != in.DoctorID {
		return domain.Appointment{}, fmt.Errorf("%w: timeslot does not belong to doctor", domain.ErrValidation)
	}
	if ts.Status != domain.TimeslotOpen {
		return domain.Appointment{}, fmt.Errorf("%w: timeslot already booked", domain.ErrConflict)
	}

	exists, err := s.appointments.ExistsForPatientDoctor(ctx, tenant, actor.ID, ts.DoctorID)
	if err != nil {
		return domain.Appointment{}, err
	}
	if exists {
		return domain.Appointment{}, fmt.Errorf("%w: patient already has an appointment with this doctor", domain.ErrConflict)
	}

	appt := domain.Appointment{
		ID:         s.ids.NewID(),
		TenantID:   tenant,
		DoctorID:   ts.DoctorID,
		PatientID:  actor.ID,
		TimeslotID: ts.ID,
		Start:      ts.Start,
		End:        ts.End,
		Status:     domain.AppointmentScheduled,
		CreatedAt:  s.clock.Now(),
	}

	ts.Status = domain.TimeslotBooked
	if err := s.timeslots.Update(ctx, ts); err != nil {
		return domain.Appointment{}, err
	}
	if err := s.appointments.Create(ctx, appt); err != nil {
		return domain.Appointment{}, err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  tenant,
		Type:      domain.EventAppointmentBooked,
		ActorID:   actor.ID,
		EntityRef: appt.ID,
		Payload: map[string]any{
			"appointmentId": appt.ID,
			"doctorId":      appt.DoctorID,
			"patientId":     appt.PatientID,
			"timeslotId":    appt.TimeslotID,
		},
	})
	return appt, nil
}
