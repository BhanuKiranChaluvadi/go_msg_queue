package api

import (
	"fmt"
	"net/http"

	"medconnect/internal/domain"
)

// handleListActivePrescriptions returns the tenant's active prescriptions for a
// pharmacist to dispatch. GET /v1/prescriptions?status=active
func (s *Server) handleListActivePrescriptions(w http.ResponseWriter, r *http.Request) {
	if status := r.URL.Query().Get("status"); status != "" && status != "active" {
		writeError(w, fmt.Errorf("%w: unsupported status filter %q (only \"active\" is supported)", domain.ErrValidation, status))
		return
	}
	list, err := s.Appointments.ListActivePrescriptions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleDispatchPrescription dispatches an active prescription, exactly once.
// POST /v1/prescriptions/{id}/dispatch
func (s *Server) handleDispatchPrescription(w http.ResponseWriter, r *http.Request) {
	rx, err := s.Appointments.DispatchPrescription(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rx)
}
