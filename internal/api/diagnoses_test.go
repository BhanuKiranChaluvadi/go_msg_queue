package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"medconnect/internal/domain"
)

func TestDiagnose_Handler(t *testing.T) {
	srv := newTestServer()

	tests := []struct {
		name       string
		tenant     string
		user       string
		body       any
		wantStatus int
	}{
		{"unauthenticated", "", "", map[string]string{"disease": "Flu"}, http.StatusUnauthorized},
		{"patient forbidden", "hosp-A", "pat-a", map[string]string{"disease": "Flu"}, http.StatusForbidden},
		{"empty disease", "hosp-A", "doc-a", map[string]string{"disease": ""}, http.StatusBadRequest},
		{"doctor diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Hypertension"}, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", tt.tenant, tt.user, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var d domain.Diagnosis
				_ = json.Unmarshal(rec.Body.Bytes(), &d)
				if d.PatientID != "pat-a" || d.Disease != "Hypertension" {
					t.Errorf("diagnosis = %+v", d)
				}
			}
		})
	}
}

func TestDismissDiagnosis_Handler(t *testing.T) {
	srv := newTestServer()
	rec := doRequest(t, srv, http.MethodPost, "/v1/patients/pat-a/diagnoses", "hosp-A", "doc-a", map[string]string{"disease": "Flu"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("diagnose: %d", rec.Code)
	}
	var d domain.Diagnosis
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	path := "/v1/patients/pat-a/diagnoses/" + d.ID

	// Doctor dismisses it.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "doc-a", nil); rec.Code != http.StatusNoContent {
		t.Errorf("dismiss = %d, want 204", rec.Code)
	}
	// Dismissing again is a conflict.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "doc-a", nil); rec.Code != http.StatusConflict {
		t.Errorf("re-dismiss = %d, want 409", rec.Code)
	}
	// Patient may not dismiss.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-A", "pat-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("patient dismiss = %d, want 403", rec.Code)
	}
	// Cross-tenant is a not-found.
	if rec := doRequest(t, srv, http.MethodDelete, path, "hosp-B", "doc-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant dismiss = %d, want 404", rec.Code)
	}
}
