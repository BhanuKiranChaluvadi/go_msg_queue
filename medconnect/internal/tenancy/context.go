// Package tenancy resolves the calling tenant and actor for each request and
// enforces role-based access. Tenant and actor flow through context.Context so
// that every downstream store and service call is tenant-scoped by construction.
package tenancy

import (
	"context"

	"medconnect/internal/domain"
)

// Actor is the authenticated caller: who they are, which tenant they belong to,
// and what role authorizes them.
type Actor struct {
	ID       string
	TenantID string
	Role     domain.Role
}

type ctxKey int

const actorKey ctxKey = iota

// WithActor returns a copy of ctx carrying the actor.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey, a)
}

// ActorFrom returns the actor stored in ctx, if any.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey).(Actor)
	return a, ok
}

// TenantFrom returns the tenant id of the actor stored in ctx, if any.
func TenantFrom(ctx context.Context) (string, bool) {
	a, ok := ActorFrom(ctx)
	if !ok {
		return "", false
	}
	return a.TenantID, true
}
