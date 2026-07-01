// Package appointments holds the appointment-management domain services:
// timeslots, bookings, notes, and prescriptions. Services depend only on the
// store interfaces plus a Clock and IDGen, so they are pure business logic that
// can be tested in memory and later backed by any datastore.
package appointments

import (
	"context"
	"sync"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store"
)

// EventPublisher is the narrow slice of the event pipeline the service needs:
// it publishes a domain event. Depending on this interface (not the concrete
// publisher) keeps the service testable and decoupled.
type EventPublisher interface {
	Publish(ctx context.Context, e domain.Event) domain.Event
}

// Deps groups the Service's collaborators. Using a struct keeps the constructor
// stable as more repositories are added in later slices.
type Deps struct {
	Timeslots     store.TimeslotRepo
	Appointments  store.AppointmentRepo
	Notes         store.NoteRepo
	Prescriptions store.PrescriptionRepo
	Clock         platform.Clock
	IDGen         platform.IDGen
	Events        EventPublisher
}

// Service implements appointment-management use cases. Dependencies are injected
// as interfaces (dependency inversion) so the concrete stores, clock, id
// generator, and event publisher can vary without touching business logic.
type Service struct {
	timeslots     store.TimeslotRepo
	appointments  store.AppointmentRepo
	notes         store.NoteRepo
	prescriptions store.PrescriptionRepo
	clock         platform.Clock
	ids           platform.IDGen
	events        EventPublisher

	// mu guards multi-step invariants (booking, dispatch) so a check-and-set is
	// atomic across the separate stores. It is the in-memory stand-in for a
	// database transaction.
	mu sync.Mutex
}

// NewService constructs a Service from its collaborators.
func NewService(d Deps) *Service {
	return &Service{
		timeslots:     d.Timeslots,
		appointments:  d.Appointments,
		notes:         d.Notes,
		prescriptions: d.Prescriptions,
		clock:         d.Clock,
		ids:           d.IDGen,
		events:        d.Events,
	}
}
