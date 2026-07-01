package clinical

import (
	"context"
	"fmt"
	"sort"
	"time"

	"medconnect/internal/domain"
	"medconnect/internal/tenancy"
)

// AppointmentNotes pairs an appointment with the notes it had as of the overview
// time.
type AppointmentNotes struct {
	Appointment domain.Appointment `json:"appointment"`
	Notes       []domain.Note      `json:"notes"`
}

// PatientOverview is a patient's clinical picture as of a point in time.
type PatientOverview struct {
	PatientID           string                `json:"patientId"`
	At                  time.Time             `json:"at"`
	Diagnoses           []domain.Diagnosis    `json:"diagnoses"`
	ActivePrescriptions []domain.Prescription `json:"activePrescriptions"`
	Appointments        []AppointmentNotes    `json:"appointments"`
}

// Overview assembles a patient's diagnoses, active prescriptions, and
// appointments-with-notes as they were at time at (zero at means now). It is
// visible to the patient themselves and to any doctor in the tenant.
func (s *Service) Overview(ctx context.Context, patientID string, at time.Time) (PatientOverview, error) {
	actor, ok := tenancy.ActorFrom(ctx)
	if !ok {
		return PatientOverview{}, fmt.Errorf("%w: no actor", domain.ErrForbidden)
	}
	switch actor.Role {
	case domain.RolePatient:
		if actor.ID != patientID {
			return PatientOverview{}, fmt.Errorf("%w: patients may only view their own overview", domain.ErrForbidden)
		}
	case domain.RoleDoctor:
		// A doctor may view any patient in their tenant.
	default:
		return PatientOverview{}, fmt.Errorf("%w: not permitted to view patient overview", domain.ErrForbidden)
	}
	if at.IsZero() {
		at = s.clock.Now()
	}
	tenant := actor.TenantID

	diagnoses, err := s.activeDiagnoses(ctx, tenant, patientID, at)
	if err != nil {
		return PatientOverview{}, err
	}
	prescriptions, err := s.activePrescriptions(ctx, tenant, patientID, at)
	if err != nil {
		return PatientOverview{}, err
	}
	appointments, err := s.appointmentsWithNotes(ctx, tenant, patientID, at)
	if err != nil {
		return PatientOverview{}, err
	}

	return PatientOverview{
		PatientID:           patientID,
		At:                  at,
		Diagnoses:           diagnoses,
		ActivePrescriptions: prescriptions,
		Appointments:        appointments,
	}, nil
}

func (s *Service) activeDiagnoses(ctx context.Context, tenant, patientID string, at time.Time) ([]domain.Diagnosis, error) {
	all, err := s.diagnoses.ListByPatient(ctx, tenant, patientID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Diagnosis, 0)
	for _, d := range all {
		if d.IsActiveAt(at) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DiagnosedAt.Before(out[j].DiagnosedAt) })
	return out, nil
}

func (s *Service) activePrescriptions(ctx context.Context, tenant, patientID string, at time.Time) ([]domain.Prescription, error) {
	all, err := s.prescriptions.ListByTenant(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Prescription, 0)
	for _, p := range all {
		if p.PatientID == patientID && p.ActiveAt(at) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.Before(out[j].IssuedAt) })
	return out, nil
}

func (s *Service) appointmentsWithNotes(ctx context.Context, tenant, patientID string, at time.Time) ([]AppointmentNotes, error) {
	appts, err := s.appointments.ListByPatient(ctx, tenant, patientID)
	if err != nil {
		return nil, err
	}
	out := make([]AppointmentNotes, 0)
	for _, a := range appts {
		if a.CreatedAt.After(at) {
			continue // not booked yet as of at
		}
		notes, err := s.notes.ListByAppointment(ctx, tenant, a.ID)
		if err != nil {
			return nil, err
		}
		kept := make([]domain.Note, 0, len(notes))
		for _, n := range notes {
			if !n.CreatedAt.After(at) {
				kept = append(kept, n)
			}
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].CreatedAt.Before(kept[j].CreatedAt) })
		out = append(out, AppointmentNotes{Appointment: a, Notes: kept})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Appointment.Start.Before(out[j].Appointment.Start) })
	return out, nil
}
