package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"medconnect/internal/domain"
)

func TestListActivePrescriptions_Handler(t *testing.T) {
	srv := newTestServer()

	// A doctor issues a prescription on a booked appointment.
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody); rec.Code != http.StatusCreated {
		t.Fatalf("issue rx: %d %s", rec.Code, rec.Body.String())
	}

	// The pharmacist lists active prescriptions.
	rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions?status=active", "hosp-A", "pharm-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	list := decodeList[domain.Prescription](t, rec)
	if len(list) != 1 || list[0].Medication != "Aspirin 100mg" {
		t.Errorf("active list = %+v, want one Aspirin prescription", list)
	}

	// Role + auth checks.
	if rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions", "hosp-A", "doc-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("doctor list = %d, want 403", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth list = %d, want 401", rec.Code)
	}
}

func TestListActivePrescriptions_UnsupportedStatusRejected(t *testing.T) {
	srv := newTestServer()
	rec := doRequest(t, srv, http.MethodGet, "/v1/prescriptions?status=dispatched", "hosp-A", "pharm-a", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported status filter = %d, want 400", rec.Code)
	}
}

func TestDispatchPrescription_Handler(t *testing.T) {
	srv := newTestServer()
	apptID := bookAppointment(t, srv, "hosp-A", "doc-a", "pat-a", 1, 2)
	rxBody := map[string]any{"medication": "Aspirin 100mg", "expiresAt": apiTestNow.Add(30 * 24 * time.Hour)}
	rec := doRequest(t, srv, http.MethodPost, "/v1/appointments/"+apptID+"/prescriptions", "hosp-A", "doc-a", rxBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue rx: %d %s", rec.Code, rec.Body.String())
	}
	var rx domain.Prescription
	_ = json.Unmarshal(rec.Body.Bytes(), &rx)
	path := "/v1/prescriptions/" + rx.ID + "/dispatch"

	// Role/auth checks first.
	if rec := doRequest(t, srv, http.MethodPost, path, "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth dispatch = %d, want 401", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "doc-a", nil); rec.Code != http.StatusForbidden {
		t.Errorf("doctor dispatch = %d, want 403", rec.Code)
	}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/prescriptions/nope/dispatch", "hosp-A", "pharm-a", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown dispatch = %d, want 404", rec.Code)
	}

	// Pharmacist dispatches it once.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "pharm-a", nil); rec.Code != http.StatusOK {
		t.Fatalf("dispatch: %d %s", rec.Code, rec.Body.String())
	}
	// Dispatching again is a conflict.
	if rec := doRequest(t, srv, http.MethodPost, path, "hosp-A", "pharm-a", nil); rec.Code != http.StatusConflict {
		t.Errorf("re-dispatch = %d, want 409", rec.Code)
	}
	// The prescription is no longer in the active list.
	list := doRequest(t, srv, http.MethodGet, "/v1/prescriptions?status=active", "hosp-A", "pharm-a", nil)
	active := decodeList[domain.Prescription](t, list)
	if len(active) != 0 {
		t.Errorf("active after dispatch = %d, want 0", len(active))
	}
}
