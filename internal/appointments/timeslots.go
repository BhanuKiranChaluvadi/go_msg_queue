package appointments

import (
	"context"
	"fmt"
	"sort"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// RegisterTimeslotInput is the doctor-supplied data for a new availability slot.
type RegisterTimeslotInput struct {
	Start time.Time
	End   time.Time
}

// RegisterTimeslot creates an open availability slot for the calling doctor. The
// doctor is taken from the request actor, so a doctor can only register slots for
// themselves. Slots must be non-empty, in the future, and must not overlap the
// doctor's existing slots.
func (s *Service) RegisterTimeslot(ctx context.Context, in RegisterTimeslotInput) (domain.Timeslot, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Timeslot{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	if !in.End.After(in.Start) {
		return domain.Timeslot{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}
	if in.Start.Before(s.clock.Now()) {
		return domain.Timeslot{}, fmt.Errorf("%w: start must be in the future", domain.ErrValidation)
	}

	existing, err := s.timeslots.ListByDoctor(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return domain.Timeslot{}, err
	}
	for _, e := range existing {
		if overlaps(in.Start, in.End, e.Start, e.End) {
			return domain.Timeslot{}, fmt.Errorf("%w: overlaps an existing timeslot", domain.ErrConflict)
		}
	}

	ts := domain.Timeslot{
		ID:       s.ids.NewID(),
		TenantID: actor.TenantID,
		DoctorID: actor.ID,
		Start:    in.Start,
		End:      in.End,
		Status:   domain.TimeslotOpen,
	}
	if err := s.timeslots.Create(ctx, ts); err != nil {
		return domain.Timeslot{}, err
	}
	return ts, nil
}

// ListOpenTimeslots returns a doctor's bookable slots — open and not yet ended —
// for the caller's tenant, ordered by start time. It is the patient-facing view
// of a doctor's availability.
func (s *Service) ListOpenTimeslots(ctx context.Context, doctorID string) ([]domain.Timeslot, error) {
	tenant, ok := tenancy.TenantFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: no tenant", domain.ErrForbidden)
	}

	all, err := s.timeslots.ListByDoctor(ctx, tenant, doctorID)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	open := make([]domain.Timeslot, 0, len(all))
	for _, t := range all {
		if t.Status == domain.TimeslotOpen && t.End.After(now) {
			open = append(open, t)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].Start.Before(open[j].Start) })
	return open, nil
}

// overlaps reports whether half-open intervals [s1,e1) and [s2,e2) intersect.
// Adjacent intervals (one ending exactly when the other starts) do not overlap.
func overlaps(s1, e1, s2, e2 time.Time) bool {
	return s1.Before(e2) && s2.Before(e1)
}
