// Package protocol defines the tiny internal contract that lets worker binaries
// (transcriber, notifier) talk to the hub in split mode, plus a reusable SSE
// client used to consume both the external transcription stream and the hub's
// internal event stream. Keeping this contract small and versioned is what makes
// the optional process split sustainable.
package protocol

// TranscriptionJob instructs a transcriber worker to consume the transcription
// stream for one appointment. Delivered over GET /internal/transcription/jobs.
type TranscriptionJob struct {
	TenantID      string `json:"tenantId"`
	AppointmentID string `json:"appointmentId"`
	StreamURL     string `json:"streamUrl"`
}

// AssembledNote is the result a transcriber posts back to the hub at
// POST /internal/appointments/{id}/notes once a stream has been assembled.
type AssembledNote struct {
	Text    string `json:"text"`
	Status  string `json:"status"`
	Missing []int  `json:"missing,omitempty"`
}
