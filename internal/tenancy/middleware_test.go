package tenancy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"medconnect/internal/domain"
)

func resolver() StaticResolver {
	return StaticResolver{
		"doc1": {ID: "doc1", TenantID: "A", Role: domain.RoleDoctor},
		"pat1": {ID: "pat1", TenantID: "A", Role: domain.RolePatient},
	}
}

func TestAuthenticate(t *testing.T) {
	var seen Actor
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = ActorFrom(r.Context())
	})
	h := Authenticate(resolver())(next)

	tests := []struct {
		name       string
		tenant     string
		user       string
		wantStatus int
	}{
		{"missing headers", "", "", http.StatusUnauthorized},
		{"missing user", "A", "", http.StatusUnauthorized},
		{"unknown user", "A", "ghost", http.StatusUnauthorized},
		{"wrong tenant for user", "B", "doc1", http.StatusUnauthorized},
		{"valid", "A", "doc1", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen = Actor{}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.tenant != "" {
				req.Header.Set("X-Tenant-ID", tt.tenant)
			}
			if tt.user != "" {
				req.Header.Set("X-User-ID", tt.user)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && seen.ID != "doc1" {
				t.Errorf("actor = %+v, want doc1 injected into context", seen)
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name       string
		actor      *Actor
		want       domain.Role
		wantStatus int
	}{
		{"no actor", nil, domain.RoleDoctor, http.StatusUnauthorized},
		{"wrong role", &Actor{ID: "pat1", TenantID: "A", Role: domain.RolePatient}, domain.RoleDoctor, http.StatusForbidden},
		{"correct role", &Actor{ID: "doc1", TenantID: "A", Role: domain.RoleDoctor}, domain.RoleDoctor, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.actor != nil {
				req = req.WithContext(WithActor(req.Context(), *tt.actor))
			}
			rec := httptest.NewRecorder()
			RequireRole(tt.want, ok).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestTenantFrom(t *testing.T) {
	ctx := WithActor(httptest.NewRequest(http.MethodGet, "/", nil).Context(),
		Actor{ID: "doc1", TenantID: "A", Role: domain.RoleDoctor})
	if tenant, ok := TenantFrom(ctx); !ok || tenant != "A" {
		t.Errorf("TenantFrom = %q, %v; want A, true", tenant, ok)
	}
	if _, ok := TenantFrom(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Error("TenantFrom on empty context should report not-ok")
	}
}
