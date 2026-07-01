package api

import (
	"net/http"

	"medconnect/internal/domain"
	"medconnect/internal/webhooks"
)

// handleRegisterWebhook lets a patient subscribe a URL to event notifications.
// POST /v1/webhooks  {"url": "...", "eventTypes": ["note_added", ...]}
func (s *Server) handleRegisterWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL        string             `json:"url"`
		EventTypes []domain.EventType `json:"eventTypes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	wh, err := s.Webhooks.Register(r.Context(),
		webhooks.RegisterInput{URL: body.URL, EventTypes: body.EventTypes})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wh)
}

// handleUnregisterWebhook removes a patient's subscription.
// DELETE /v1/webhooks/{id}
func (s *Server) handleUnregisterWebhook(w http.ResponseWriter, r *http.Request) {
	if err := s.Webhooks.Unregister(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
