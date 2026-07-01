package clinical

import (
	"context"
	"fmt"
	"strings"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// Diagnose records a new disease for a patient. Any doctor in the tenant may
// diagnose; the diagnosis is timestamped and an event is emitted.
func (s *Service) Diagnose(ctx context.Context, patientID, disease string) (domain.Diagnosis, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Diagnosis{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	if strings.TrimSpace(patientID) == "" {
		return domain.Diagnosis{}, fmt.Errorf("%w: patient id is required", domain.ErrValidation)
	}
	if strings.TrimSpace(disease) == "" {
		return domain.Diagnosis{}, fmt.Errorf("%w: disease is required", domain.ErrValidation)
	}

	d := domain.Diagnosis{
		ID:          s.ids.NewID(),
		TenantID:    actor.TenantID,
		PatientID:   patientID,
		Disease:     disease,
		DiagnosedAt: s.clock.Now(),
	}
	if err := s.diagnoses.Create(ctx, d); err != nil {
		return domain.Diagnosis{}, err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  actor.TenantID,
		Type:      domain.EventDiagnosisAdded,
		ActorID:   actor.ID,
		EntityRef: d.ID,
		Payload: map[string]any{
			"diagnosisId": d.ID,
			"disease":     d.Disease,
			"patientId":   d.PatientID,
		},
	})
	return d, nil
}

// DismissDiagnosis soft-closes a diagnosis by setting DismissedAt (history is
// preserved). The diagnosis must belong to the given patient and not already be
// dismissed.
func (s *Service) DismissDiagnosis(ctx context.Context, patientID, diagnosisID string) error {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	d, err := s.diagnoses.Get(ctx, actor.TenantID, diagnosisID)
	if err != nil {
		return err // ErrNotFound (incl. cross-tenant)
	}
	if d.PatientID != patientID {
		return fmt.Errorf("%w: diagnosis does not belong to this patient", domain.ErrNotFound)
	}
	if d.DismissedAt != nil {
		return fmt.Errorf("%w: diagnosis already dismissed", domain.ErrConflict)
	}

	now := s.clock.Now()
	d.DismissedAt = &now
	if err := s.diagnoses.Update(ctx, d); err != nil {
		return err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  actor.TenantID,
		Type:      domain.EventDiagnosisDismissed,
		ActorID:   actor.ID,
		EntityRef: d.ID,
		Payload: map[string]any{
			"diagnosisId": d.ID,
			"patientId":   d.PatientID,
		},
	})
	return nil
}
