package domain

import (
	"testing"
	"time"
)

func TestPrescriptionIsActive(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	expires := base.Add(24 * time.Hour)

	tests := []struct {
		name   string
		status PrescriptionStatus
		now    time.Time
		want   bool
	}{
		{"active before expiry", PrescriptionActive, base, true},
		{"active at expiry boundary", PrescriptionActive, expires, false},
		{"active after expiry", PrescriptionActive, expires.Add(time.Second), false},
		{"dispatched before expiry", PrescriptionDispatched, base, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Prescription{Status: tt.status, ExpiresAt: expires}
			if got := p.IsActive(tt.now); got != tt.want {
				t.Errorf("IsActive(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestPrescriptionEffectiveStatus(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	expires := base.Add(24 * time.Hour)

	tests := []struct {
		name   string
		status PrescriptionStatus
		now    time.Time
		want   PrescriptionStatus
	}{
		{"active", PrescriptionActive, base, PrescriptionActive},
		{"expired by time", PrescriptionActive, expires.Add(time.Hour), PrescriptionExpired},
		{"dispatched stays dispatched even after expiry", PrescriptionDispatched, expires.Add(time.Hour), PrescriptionDispatched},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Prescription{Status: tt.status, ExpiresAt: expires}
			if got := p.EffectiveStatus(tt.now); got != tt.want {
				t.Errorf("EffectiveStatus(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestDiagnosisIsActiveAt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	t2 := t0.Add(48 * time.Hour)
	dismissedAtT1 := t1

	tests := []struct {
		name        string
		diagnosedAt time.Time
		dismissedAt *time.Time
		at          time.Time
		want        bool
	}{
		{"before diagnosis", t1, nil, t0, false},
		{"at diagnosis boundary", t1, nil, t1, true},
		{"after diagnosis, never dismissed", t0, nil, t2, true},
		{"active before dismissal", t0, &dismissedAtT1, t0, true},
		{"inactive at dismissal boundary", t0, &dismissedAtT1, t1, false},
		{"still active as-of before a later dismissal", t0, &dismissedAtT1, t0.Add(12 * time.Hour), true},
		{"inactive after dismissal", t0, &dismissedAtT1, t2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Diagnosis{DiagnosedAt: tt.diagnosedAt, DismissedAt: tt.dismissedAt}
			if got := d.IsActiveAt(tt.at); got != tt.want {
				t.Errorf("IsActiveAt(%v) = %v, want %v", tt.at, got, tt.want)
			}
		})
	}
}
