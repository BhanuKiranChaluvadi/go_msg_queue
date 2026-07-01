package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// IDGen generates opaque unique identifiers. Production uses RandomID; tests use
// FakeIDGen for predictable, ordered ids.
type IDGen interface {
	NewID() string
}

// RandomID generates cryptographically-random hex identifiers.
type RandomID struct{}

// NewRandomID returns a RandomID generator.
func NewRandomID() RandomID { return RandomID{} }

// NewID returns a new 128-bit random id as a hex string.
func (RandomID) NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails on supported platforms; panic is the
		// correct response to a broken entropy source.
		panic(fmt.Sprintf("platform: entropy source failed: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// FakeIDGen returns deterministic ids ("<prefix>1", "<prefix>2", ...) for tests.
type FakeIDGen struct {
	mu     sync.Mutex
	n      int
	prefix string
}

// NewFakeIDGen returns a FakeIDGen whose ids start with prefix.
func NewFakeIDGen(prefix string) *FakeIDGen { return &FakeIDGen{prefix: prefix} }

// NewID returns the next sequential id.
func (f *FakeIDGen) NewID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return fmt.Sprintf("%s%d", f.prefix, f.n)
}
