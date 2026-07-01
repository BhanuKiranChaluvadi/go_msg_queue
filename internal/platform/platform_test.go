package platform

import (
	"testing"
	"time"
)

func TestFakeClock(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)

	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	c.Advance(90 * time.Minute)
	if want := start.Add(90 * time.Minute); !c.Now().Equal(want) {
		t.Errorf("after Advance Now = %v, want %v", c.Now(), want)
	}
	c.Set(start)
	if !c.Now().Equal(start) {
		t.Errorf("after Set Now = %v, want %v", c.Now(), start)
	}
}

func TestFakeIDGenDeterministic(t *testing.T) {
	g := NewFakeIDGen("ts-")
	got := []string{g.NewID(), g.NewID(), g.NewID()}
	want := []string{"ts-1", "ts-2", "ts-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRandomIDUniqueAndLength(t *testing.T) {
	g := NewRandomID()
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := g.NewID()
		if len(id) != 32 {
			t.Fatalf("id length = %d, want 32 hex chars", len(id))
		}
		if seen[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = true
	}
}
