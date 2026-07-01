// Package analytics implements Feature 7 (Usage Analytics). Per-tenant usage
// metrics are aggregated over the same append-only event log that powers the
// audit trail, so no separate tracking system is needed.
package analytics

import (
	"context"
	"fmt"

	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/tenancy"
)

// Service computes usage metrics for a tenant from the event log.
type Service struct {
	store *events.Store
}

// NewService builds a Service backed by the event log.
func NewService(store *events.Store) *Service {
	return &Service{store: store}
}

// PrescriptionCounts breaks prescriptions down by lifecycle.
type PrescriptionCounts struct {
	Issued     int `json:"issued"`
	Dispatched int `json:"dispatched"`
}

// Summary is the usage snapshot for a tenant.
type Summary struct {
	TotalAppointments  int                `json:"totalAppointments"`
	ActivePatients     int                `json:"activePatients"`
	PrescriptionCounts PrescriptionCounts `json:"prescriptionCounts"`
}

// Summarize aggregates the caller's tenant usage: total appointments booked, the
// number of distinct patients who have booked (active patients), and prescription
// counts by lifecycle.
func (s *Service) Summarize(ctx context.Context) (Summary, error) {
	tenant, ok := tenancy.TenantFrom(ctx)
	if !ok {
		return Summary{}, fmt.Errorf("%w: no tenant", domain.ErrForbidden)
	}

	evs := s.store.Query(tenant, events.Filter{})
	activePatients := make(map[string]struct{})
	var sum Summary
	for _, e := range evs {
		switch e.Type {
		case domain.EventAppointmentBooked:
			sum.TotalAppointments++
			if pid := asString(e.Payload["patientId"]); pid != "" {
				activePatients[pid] = struct{}{}
			}
		case domain.EventPrescriptionAdded:
			sum.PrescriptionCounts.Issued++
		case domain.EventPrescriptionDispatched:
			sum.PrescriptionCounts.Dispatched++
		}
	}
	sum.ActivePatients = len(activePatients)
	return sum, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
