package webhooks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fastConfig makes retries near-instant so tests stay quick.
func fastConfig(workers, maxAttempts int) Config {
	return Config{
		Workers:     workers,
		QueueSize:   16,
		MaxAttempts: maxAttempts,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
		Timeout:     2 * time.Second,
		Logger:      quietLogger(),
	}
}

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestDispatcher_DeliversWithSignatureAndHeaders(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody []byte
		gotSig  string
		gotEvt  string
		gotType string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		gotSig = r.Header.Get("X-Signature")
		gotEvt = r.Header.Get("X-Webhook-Event-Id")
		gotType = r.Header.Get("Content-Type")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(fastConfig(2, 3))
	d.Start()
	defer d.Stop(context.Background())

	payload := []byte(`{"eventId":"ev-1","eventType":"note_added"}`)
	d.Enqueue(Delivery{WebhookID: "wh-1", URL: srv.URL, Secret: "s3cr3t", EventID: "ev-1", Payload: payload})

	waitFor(t, func() bool { return d.Stats().Delivered == 1 })

	mu.Lock()
	defer mu.Unlock()
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", gotBody, payload)
	}
	if gotType != "application/json" {
		t.Errorf("content-type = %q", gotType)
	}
	if gotEvt != "ev-1" {
		t.Errorf("event id header = %q", gotEvt)
	}
	if want := Sign("s3cr3t", payload); gotSig != want {
		t.Errorf("signature = %q, want %q", gotSig, want)
	}
}

func TestDispatcher_RetriesThenSucceeds(t *testing.T) {
	var attempts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt64(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(fastConfig(1, 5))
	d.Start()
	defer d.Stop(context.Background())

	d.Enqueue(Delivery{URL: srv.URL, Secret: "x", EventID: "ev", Payload: []byte(`{}`)})
	waitFor(t, func() bool { return d.Stats().Delivered == 1 })
	if got := atomic.LoadInt64(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures then success)", got)
	}
}

func TestDispatcher_DeadLettersAfterExhaustion(t *testing.T) {
	var attempts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := NewDispatcher(fastConfig(1, 3))
	d.Start()
	defer d.Stop(context.Background())

	d.Enqueue(Delivery{URL: srv.URL, Secret: "x", EventID: "ev", Payload: []byte(`{}`)})
	waitFor(t, func() bool { return d.Stats().DeadLettered == 1 })
	if d.Stats().Delivered != 0 {
		t.Errorf("delivered = %d, want 0", d.Stats().Delivered)
	}
	if got := atomic.LoadInt64(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (MaxAttempts)", got)
	}
}

func TestDispatcher_EnqueueNonBlockingWhenFull(t *testing.T) {
	// No workers started -> nothing drains the queue.
	cfg := fastConfig(1, 3)
	cfg.QueueSize = 1
	d := NewDispatcher(cfg)

	if !d.Enqueue(Delivery{EventID: "1"}) {
		t.Fatal("first enqueue should succeed")
	}
	if d.Enqueue(Delivery{EventID: "2"}) {
		t.Error("second enqueue should be dropped (queue full)")
	}
	if d.Stats().Dropped != 1 {
		t.Errorf("dropped = %d, want 1", d.Stats().Dropped)
	}
}

func TestDispatcher_GracefulDrainOnStop(t *testing.T) {
	var received int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&received, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(fastConfig(2, 3))
	d.Start()
	for i := 0; i < 5; i++ {
		d.Enqueue(Delivery{URL: srv.URL, Secret: "x", EventID: "ev", Payload: []byte(`{}`)})
	}
	// Stop drains the queue before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d.Stop(ctx)

	if got := atomic.LoadInt64(&received); got != 5 {
		t.Errorf("received = %d, want 5 (all drained on graceful stop)", got)
	}
	// After Stop, enqueue is rejected.
	if d.Enqueue(Delivery{EventID: "late"}) {
		t.Error("enqueue after Stop should return false")
	}
}
