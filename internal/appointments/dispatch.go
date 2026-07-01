package appointments

import (
	"context"
	"fmt"
	"sort"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// ListActivePrescriptions returns the tenant's prescriptions that are still
// active — not yet dispatched and not expired — ordered by issue time. It is the
// pharmacist's worklist. Authorization to the pharmacist role is enforced at the
// HTTP boundary; here it is tenant-scoped.
func (s *Service) ListActivePrescriptions(ctx context.Context) ([]domain.Prescription, error) {
	tenant, ok := tenancy.TenantFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: no tenant", domain.ErrForbidden)
	}

	all, err := s.prescriptions.ListByTenant(ctx, tenant)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	active := make([]domain.Prescription, 0, len(all))
	for _, p := range all {
		if p.IsActive(now) {
			active = append(active, p)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].IssuedAt.Equal(active[j].IssuedAt) {
			return active[i].ID < active[j].ID
		}
		return active[i].IssuedAt.Before(active[j].IssuedAt)
	})
	return active, nil
}

// DispatchPrescription marks an active prescription as dispatched, exactly once.
// The check-and-set runs under the service lock so two concurrent dispatches
// cannot both succeed; the loser sees a conflict. A prescription that is already
// dispatched or has expired cannot be dispatched.
func (s *Service) DispatchPrescription(ctx context.Context, prescriptionID string) (domain.Prescription, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Prescription{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rx, err := s.prescriptions.Get(ctx, actor.TenantID, prescriptionID)
	if err != nil {
		return domain.Prescription{}, err // ErrNotFound (incl. cross-tenant)
	}
	if !rx.IsActive(s.clock.Now()) {
		return domain.Prescription{}, fmt.Errorf("%w: prescription is not active (already dispatched or expired)", domain.ErrConflict)
	}

	rx.Status = domain.PrescriptionDispatched
	if err := s.prescriptions.Update(ctx, rx); err != nil {
		return domain.Prescription{}, err
	}

	s.events.Publish(ctx, domain.Event{
		TenantID:  actor.TenantID,
		Type:      domain.EventPrescriptionDispatched,
		ActorID:   actor.ID,
		EntityRef: rx.ID,
		Payload: map[string]any{
			"prescriptionId": rx.ID,
			"patientId":      rx.PatientID,
			"pharmacistId":   actor.ID,
		},
	})
	return rx, nil
}
