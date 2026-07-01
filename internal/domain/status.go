package domain

// Role identifies what a user is permitted to do within a tenant.
type Role string

const (
	// RoleDoctor may manage timeslots, notes, prescriptions, and diagnoses.
	RoleDoctor Role = "doctor"
	// RolePatient may book appointments and register webhooks.
	RolePatient Role = "patient"
	// RolePharmacist may dispatch active prescriptions.
	RolePharmacist Role = "pharmacist"
)

// TimeslotStatus is the lifecycle state of a doctor's availability slot.
type TimeslotStatus string

const (
	// TimeslotOpen means the slot is available to be booked.
	TimeslotOpen TimeslotStatus = "open"
	// TimeslotBooked means the slot has been reserved by an appointment.
	TimeslotBooked TimeslotStatus = "booked"
)

// AppointmentStatus is the lifecycle state of an appointment.
type AppointmentStatus string

const (
	// AppointmentScheduled means the appointment is upcoming.
	AppointmentScheduled AppointmentStatus = "scheduled"
	// AppointmentCancelled means the appointment was called off.
	AppointmentCancelled AppointmentStatus = "cancelled"
	// AppointmentCompleted means the appointment has taken place.
	AppointmentCompleted AppointmentStatus = "completed"
)

// NoteSource records how a note's text was produced.
type NoteSource string

const (
	// NoteSourceManual is a note typed directly by a doctor.
	NoteSourceManual NoteSource = "manual"
	// NoteSourceDictation is a note assembled from a transcription stream.
	NoteSourceDictation NoteSource = "dictation"
)

// NoteStatus captures whether a note is a finished clinical record. A dictated
// note is only Complete when every chunk in sequence was assembled; if a gap
// remained it is Incomplete and must not be presented as a finished note.
type NoteStatus string

const (
	// NoteComplete means the note text is whole.
	NoteComplete NoteStatus = "complete"
	// NoteIncomplete means one or more transcription chunks are missing.
	NoteIncomplete NoteStatus = "incomplete"
)

// PrescriptionStatus is the lifecycle state of a prescription. Expired is a
// derived state evaluated against ExpiresAt (see Prescription.EffectiveStatus).
type PrescriptionStatus string

const (
	// PrescriptionActive means the prescription is usable (not dispatched, not expired).
	PrescriptionActive PrescriptionStatus = "active"
	// PrescriptionDispatched means the prescription has been fulfilled and is now unavailable.
	PrescriptionDispatched PrescriptionStatus = "dispatched"
	// PrescriptionExpired means the prescription passed its expiry (derived, not stored).
	PrescriptionExpired PrescriptionStatus = "expired"
)

// EventType names an immutable event appended to the tenant event log. The log
// unifies live updates, historical overview, audit, and analytics.
type EventType string

const (
	// EventNoteAdded is emitted when a complete note is stored.
	EventNoteAdded EventType = "note_added"
	// EventNoteIncomplete is emitted when a dictated note is stored with gaps.
	EventNoteIncomplete EventType = "note_incomplete"
	// EventPrescriptionAdded is emitted when a prescription is issued.
	EventPrescriptionAdded EventType = "prescription_added"
	// EventAppointmentBooked is emitted when a patient books an appointment.
	EventAppointmentBooked EventType = "appointment_booked"
	// EventPrescriptionDispatched is emitted when a pharmacist dispatches a prescription.
	EventPrescriptionDispatched EventType = "prescription_dispatched"
	// EventDiagnosisAdded is emitted when a doctor diagnoses a disease.
	EventDiagnosisAdded EventType = "diagnosis_added"
	// EventDiagnosisDismissed is emitted when a doctor dismisses a diagnosis.
	EventDiagnosisDismissed EventType = "diagnosis_dismissed"
)
