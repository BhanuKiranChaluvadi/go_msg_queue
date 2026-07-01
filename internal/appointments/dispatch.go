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
