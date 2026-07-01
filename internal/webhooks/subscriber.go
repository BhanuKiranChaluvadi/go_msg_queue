package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"medconnect/internal/domain"
)

// Enqueuer accepts webhook deliveries. The Dispatcher implements it; depending on
// the interface (not the concrete dispatcher) keeps the Subscriber unit-testable.
type Enqueuer interface {
	Enqueue(Delivery) bool
}

// Subscriber turns domain events into webhook deliveries. It implements
// events.Subscriber, so the event Publisher fans out to it; it looks up the
// patient's matching subscriptions and enqueues a signed delivery for each.
type Subscriber struct {
	registry *Registry
	out      Enqueuer
	logger   *slog.Logger
}

// NewSubscriber builds a Subscriber.
func NewSubscriber(registry *Registry, out Enqueuer, logger *slog.Logger) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &Subscriber{registry: registry, out: out, logger: logger}
}

// Notify is invoked (synchronously) by the publisher for every event. It returns
// quickly: a fast in-memory lookup plus a non-blocking enqueue per subscription,
// so it never stalls the request path.
func (s *Subscriber) Notify(ctx context.Context, e domain.Event) {
	if !deliverableEventTypes[e.Type] {
		return
	}
	patientID := asString(e.Payload["patientId"])
	if patientID == "" {
		return
	}

	subs, err := s.registry.ForPatientEvent(ctx, e.TenantID, patientID, e.Type)
	if err != nil {
		s.logger.Error("webhook subscription lookup failed", "tenant", e.TenantID, "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}

	body, err := json.Marshal(buildEventPayload(e))
	if err != nil {
		s.logger.Error("webhook payload marshal failed", "eventId", e.ID, "err", err)
		return
	}
	for _, wh := range subs {
		s.out.Enqueue(Delivery{
			WebhookID: wh.ID,
			URL:       wh.URL,
			Secret:    wh.Secret,
			EventID:   e.ID,
			Payload:   body,
		})
	}
}

// ForPatientEvent returns the tenant's subscriptions owned by patientID that are
// subscribed to eventType.
func (r *Registry) ForPatientEvent(ctx context.Context, tenant, patientID string, eventType domain.EventType) ([]domain.Webhook, error) {
	all, err := r.store.ListByTenant(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var out []domain.Webhook
	for _, wh := range all {
		if wh.PatientID != patientID {
			continue
		}
		for _, t := range wh.EventTypes {
			if t == eventType {
				out = append(out, wh)
				break
			}
		}
	}
	return out, nil
}

// eventPayload is the webhook notification body sent to subscribers.
type eventPayload struct {
	EventID       string         `json:"eventId"`
	EventType     string         `json:"eventType"`
	Timestamp     time.Time      `json:"timestamp"`
	AppointmentID string         `json:"appointmentId"`
	PatientID     string         `json:"patientId"`
	Data          map[string]any `json:"data"`
}

func buildEventPayload(e domain.Event) eventPayload {
	return eventPayload{
		EventID:       e.ID,
		EventType:     string(e.Type),
		Timestamp:     e.Timestamp,
		AppointmentID: asString(e.Payload["appointmentId"]),
		PatientID:     asString(e.Payload["patientId"]),
		Data:          eventData(e),
	}
}

// eventData projects the event-specific fields for the "data" object.
func eventData(e domain.Event) map[string]any {
	switch e.Type {
	case domain.EventNoteAdded:
		return map[string]any{
			"noteId":   e.Payload["noteId"],
			"noteText": e.Payload["noteText"],
		}
	case domain.EventPrescriptionAdded:
		return map[string]any{
			"prescriptionId": e.Payload["prescriptionId"],
			"medication":     e.Payload["medication"],
			"expiresAt":      e.Payload["expiresAt"],
		}
	default:
		return map[string]any{}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
