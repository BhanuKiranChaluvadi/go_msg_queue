package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"medconnect/internal/domain"
)

type webhookReq struct {
	URL        string             `json:"url"`
	EventTypes []domain.EventType `json:"eventTypes"`
}

func TestRegisterWebhook_Handler_AuthAndRoles(t *testing.T) {
	srv := newTestServer()
	body := webhookReq{URL: "https://example.test/hook", EventTypes: []domain.EventType{domain.EventNoteAdded}}

	tests := []struct {
		name       string
		tenant     string
		user       string
		body       any
		wantStatus int
	}{
		{"unauthenticated", "", "", body, http.StatusUnauthorized},
		{"doctor forbidden", "hosp-A", "doc-a", body, http.StatusForbidden},
		{"invalid url", "hosp-A", "pat-a", webhookReq{URL: "ftp://x", EventTypes: []domain.EventType{domain.EventNoteAdded}}, http.StatusBadRequest},
		{"patient registers", "hosp-A", "pat-a", body, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, srv, http.MethodPost, "/v1/webhooks", tt.tenant, tt.user, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var wh domain.Webhook
				_ = json.Unmarshal(rec.Body.Bytes(), &wh)
				if wh.PatientID != "pat-a" || wh.Secret == "" {
					t.Errorf("wh = %+v, want pat-a with a secret", wh)
				}
			}
		})
	}
}

func TestUnregisterWebhook_Handler(t *testing.T) {
	srv := newTestServer()

	// pat-a registers a webhook.
	rec := doRequest(t, srv, http.MethodPost, "/v1/webhooks", "hosp-A", "pat-a",
		webhookReq{URL: "https://example.test/hook", EventTypes: []domain.EventType{domain.EventNoteAdded}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	var wh domain.Webhook
	_ = json.Unmarshal(rec.Body.Bytes(), &wh)
	path := "/v1/webhooks/" + wh.ID

	// Another patient in the same hospital cannot delete it.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "pat-a2", nil); rec.Code != http.StatusForbidden {
		t.Errorf("other-patient delete = %d, want 403", rec.Code)
	}
	// A patient in another hospital gets not-found.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-B", "pat-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant delete = %d, want 404", rec.Code)
	}
	// The owner deletes it.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "pat-a", nil); rec.Code != http.StatusNoContent {
		t.Errorf("owner delete = %d, want 204", rec.Code)
	}
	// Deleting again is a not-found.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "pat-a", nil); rec.Code != http.StatusNotFound {
		t.Errorf("re-delete = %d, want 404", rec.Code)
	}
}
