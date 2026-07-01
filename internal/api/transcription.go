package api

import (
	"fmt"
	"net/http"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// TranscriptionStarter launches a background transcription session for an
// appointment, returning false if one is already running.
type TranscriptionStarter interface {
	Start(tenantID, appointmentID, doctorID string) bool
}

// handleStartTranscription begins consuming the external transcription stream for
// an appointment. It returns 202 immediately; assembly and storage happen in the
// background. Only the appointment's doctor may start it.
// POST /v1/appointments/{id}/transcription
func (s *Server) handleStartTranscription(w http.ResponseWriter, r *http.Request) {
	appt, err := s.Appointments.DoctorAppointment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	actor, _ := tenancy.ActorFrom(r.Context())
	if !s.Transcription.Start(actor.TenantID, appt.ID, actor.ID) {
		writeError(w, fmt.Errorf("%w: transcription already in progress for this appointment", domain.ErrConflict))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "transcription started"})
}
