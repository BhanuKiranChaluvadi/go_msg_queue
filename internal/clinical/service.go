// Package clinical implements Feature 4 (Historical Overview): patient diagnoses
// and a point-in-time overview of a patient's clinical picture. It reads across
// the appointment/prescription/diagnosis stores and owns the diagnosis lifecycle.
package clinical

import (
	"context"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store"
)

// EventPublisher is the narrow event-publishing dependency this package needs.
type EventPublisher interface {
	Publish(ctx context.Context, e domain.Event) domain.Event
}

// Deps groups the Service's collaborators.
type Deps struct {
	Diagnoses     store.DiagnosisRepo
	Appointments  store.AppointmentRepo
	Notes         store.NoteRepo
	Prescriptions store.PrescriptionRepo
	Clock         platform.Clock
	IDGen         platform.IDGen
	Events        EventPublisher
}

// Service manages diagnoses and assembles point-in-time patient overviews.
type Service struct {
	diagnoses     store.DiagnosisRepo
	appointments  store.AppointmentRepo
	notes         store.NoteRepo
	prescriptions store.PrescriptionRepo
	clock         platform.Clock
	ids           platform.IDGen
	events        EventPublisher
}

// NewService constructs a Service.
func NewService(d Deps) *Service {
	return &Service{
		diagnoses:     d.Diagnoses,
		appointments:  d.Appointments,
		notes:         d.Notes,
		prescriptions: d.Prescriptions,
		clock:         d.Clock,
		ids:           d.IDGen,
		events:        d.Events,
	}
}
