package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"medconnect/internal/domain"
)

// ErrUnknownActor is returned by a resolver when no actor matches the supplied
// tenant and user id.
var ErrUnknownActor = errors.New("tenancy: unknown actor")

// ActorResolver looks up the Actor for an authenticated (tenant, user) pair.
// Production would back this with a user store; the core uses StaticResolver.
type ActorResolver interface {
	Resolve(ctx context.Context, tenantID, userID string) (Actor, error)
}

// StaticResolver is an in-memory ActorResolver keyed by user id, used for wiring
// and tests. It verifies the actor belongs to the requested tenant.
type StaticResolver map[string]Actor

// Resolve returns the actor for userID if it exists in the requested tenant.
func (s StaticResolver) Resolve(_ context.Context, tenantID, userID string) (Actor, error) {
	a, ok := s[userID]
	if !ok || a.TenantID != tenantID {
		return Actor{}, ErrUnknownActor
	}
	return a, nil
}

// Authenticate is middleware that resolves the tenant and actor from the
// X-Tenant-ID and X-User-ID request headers and injects the actor into context.
// Missing headers or an unknown actor yield 401.
func Authenticate(resolver ActorResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := r.Header.Get("X-Tenant-ID")
			userID := r.Header.Get("X-User-ID")
			if tenantID == "" || userID == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing X-Tenant-ID or X-User-ID")
				return
			}
			actor, err := resolver.Resolve(r.Context(), tenantID, userID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unknown tenant or user")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
		})
	}
}

// RequireRole wraps a handler so only actors with the given role may proceed.
// A missing actor yields 401; a wrong role yields 403.
func RequireRole(role domain.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := ActorFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "no authenticated actor")
			return
		}
		if actor.Role != role {
			writeError(w, http.StatusForbidden, "forbidden", "requires role "+string(role))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeError emits a minimal JSON error envelope. The shared envelope in the api
// package (Task 0.6) supersedes this for feature handlers; tenancy keeps its own
// to avoid importing api and creating an import cycle.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
