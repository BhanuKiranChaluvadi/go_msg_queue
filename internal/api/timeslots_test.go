package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"medconnect/internal/domain"
)

// doRequest issues an authenticated JSON request against the server and returns
// the recorder. Empty user leaves the auth headers off (unauthenticated).
func doRequest(t *testing.T, srv *Server, method, path, tenant, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if user != "" {
		req.Header.Set("X-User-ID", user)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

type slotBody struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func futureSlot(hFrom, hTo int) slotBody {
	return slotBody{
		Start: apiTestNow.Add(time.Duration(hFrom) * time.Hour),
		End:   apiTestNow.Add(time.Duration(hTo) * time.Hour),
	}
}

func TestRegisterTimeslot_Handler_AuthAndRoles(t *testing.T) {
	srv := newTestServer()
	tests := []struct {
		name       string
		tenant     string
		user       string
		body       any
		wantStatus int
	}{
		{"unauthenticated", "", "", futureSlot(1, 2), http.StatusUnauthorized},
		{"patient forbidden", "hosp-A", "pat-a", futureSlot(1, 2), http.StatusForbidden},
		{"doctor creates", "hosp-A", "doc-a", futureSlot(1, 2), http.StatusCreated},
		{"invalid times", "hosp-A", "doc-a", futureSlot(2, 1), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, srv, http.MethodPost, "/v1/timeslots", tt.tenant, tt.user, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var ts domain.Timeslot
				if err := json.Unmarshal(rec.Body.Bytes(), &ts); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if ts.DoctorID != "doc-a" || ts.TenantID != "hosp-A" || ts.Status != domain.TimeslotOpen {
					t.Errorf("timeslot = %+v, want doc-a/hosp-A/open", ts)
				}
			}
		})
	}
}

func TestListDoctorTimeslots_Handler_MultiTenant(t *testing.T) {
	srv := newTestServer()

	// doc-a in hosp-A and doc-b in hosp-B each register one slot.
	if rec := doRequest(t, srv, http.MethodPost, "/v1/timeslots", "hosp-A", "doc-a", futureSlot(1, 2)); rec.Code != http.StatusCreated {
		t.Fatalf("seed A: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, srv, http.MethodPost, "/v1/timeslots", "hosp-B", "doc-b", futureSlot(1, 2)); rec.Code != http.StatusCreated {
		t.Fatalf("seed B: %d %s", rec.Code, rec.Body.String())
	}

	// A patient in hosp-A sees doc-a's slot.
	rec := doRequest(t, srv, http.MethodGet, "/v1/doctors/doc-a/timeslots", "hosp-A", "pat-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list A: %d", rec.Code)
	}
	listA := decodeList[domain.Timeslot](t, rec)
	if len(listA) != 1 || listA[0].DoctorID != "doc-a" {
		t.Errorf("hosp-A list = %+v, want one doc-a slot", listA)
	}

	// A patient in hosp-B cannot see doc-a (a different tenant's doctor).
	rec = doRequest(t, srv, http.MethodGet, "/v1/doctors/doc-a/timeslots", "hosp-B", "pat-b", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list B: %d", rec.Code)
	}
	listB := decodeList[domain.Timeslot](t, rec)
	if len(listB) != 0 {
		t.Errorf("hosp-B list for doc-a = %+v, want empty (tenant isolation)", listB)
	}
}
