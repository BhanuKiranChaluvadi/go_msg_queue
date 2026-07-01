// Package memory provides in-memory, tenant-scoped implementations of the
// store interfaces. Each adapter is backed by a generic Store guarded by a
// RWMutex; data is partitioned by tenant so no query can cross tenants.
package memory

import (
	"context"
	"sync"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/store"
)

// Store is a generic, concurrency-safe, tenant-partitioned key-value store. The
// id and tenant extractors let it index any entity type without reflection.
type Store[T any] struct {
	mu       sync.RWMutex
	byTenant map[string]map[string]T
	id       func(T) string
	tenant   func(T) string
}

// NewStore builds a Store using the given id and tenant extractors.
func NewStore[T any](id, tenant func(T) string) *Store[T] {
	return &Store[T]{
		byTenant: make(map[string]map[string]T),
		id:       id,
		tenant:   tenant,
	}
}

// Put inserts or overwrites v, partitioned by its tenant.
func (s *Store[T]) Put(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tenant(v)
	m := s.byTenant[t]
	if m == nil {
		m = make(map[string]T)
		s.byTenant[t] = m
	}
	m[s.id(v)] = v
}

// Get returns the entity for (tenant, id) and whether it was found.
func (s *Store[T]) Get(tenant, id string) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var zero T
	m := s.byTenant[tenant]
	if m == nil {
		return zero, false
	}
	v, ok := m[id]
	return v, ok
}

// Exists reports whether (tenant, id) is present.
func (s *Store[T]) Exists(tenant, id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.byTenant[tenant]
	if m == nil {
		return false
	}
	_, ok := m[id]
	return ok
}

// Delete removes (tenant, id), reporting whether it existed.
func (s *Store[T]) Delete(tenant, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.byTenant[tenant]
	if m == nil {
		return false
	}
	if _, ok := m[id]; !ok {
		return false
	}
	delete(m, id)
	return true
}

// List returns a snapshot of all entities for a tenant (unordered).
func (s *Store[T]) List(tenant string) []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.byTenant[tenant]
	out := make([]T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// base supplies the uniform Create/Get/Update/Delete operations shared by every
// adapter; aggregate-specific queries are added by the embedding type.
type base[T any] struct {
	s *Store[T]
}

func (b base[T]) Create(_ context.Context, v T) error {
	b.s.Put(v)
	return nil
}

func (b base[T]) Get(_ context.Context, tenant, id string) (T, error) {
	v, ok := b.s.Get(tenant, id)
	if !ok {
		var zero T
		return zero, domain.ErrNotFound
	}
	return v, nil
}

func (b base[T]) Update(_ context.Context, v T) error {
	if !b.s.Exists(b.s.tenant(v), b.s.id(v)) {
		return domain.ErrNotFound
	}
	b.s.Put(v)
	return nil
}

func (b base[T]) Delete(_ context.Context, tenant, id string) error {
	if !b.s.Delete(tenant, id) {
		return domain.ErrNotFound
	}
	return nil
}

// --- Timeslots ---

// TimeslotStore is the in-memory TimeslotRepo.
type TimeslotStore struct{ base[domain.Timeslot] }

// NewTimeslotStore builds an empty TimeslotStore.
func NewTimeslotStore() *TimeslotStore {
	return &TimeslotStore{base[domain.Timeslot]{NewStore(
		func(t domain.Timeslot) string { return t.ID },
		func(t domain.Timeslot) string { return t.TenantID },
	)}}
}

// ListByDoctor returns a tenant's timeslots for one doctor.
func (st *TimeslotStore) ListByDoctor(_ context.Context, tenant, doctorID string) ([]domain.Timeslot, error) {
	out := make([]domain.Timeslot, 0)
	for _, t := range st.s.List(tenant) {
		if t.DoctorID == doctorID {
			out = append(out, t)
		}
	}
	return out, nil
}

// --- Appointments ---

// AppointmentStore is the in-memory AppointmentRepo.
type AppointmentStore struct{ base[domain.Appointment] }

// NewAppointmentStore builds an empty AppointmentStore.
func NewAppointmentStore() *AppointmentStore {
	return &AppointmentStore{base[domain.Appointment]{NewStore(
		func(a domain.Appointment) string { return a.ID },
		func(a domain.Appointment) string { return a.TenantID },
	)}}
}

// ExistsForPatientDoctor reports whether a non-cancelled appointment exists for
// the patient-doctor pair.
func (st *AppointmentStore) ExistsForPatientDoctor(_ context.Context, tenant, patientID, doctorID string) (bool, error) {
	for _, a := range st.s.List(tenant) {
		if a.PatientID == patientID && a.DoctorID == doctorID && a.Status != domain.AppointmentCancelled {
			return true, nil
		}
	}
	return false, nil
}

// NextForDoctor returns the doctor's appointments scheduled at or after from.
func (st *AppointmentStore) NextForDoctor(_ context.Context, tenant, doctorID string, from time.Time) ([]domain.Appointment, error) {
	out := make([]domain.Appointment, 0)
	for _, a := range st.s.List(tenant) {
		if a.DoctorID == doctorID && !a.Start.Before(from) {
			out = append(out, a)
		}
	}
	return out, nil
}

// --- Notes ---

// NoteStore is the in-memory NoteRepo.
type NoteStore struct{ base[domain.Note] }

// NewNoteStore builds an empty NoteStore.
func NewNoteStore() *NoteStore {
	return &NoteStore{base[domain.Note]{NewStore(
		func(n domain.Note) string { return n.ID },
		func(n domain.Note) string { return n.TenantID },
	)}}
}

// ListByAppointment returns a tenant's notes for one appointment.
func (st *NoteStore) ListByAppointment(_ context.Context, tenant, appointmentID string) ([]domain.Note, error) {
	out := make([]domain.Note, 0)
	for _, n := range st.s.List(tenant) {
		if n.AppointmentID == appointmentID {
			out = append(out, n)
		}
	}
	return out, nil
}

// --- Prescriptions ---

// PrescriptionStore is the in-memory PrescriptionRepo.
type PrescriptionStore struct{ base[domain.Prescription] }

// NewPrescriptionStore builds an empty PrescriptionStore.
func NewPrescriptionStore() *PrescriptionStore {
	return &PrescriptionStore{base[domain.Prescription]{NewStore(
		func(p domain.Prescription) string { return p.ID },
		func(p domain.Prescription) string { return p.TenantID },
	)}}
}

// ListByAppointment returns a tenant's prescriptions for one appointment.
func (st *PrescriptionStore) ListByAppointment(_ context.Context, tenant, appointmentID string) ([]domain.Prescription, error) {
	out := make([]domain.Prescription, 0)
	for _, p := range st.s.List(tenant) {
		if p.AppointmentID == appointmentID {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListByTenant returns all prescriptions for a tenant.
func (st *PrescriptionStore) ListByTenant(_ context.Context, tenant string) ([]domain.Prescription, error) {
	return st.s.List(tenant), nil
}

// --- Diagnoses ---

// DiagnosisStore is the in-memory DiagnosisRepo.
type DiagnosisStore struct{ base[domain.Diagnosis] }

// NewDiagnosisStore builds an empty DiagnosisStore.
func NewDiagnosisStore() *DiagnosisStore {
	return &DiagnosisStore{base[domain.Diagnosis]{NewStore(
		func(d domain.Diagnosis) string { return d.ID },
		func(d domain.Diagnosis) string { return d.TenantID },
	)}}
}

// ListByPatient returns a tenant's diagnoses for one patient.
func (st *DiagnosisStore) ListByPatient(_ context.Context, tenant, patientID string) ([]domain.Diagnosis, error) {
	out := make([]domain.Diagnosis, 0)
	for _, d := range st.s.List(tenant) {
		if d.PatientID == patientID {
			out = append(out, d)
		}
	}
	return out, nil
}

// --- Webhooks ---

// WebhookStore is the in-memory WebhookRepo.
type WebhookStore struct{ base[domain.Webhook] }

// NewWebhookStore builds an empty WebhookStore.
func NewWebhookStore() *WebhookStore {
	return &WebhookStore{base[domain.Webhook]{NewStore(
		func(w domain.Webhook) string { return w.ID },
		func(w domain.Webhook) string { return w.TenantID },
	)}}
}

// ListByTenant returns all webhook subscriptions for a tenant.
func (st *WebhookStore) ListByTenant(_ context.Context, tenant string) ([]domain.Webhook, error) {
	return st.s.List(tenant), nil
}

// Compile-time guarantees that every adapter satisfies its interface.
var (
	_ store.TimeslotRepo     = (*TimeslotStore)(nil)
	_ store.AppointmentRepo  = (*AppointmentStore)(nil)
	_ store.NoteRepo         = (*NoteStore)(nil)
	_ store.PrescriptionRepo = (*PrescriptionStore)(nil)
	_ store.DiagnosisRepo    = (*DiagnosisStore)(nil)
	_ store.WebhookRepo      = (*WebhookStore)(nil)
)
