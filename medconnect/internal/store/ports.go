// Package store defines the persistence boundary for medconnect. Services depend
// on these interfaces, never on a concrete store, so an in-memory adapter (this
// package's memory subpackage) can be swapped for a SQL adapter without changing
// business logic. Every method is tenant-scoped: reads take an explicit tenant
// and writes carry it on the entity.
package store

import (
	"context"
	"time"

	"medconnect/internal/domain"
)

// TimeslotRepo stores doctor availability slots.
type TimeslotRepo interface {
	Create(ctx context.Context, ts domain.Timeslot) error
	Get(ctx context.Context, tenant, id string) (domain.Timeslot, error)
	Update(ctx context.Context, ts domain.Timeslot) error
	ListByDoctor(ctx context.Context, tenant, doctorID string) ([]domain.Timeslot, error)
}

// AppointmentRepo stores appointments and answers the booking invariant query.
type AppointmentRepo interface {
	Create(ctx context.Context, a domain.Appointment) error
	Get(ctx context.Context, tenant, id string) (domain.Appointment, error)
	Update(ctx context.Context, a domain.Appointment) error
	// ExistsForPatientDoctor reports whether a non-cancelled appointment already
	// exists for the patient-doctor pair (enforces "at most one").
	ExistsForPatientDoctor(ctx context.Context, tenant, patientID, doctorID string) (bool, error)
	// NextForDoctor returns the doctor's appointments starting at or after from.
	NextForDoctor(ctx context.Context, tenant, doctorID string, from time.Time) ([]domain.Appointment, error)
}

// NoteRepo stores appointment notes.
type NoteRepo interface {
	Create(ctx context.Context, n domain.Note) error
	Get(ctx context.Context, tenant, id string) (domain.Note, error)
	ListByAppointment(ctx context.Context, tenant, appointmentID string) ([]domain.Note, error)
}

// PrescriptionRepo stores prescriptions.
type PrescriptionRepo interface {
	Create(ctx context.Context, p domain.Prescription) error
	Get(ctx context.Context, tenant, id string) (domain.Prescription, error)
	Update(ctx context.Context, p domain.Prescription) error
	ListByAppointment(ctx context.Context, tenant, appointmentID string) ([]domain.Prescription, error)
	ListByTenant(ctx context.Context, tenant string) ([]domain.Prescription, error)
}

// DiagnosisRepo stores patient diagnoses.
type DiagnosisRepo interface {
	Create(ctx context.Context, d domain.Diagnosis) error
	Get(ctx context.Context, tenant, id string) (domain.Diagnosis, error)
	Update(ctx context.Context, d domain.Diagnosis) error
	ListByPatient(ctx context.Context, tenant, patientID string) ([]domain.Diagnosis, error)
}

// WebhookRepo stores patient webhook subscriptions.
type WebhookRepo interface {
	Create(ctx context.Context, w domain.Webhook) error
	Get(ctx context.Context, tenant, id string) (domain.Webhook, error)
	Delete(ctx context.Context, tenant, id string) error
	ListByTenant(ctx context.Context, tenant string) ([]domain.Webhook, error)
}
