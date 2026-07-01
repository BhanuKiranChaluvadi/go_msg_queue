package webhooks

import (
	"context"
	"errors"
	"testing"

	"medconnect/internal/domain"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
)

func newRegistry() (*Registry, *memory.WebhookStore) {
	store := memory.NewWebhookStore()
	return NewRegistry(store, platform.NewFakeIDGen("wh-")), store
}

func patientCtx(tenant, id string) context.Context {
	return tenancy.WithActor(context.Background(),
		tenancy.Actor{ID: id, TenantID: tenant, Role: domain.RolePatient})
}

func TestRegister_Success(t *testing.T) {
	reg, store := newRegistry()
	wh, err := reg.Register(patientCtx("hosp-A", "pat-1"), RegisterInput{
		URL:        "https://example.test/hook",
		EventTypes: []domain.EventType{domain.EventNoteAdded, domain.EventPrescriptionAdded},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if wh.PatientID != "pat-1" || wh.TenantID != "hosp-A" || wh.URL != "https://example.test/hook" {
		t.Errorf("wh = %+v", wh)
	}
	if wh.ID == "" || wh.Secret == "" || wh.ID == wh.Secret {
		t.Errorf("expected distinct non-empty id and secret, got id=%q secret=%q", wh.ID, wh.Secret)
	}
	// Persisted.
	if _, err := store.Get(context.Background(), "hosp-A", wh.ID); err != nil {
		t.Errorf("subscription not stored: %v", err)
	}
}

func TestRegister_Validation(t *testing.T) {
	reg, _ := newRegistry()
	tests := []struct {
		name  string
		input RegisterInput
	}{
		{"bad scheme", RegisterInput{URL: "ftp://x/y", EventTypes: []domain.EventType{domain.EventNoteAdded}}},
		{"no host", RegisterInput{URL: "https:///path", EventTypes: []domain.EventType{domain.EventNoteAdded}}},
		{"empty url", RegisterInput{URL: "", EventTypes: []domain.EventType{domain.EventNoteAdded}}},
		{"no event types", RegisterInput{URL: "https://x.test/y", EventTypes: nil}},
		{"unsupported event type", RegisterInput{URL: "https://x.test/y", EventTypes: []domain.EventType{domain.EventAppointmentBooked}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := reg.Register(patientCtx("hosp-A", "pat-1"), tt.input); !errors.Is(err, domain.ErrValidation) {
				t.Errorf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestRegister_NoActorForbidden(t *testing.T) {
	reg, _ := newRegistry()
	if _, err := reg.Register(context.Background(), RegisterInput{URL: "https://x.test/y", EventTypes: []domain.EventType{domain.EventNoteAdded}}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
}

func TestUnregister_OwnerOnly(t *testing.T) {
	reg, store := newRegistry()
	wh, _ := reg.Register(patientCtx("hosp-A", "pat-1"), RegisterInput{URL: "https://x.test/y", EventTypes: []domain.EventType{domain.EventNoteAdded}})

	// A different patient cannot remove it.
	if err := reg.Unregister(patientCtx("hosp-A", "pat-2"), wh.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("other-patient unregister err = %v, want ErrForbidden", err)
	}
	// The owner can.
	if err := reg.Unregister(patientCtx("hosp-A", "pat-1"), wh.ID); err != nil {
		t.Fatalf("owner unregister: %v", err)
	}
	if _, err := store.Get(context.Background(), "hosp-A", wh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("subscription still present after unregister")
	}
}

func TestUnregister_UnknownAndCrossTenant(t *testing.T) {
	reg, _ := newRegistry()
	wh, _ := reg.Register(patientCtx("hosp-A", "pat-1"), RegisterInput{URL: "https://x.test/y", EventTypes: []domain.EventType{domain.EventNoteAdded}})

	if err := reg.Unregister(patientCtx("hosp-A", "pat-1"), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown err = %v, want ErrNotFound", err)
	}
	// A patient in another hospital cannot see (or delete) hosp-A's subscription.
	if err := reg.Unregister(patientCtx("hosp-B", "pat-1"), wh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant err = %v, want ErrNotFound", err)
	}
}

func TestRegister_MultiTenantAndMultiPatientIsolation(t *testing.T) {
	reg, store := newRegistry()
	_, _ = reg.Register(patientCtx("hosp-A", "pat-1"), RegisterInput{URL: "https://a1.test", EventTypes: []domain.EventType{domain.EventNoteAdded}})
	_, _ = reg.Register(patientCtx("hosp-A", "pat-2"), RegisterInput{URL: "https://a2.test", EventTypes: []domain.EventType{domain.EventNoteAdded}})
	_, _ = reg.Register(patientCtx("hosp-B", "pat-1"), RegisterInput{URL: "https://b1.test", EventTypes: []domain.EventType{domain.EventPrescriptionAdded}})

	a, _ := store.ListByTenant(context.Background(), "hosp-A")
	b, _ := store.ListByTenant(context.Background(), "hosp-B")
	if len(a) != 2 || len(b) != 1 {
		t.Errorf("hosp-A=%d hosp-B=%d, want 2 and 1", len(a), len(b))
	}
}
