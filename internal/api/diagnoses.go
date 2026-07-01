package api

import "net/http"

// handleDiagnose records a new diagnosis for a patient.
// POST /v1/patients/{id}/diagnoses  {"disease": "..."}
func (s *Server) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disease string `json:"disease"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	d, err := s.Clinical.Diagnose(r.Context(), r.PathValue("id"), body.Disease)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// handleDismissDiagnosis soft-closes a diagnosis.
// DELETE /v1/patients/{id}/diagnoses/{did}
func (s *Server) handleDismissDiagnosis(w http.ResponseWriter, r *http.Request) {
	if err := s.Clinical.DismissDiagnosis(r.Context(), r.PathValue("id"), r.PathValue("did")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
