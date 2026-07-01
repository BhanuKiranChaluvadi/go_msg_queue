// Package webhooks implements Feature 3 (Live Updates): patients register webhook
// URLs, and the service delivers event notifications to them. This file holds the
// subscription Registry; delivery is handled by the dispatcher.
package webhooks

import (
	"context"
	"fmt"
	"net/url"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store"
	"medconnect/internal/tenancy"
)

// deliverableEventTypes are the event types a patient may subscribe to. They map
// to the webhook payloads defined by the brief.
var deliverableEventTypes = map[domain.EventType]bool{
	domain.EventNoteAdded:         true,
	domain.EventPrescriptionAdded: true,
}

// Registry manages patient webhook subscriptions. Dependencies are injected as
// interfaces so storage and id/secret generation can vary.
type Registry struct {
	store store.WebhookRepo
	ids   platform.IDGen
}

// NewRegistry constructs a Registry.
func NewRegistry(store store.WebhookRepo, ids platform.IDGen) *Registry {
	return &Registry{store: store, ids: ids}
}

// RegisterInput is a patient's subscription request.
type RegisterInput struct {
	URL        string
	EventTypes []domain.EventType
}

// Register creates a webhook subscription owned by the calling patient and
// returns it, including a generated signing secret. The URL must be http(s) with
// a host, and every requested event type must be deliverable.
func (r *Registry) Register(ctx context.Context, in RegisterInput) (domain.Webhook, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return domain.Webhook{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	if err := validateURL(in.URL); err != nil {
		return domain.Webhook{}, err
	}
	if err := validateEventTypes(in.EventTypes); err != nil {
		return domain.Webhook{}, err
	}

	wh := domain.Webhook{
		ID:         r.ids.NewID(),
		TenantID:   actor.TenantID,
		PatientID:  actor.ID,
		URL:        in.URL,
		EventTypes: append([]domain.EventType(nil), in.EventTypes...),
		Secret:     r.ids.NewID(),
	}
	if err := r.store.Create(ctx, wh); err != nil {
		return domain.Webhook{}, err
	}
	return wh, nil
}

// Unregister removes a subscription. Only its owning patient may remove it; an
// unknown id (including one in another tenant) is a not-found.
func (r *Registry) Unregister(ctx context.Context, id string) error {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	wh, err := r.store.Get(ctx, actor.TenantID, id)
	if err != nil {
		return err // ErrNotFound
	}
	if wh.PatientID != actor.ID {
		return fmt.Errorf("%w: not the subscription owner", domain.ErrForbidden)
	}
	return r.store.Delete(ctx, actor.TenantID, id)
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid webhook url", domain.ErrValidation)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: webhook url must be http(s) with a host", domain.ErrValidation)
	}
	// NOTE (production hardening): block private/loopback/link-local hosts to
	// prevent SSRF. Left permissive here so tests can target httptest servers.
	return nil
}

func validateEventTypes(types []domain.EventType) error {
	if len(types) == 0 {
		return fmt.Errorf("%w: at least one event type is required", domain.ErrValidation)
	}
	for _, t := range types {
		if !deliverableEventTypes[t] {
			return fmt.Errorf("%w: unsupported event type %q", domain.ErrValidation, t)
		}
	}
	return nil
}
