package appointments

import (
	"context"
	"fmt"
	"strings"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// IssuePrescriptionInput is a doctor's follow-up prescription for an appointment.
type IssuePrescriptionInput struct {
	AppointmentID string
	Medication    string
	ExpiresAt     time.Time
}

// IssuePrescription creates an active prescription on one of the calling doctor's
// appointments and emits a prescription_added event. A doctor may only prescribe
// on their own appointments; the expiry must be in the future.
func (s *Service) IssuePrescription(ctx context.Context, in IssuePrescriptionInput) (domain.Prescription, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Prescription{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	if strings.TrimSpace(in.Medication) == "" {
		return domain.Prescription{}, fmt.Errorf("%w: medication is required", domain.ErrValidation)
	}
	now := s.clock.Now()
	if !in.ExpiresAt.After(now) {
		return domain.Prescription{}, fmt.Errorf("%w: expiry must be in the future", domain.ErrValidation)
	}

	appt, err := s.appointments.Get(ctx, actor.TenantID, in.AppointmentID)
	if err != nil {
		return domain.Prescription{}, err // ErrNotFound for an unknown appointment
	}
	if appt.DoctorID != actor.ID {
		return domain.Prescription{}, fmt.Errorf("%w: not the appointment's doctor", domain.ErrForbidden)
	}

	rx := domain.Prescription{
		ID:            s.ids.NewID(),
		TenantID:      actor.TenantID,
		AppointmentID: appt.ID,
		PatientID:     appt.PatientID,
		Medication:    in.Medication,
		IssuedAt:      now,
		ExpiresAt:     in.ExpiresAt,
		Status:        domain.PrescriptionActive,
	}
	if err := s.prescriptions.Create(ctx, rx); err != nil {
		return domain.Prescription{}, err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  actor.TenantID,
		Type:      domain.EventPrescriptionAdded,
		ActorID:   actor.ID,
		EntityRef: appt.ID,
		Payload: map[string]any{
			"prescriptionId": rx.ID,
			"medication":     rx.Medication,
			"expiresAt":      rx.ExpiresAt,
			"appointmentId":  appt.ID,
			"patientId":      appt.PatientID,
		},
	})
	return rx, nil
}
