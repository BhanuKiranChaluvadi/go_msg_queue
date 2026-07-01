package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Delivery is one webhook POST to make: the target, the secret used to sign it,
// and the already-marshalled payload.
type Delivery struct {
	WebhookID string
	URL       string
	Secret    string
	EventID   string
	Payload   []byte
}

// Config tunes the Dispatcher. Zero fields fall back to safe defaults.
type Config struct {
	Workers     int           // concurrent delivery workers
	QueueSize   int           // bounded queue capacity
	MaxAttempts int           // total attempts per delivery before dead-lettering
	BaseBackoff time.Duration // first retry delay; doubles each attempt
	MaxBackoff  time.Duration // cap on the (pre-jitter) backoff
	Timeout     time.Duration // per-attempt HTTP timeout
	Client      *http.Client
	Logger      *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.Workers <= 0 {
		c.Workers = 4
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 1024
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = 100 * time.Millisecond
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Second
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: c.Timeout}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Stats is a snapshot of the dispatcher's delivery counters.
type Stats struct {
	Delivered    int64
	DeadLettered int64
	Dropped      int64
}

// Dispatcher delivers webhook events asynchronously via a bounded queue and a
// worker pool. Enqueue never blocks the caller: when the queue is full the
// delivery is dropped (and counted), so a slow subscriber can never stall the
// request path. Failed deliveries are retried with exponential backoff and full
// jitter, then dead-lettered.
type Dispatcher struct {
	cfg    Config
	queue  chan Delivery
	stopCh chan struct{}
	wg     sync.WaitGroup

	mu        sync.Mutex
	accepting bool
	started   bool

	delivered    atomic.Int64
	deadLettered atomic.Int64
	dropped      atomic.Int64
}

// NewDispatcher builds a Dispatcher. Call Start to launch its workers.
func NewDispatcher(cfg Config) *Dispatcher {
	cfg.applyDefaults()
	return &Dispatcher{
		cfg:       cfg,
		queue:     make(chan Delivery, cfg.QueueSize),
		stopCh:    make(chan struct{}),
		accepting: true,
	}
}

// Start launches the worker pool. It is idempotent.
func (d *Dispatcher) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		return
	}
	d.started = true
	for i := 0; i < d.cfg.Workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
}

// Enqueue queues a delivery without blocking. It returns false if the dispatcher
// is stopping or the queue is full (in which case the delivery is dropped).
func (d *Dispatcher) Enqueue(del Delivery) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.accepting {
		return false
	}
	select {
	case d.queue <- del:
		return true
	default:
		d.dropped.Add(1)
		d.cfg.Logger.Warn("webhook queue full, dropping delivery", "eventId", del.EventID, "webhookId", del.WebhookID)
		return false
	}
}

// Stop stops accepting new deliveries, drains the queue, and waits for workers to
// finish (bounded by ctx). Retry backoffs in flight are aborted so shutdown is
// prompt; an aborted delivery is dead-lettered.
func (d *Dispatcher) Stop(ctx context.Context) {
	d.mu.Lock()
	if !d.accepting {
		d.mu.Unlock()
		return
	}
	d.accepting = false
	close(d.queue)
	close(d.stopCh)
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Stats returns a snapshot of the delivery counters.
func (d *Dispatcher) Stats() Stats {
	return Stats{
		Delivered:    d.delivered.Load(),
		DeadLettered: d.deadLettered.Load(),
		Dropped:      d.dropped.Load(),
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for del := range d.queue {
		d.deliver(del)
	}
}

func (d *Dispatcher) deliver(del Delivery) {
	for attempt := 1; ; attempt++ {
		if err := d.attempt(del); err == nil {
			d.delivered.Add(1)
			return
		} else if attempt >= d.cfg.MaxAttempts {
			d.deadLetter(del, err)
			return
		} else {
			select {
			case <-time.After(d.backoff(attempt)):
			case <-d.stopCh:
				d.deadLetter(del, fmt.Errorf("aborted on shutdown: %w", err))
				return
			}
		}
	}
}

func (d *Dispatcher) attempt(del Delivery) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, del.URL, bytes.NewReader(del.Payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event-Id", del.EventID)
	req.Header.Set("X-Signature", Sign(del.Secret, del.Payload))

	resp, err := d.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused

	if resp.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("non-2xx status %d", resp.StatusCode)
}

// backoff returns an exponentially increasing, capped delay with full jitter, so
// retrying subscribers don't synchronize into a thundering herd.
func (d *Dispatcher) backoff(attempt int) time.Duration {
	exp := float64(d.cfg.BaseBackoff) * math.Pow(2, float64(attempt-1))
	capped := math.Min(exp, float64(d.cfg.MaxBackoff))
	return time.Duration(rand.Float64() * capped)
}

func (d *Dispatcher) deadLetter(del Delivery, err error) {
	d.deadLettered.Add(1)
	d.cfg.Logger.Error("webhook delivery dead-lettered",
		"webhookId", del.WebhookID, "eventId", del.EventID, "url", del.URL, "err", err)
}

// Sign returns the HMAC-SHA256 signature header value for a webhook body, so
// subscribers can verify authenticity using their shared secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
