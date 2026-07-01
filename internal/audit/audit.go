// Package audit implements Feature 6 (Audit Trail). Every mutation already
// appends an immutable event carrying who (ActorID) and when (Timestamp) to the
// event log, so the audit trail is a filtered read view over that log rather than
// a separate system to keep in sync.
package audit

import (
	"context"
	"fmt"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/tenancy"
)

// Service answers audit queries over the tenant event log.
type Service struct {
	store *events.Store
}

// NewService builds a Service backed by the event log.
func NewService(store *events.Store) *Service {
	return &Service{store: store}
}

// Query narrows the audit trail. Zero-value fields match everything.
type Query struct {
	PatientID string
	Types     []domain.EventType
	From      time.Time
	To        time.Time
}

// Record is one audit entry: what changed, who changed it, and when.
type Record struct {
	EventID   string         `json:"eventId"`
	Type      string         `json:"type"`
	ActorID   string         `json:"actorId"`
	Timestamp time.Time      `json:"timestamp"`
	EntityRef string         `json:"entityRef"`
	PatientID string         `json:"patientId,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Query returns audit records for the caller's tenant, ordered oldest-first.
func (s *Service) Query(ctx context.Context, q Query) ([]Record, error) {
	tenant, ok := tenancy.TenantFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: no tenant", domain.ErrForbidden)
	}

	evs := s.store.Query(tenant, events.Filter{Types: q.Types, From: q.From, To: q.To})
	out := make([]Record, 0, len(evs))
	for _, e := range evs {
		patientID := asString(e.Payload["patientId"])
		if q.PatientID != "" && patientID != q.PatientID {
			continue
		}
		out = append(out, Record{
			EventID:   e.ID,
			Type:      string(e.Type),
			ActorID:   e.ActorID,
			Timestamp: e.Timestamp,
			EntityRef: e.EntityRef,
			PatientID: patientID,
			Data:      e.Payload,
		})
	}
	return out, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
