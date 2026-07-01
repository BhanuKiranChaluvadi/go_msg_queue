package api

import (
	"fmt"
	"net/http"
	"time"

	"medconnect/internal/audit"
	"medconnect/internal/domain"
)

// handleAuditQuery returns audit records (who changed what, and when) for the
// tenant, filtered by patient, event type, and time window.
// GET /v1/audit?patientId=&type=&from=&to=
func (s *Server) handleAuditQuery(w http.ResponseWriter, r *http.Request) {
	q := audit.Query{PatientID: r.URL.Query().Get("patientId")}
	if v := r.URL.Query().Get("type"); v != "" {
		q.Types = []domain.EventType{domain.EventType(v)}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, fmt.Errorf("%w: invalid 'from' timestamp", domain.ErrValidation))
			return
		}
		q.From = parsed
	}
	if v := r.URL.Query().Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, fmt.Errorf("%w: invalid 'to' timestamp", domain.ErrValidation))
			return
		}
		q.To = parsed
	}

	recs, err := s.Audit.Query(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recs)
}
