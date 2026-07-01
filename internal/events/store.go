package events

import (
	"sort"
	"sync"

	"medconnect/internal/domain"
)

// Store is an append-only, tenant-scoped log of events. Entries are never
// mutated or removed, which is what lets it double as the audit trail and the
// source for point-in-time history.
type Store struct {
	mu       sync.RWMutex
	byTenant map[string][]domain.Event
}

// NewStore returns an empty event Store.
func NewStore() *Store {
	return &Store{byTenant: make(map[string][]domain.Event)}
}

// Append records e in its tenant's log.
func (s *Store) Append(e domain.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTenant[e.TenantID] = append(s.byTenant[e.TenantID], e)
}

// Query returns the tenant's events matching f, ordered by (timestamp, id) so
// results are deterministic even when several events share a timestamp.
func (s *Store) Query(tenant string, f Filter) []domain.Event {
	s.mu.RLock()
	src := s.byTenant[tenant]
	out := make([]domain.Event, 0, len(src))
	for _, e := range src {
		if f.matches(e) {
			out = append(out, e)
		}
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].ID < out[j].ID
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}
