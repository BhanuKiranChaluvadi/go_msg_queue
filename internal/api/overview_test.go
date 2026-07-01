package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/appointments"
	"medconnect/internal/domain"
)

func TestAppointmentOverview_Handler(t *testing.T) {
	srv := newTestServer()
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)

	// Doctor adds a note and a prescription.
	notePath := "/v1/appointments/" + apptID + "/notes"
	if rec := doRequest(t, srv, http.MethodPost, notePath, "hosp-A", "doc-a", map[string]string{"text": "Reports headache."}); rec.Code != http.StatusCreated {
		t.Fatalf("add note: %d %s", rec.Code, rec.Body.String())
	}
	rxPath := "/v1/appointments/" + apptID + "/prescriptions"
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, rxPath, "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("issue rx: %d %s", rec.Code, rec.Body.String())
	}

	path := "/v1/appointments/" + apptID

	// Both participants see the full overview.
	for _, who := range []struct{ tenant, user string }{{"hosp-A", "doc-a"}, {"hosp-A", "pat-a"}} {
		rec := doRequest(t, srv, http.MethodGet, path, who.tenant, who.user, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s overview: %d %s", who.user, rec.Code, rec.Body.String())
		}
		var ov appointments.Overview
		if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ov.Appointment.ID != apptID || len(ov.Notes) != 1 || len(ov.Prescriptions) != 1 {
			t.Errorf("%s overview = appt %s, %d notes, %d rx; want 1/1", who.user, ov.Appointment.ID, len(ov.Notes), len(ov.Prescriptions))
		}
	}

	// A non-participant patient in the same hospital is forbidden.
	if rec := doRequest(t, srv, http.MethodGet, path, "hosp-A", "pat-a2", nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-participant overview = %d, want 403", rec.Code)
	}
	// Unauthenticated.
	if rec := doRequest(t, srv, http.MethodGet, path, "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth overview = %d, want 401", rec.Code)
	}
	// Cross-tenant is a not-found.
	if rec := doRequest(t, srv, http.MethodGet, path, "hosp-B", "doc-b", nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant overview = %d, want 404", rec.Code)
	}
}

// TestAppointmentRoutes_NextNotShadowed guards against the {id} route swallowing
// the literal /next route.
func TestAppointmentRoutes_NextNotShadowed(t *testing.T) {
	srv := newTestServer()
	rec := doRequest(t, srv, http.MethodGet, "/v1/appointments/next", "hosp-A", "doc-a", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("/next status = %d, want 200 (not shadowed by {id})", rec.Code)
	}
	// Decoding as a list envelope confirms /next resolved to the list handler
	// rather than being shadowed by the /{id} route.
	_ = decodeList[domain.Appointment](t, rec)
}
