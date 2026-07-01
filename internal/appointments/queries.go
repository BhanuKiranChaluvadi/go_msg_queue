package appointments

import (
	"context"
	"fmt"
	"sort"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// NextAppointments returns the calling doctor's upcoming appointments — those
// scheduled now or later, excluding cancelled ones — ordered soonest first.
func (s *Service) NextAppointments(ctx context.Context) ([]domain.Appointment, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}

	all, err := s.appointments.NextForDoctor(ctx, actor.TenantID, actor.ID, s.clock.Now())
	if err != nil {
		return nil, err
	}

	upcoming := make([]domain.Appointment, 0, len(all))
	for _, a := range all {
		if a.Status != domain.AppointmentCancelled {
			upcoming = append(upcoming, a)
		}
	}
	sort.Slice(upcoming, func(i, j int) bool { return upcoming[i].Start.Before(upcoming[j].Start) })
	return upcoming, nil
}
