package transcription

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// HTTPSource opens the transcription stream at BaseURL, passing the appointment
// id as an "appointmentId" query parameter and an optional bearer token. It is
// the production StreamSource; the exact contract of the external transcription
// server is configuration, not code.
type HTTPSource struct {
	BaseURL string
	Token   string
}

// Request builds the SSE request for an appointment's transcription stream.
func (h HTTPSource) Request(ctx context.Context, appointmentID string) (*http.Request, error) {
	if h.BaseURL == "" {
		return nil, errors.New("transcription: no stream base URL configured")
	}
	u := h.BaseURL + "?appointmentId=" + url.QueryEscape(appointmentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	return req, nil
}
