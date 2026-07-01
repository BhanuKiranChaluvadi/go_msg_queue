// Package appointments holds the appointment-management domain services:
// timeslots, bookings, notes, and prescriptions. Services depend only on the
// store interfaces plus a Clock and IDGen, so they are pure business logic that
// can be tested in memory and later backed by any datastore.
package appointments

import (
	"medconnect/internal/platform"
	"medconnect/internal/store"
)

// Service implements appointment-management use cases. Dependencies are injected
// as interfaces (dependency inversion) so the concrete stores, clock, and id
// generator can vary without touching business logic.
type Service struct {
	timeslots store.TimeslotRepo
	clock     platform.Clock
	ids       platform.IDGen
}

// NewService constructs a Service from its collaborators.
func NewService(timeslots store.TimeslotRepo, clock platform.Clock, ids platform.IDGen) *Service {
	return &Service{
		timeslots: timeslots,
		clock:     clock,
		ids:       ids,
	}
}
