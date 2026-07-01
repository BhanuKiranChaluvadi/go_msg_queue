// Package domain holds the core entities, value types, and invariants of the
// medconnect service. Types here are pure: they carry no I/O and depend only on
// the standard library, so they are cheap to construct and test.
package domain

import (
	"errors"
	"time"
)

// Sentinel errors expose failure categories that outer layers (e.g. the HTTP
// API) map to status codes without depending on concrete error values.
var (
	// ErrNotFound indicates a requested entity does not exist in the tenant.
	ErrNotFound = errors.New("not found")
	// ErrConflict indicates an invariant or state-transition conflict.
	ErrConflict = errors.New("conflict")
	// ErrValidation indicates invalid input at a system boundary.
	ErrValidation = errors.New("validation failed")
	// ErrForbidden indicates the actor lacks permission for the operation.
	ErrForbidden = errors.New("forbidden")
)

// Tenant is a hospital organization and the root of all data isolation.
type Tenant struct {
	ID     string `json:"id"`
	Region string `json:"region"`
}

// User is a person acting within a tenant, authorized by Role.
type User struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Role     Role   `json:"role"`
	Name     string `json:"name"`
}

// Timeslot is a doctor's advertised availability window.
type Timeslot struct {
	ID       string         `json:"id"`
	TenantID string         `json:"tenantId"`
	DoctorID string         `json:"doctorId"`
	Start    time.Time      `json:"start"`
	End      time.Time      `json:"end"`
	Status   TimeslotStatus `json:"status"`
}

// Appointment is a patient's booking of a doctor's timeslot. At most one
// appointment may exist per patient-doctor pair. Start/End are copied from the
// booked timeslot so an appointment carries its own scheduled time.
type Appointment struct {
	ID         string            `json:"id"`
	TenantID   string            `json:"tenantId"`
	DoctorID   string            `json:"doctorId"`
	PatientID  string            `json:"patientId"`
	TimeslotID string            `json:"timeslotId"`
	Start      time.Time         `json:"start"`
	End        time.Time         `json:"end"`
	Status     AppointmentStatus `json:"status"`
	CreatedAt  time.Time         `json:"createdAt"`
}

// Note is clinical text attached to an appointment, typed manually or assembled
// from a transcription stream. Missing lists any absent chunk sequence numbers
// when Status is NoteIncomplete.
type Note struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenantId"`
	AppointmentID string     `json:"appointmentId"`
	Text          string     `json:"text"`
	Source        NoteSource `json:"source"`
	Status        NoteStatus `json:"status"`
	Missing       []int      `json:"missing,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// Prescription is a medication order issued on an appointment. It is active
// until it is dispatched or it expires.
type Prescription struct {
	ID            string             `json:"id"`
	TenantID      string             `json:"tenantId"`
	AppointmentID string             `json:"appointmentId"`
	PatientID     string             `json:"patientId"`
	Medication    string             `json:"medication"`
	IssuedAt      time.Time          `json:"issuedAt"`
	ExpiresAt     time.Time          `json:"expiresAt"`
	Status        PrescriptionStatus `json:"status"`
}

// IsActive reports whether the prescription can still be dispatched at now: it
// must not already be dispatched and must not have reached its expiry.
func (p Prescription) IsActive(now time.Time) bool {
	return p.Status == PrescriptionActive && now.Before(p.ExpiresAt)
}

// EffectiveStatus resolves the prescription's status at now, deriving Expired
// from ExpiresAt rather than requiring a stored transition.
func (p Prescription) EffectiveStatus(now time.Time) PrescriptionStatus {
	if p.Status == PrescriptionDispatched {
		return PrescriptionDispatched
	}
	if !now.Before(p.ExpiresAt) {
		return PrescriptionExpired
	}
	return PrescriptionActive
}

// Diagnosis records a disease attributed to a patient. Dismissal is a soft close
// via DismissedAt so the clinical history is preserved.
type Diagnosis struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenantId"`
	PatientID   string     `json:"patientId"`
	Disease     string     `json:"disease"`
	DiagnosedAt time.Time  `json:"diagnosedAt"`
	DismissedAt *time.Time `json:"dismissedAt,omitempty"`
}

// IsActiveAt reports whether the diagnosis was in effect at time t: diagnosed at
// or before t, and not dismissed on or before t. A diagnosis dismissed after t
// is still considered active as of t.
func (d Diagnosis) IsActiveAt(t time.Time) bool {
	if d.DiagnosedAt.After(t) {
		return false
	}
	if d.DismissedAt != nil && !d.DismissedAt.After(t) {
		return false
	}
	return true
}

// Webhook is a patient's subscription to receive live event POSTs.
type Webhook struct {
	ID         string      `json:"id"`
	TenantID   string      `json:"tenantId"`
	PatientID  string      `json:"patientId"`
	URL        string      `json:"url"`
	EventTypes []EventType `json:"eventTypes"`
	Secret     string      `json:"secret"`
}

// Event is an immutable record appended to the tenant event log. ActorID and
// Timestamp make every event self-describing for audit.
type Event struct {
	ID        string         `json:"id"`
	TenantID  string         `json:"tenantId"`
	Type      EventType      `json:"type"`
	ActorID   string         `json:"actorId"`
	EntityRef string         `json:"entityRef"`
	Payload   map[string]any `json:"data"`
	Timestamp time.Time      `json:"timestamp"`
}

// Dispatch records a pharmacist's fulfilment of a prescription.
type Dispatch struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenantId"`
	PrescriptionID string    `json:"prescriptionId"`
	PharmacistID   string    `json:"pharmacistId"`
	DispatchedAt   time.Time `json:"dispatchedAt"`
}
