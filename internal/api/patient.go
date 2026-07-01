package api

import (
	"fmt"
	"net/http"
	"time"

	"medconnect/internal/domain"
)

// handlePatientOverview returns a patient's point-in-time clinical overview.
// GET /v1/patients/{id}/overview?at=<RFC3339>  (defaults to now)
func (s *Server) handlePatientOverview(w http.ResponseWriter, r *http.Request) {
	var at time.Time
	if v := r.URL.Query().Get("at"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, fmt.Errorf("%w: invalid 'at' timestamp, expected RFC3339", domain.ErrValidation))
			return
		}
		at = parsed
	}
	ov, err := s.Clinical.Overview(r.Context(), r.PathValue("id"), at)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}
