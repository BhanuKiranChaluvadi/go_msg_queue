package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"medconnect/internal/domain"
)

func TestInternalEventsRequiresToken(t *testing.T) {
	ts := httptest.NewServer(newTestServer().Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/internal/events")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without token", resp.StatusCode)
	}
}

func TestInternalEventsStreamsPublishedEvents(t *testing.T) {
	srv := newTestServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/internal/events", nil)
	req.Header.Set("X-Internal-Token", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// The handler subscribes once it starts running; wait for that, then publish.
	waitFor(t, func() bool { return srv.Publisher.SubscriberCount() == 1 })
	srv.Publisher.Publish(context.Background(),
		domain.Event{TenantID: "A", Type: domain.EventNoteAdded, EntityRef: "appt1"})

	// Read stream lines (skipping heartbeat comments) until the data event arrives.
	data := readFirstData(t, resp.Body)
	var e domain.Event
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
	if e.TenantID != "A" || e.Type != domain.EventNoteAdded {
		t.Errorf("event = %+v, want tenant A note_added", e)
	}

	// Closing the client body disconnects; the handler must Unsubscribe.
	_ = resp.Body.Close()
	waitFor(t, func() bool { return srv.Publisher.SubscriberCount() == 0 })
}

func readFirstData(t *testing.T, body io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	t.Fatal("stream ended before a data line")
	return ""
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
