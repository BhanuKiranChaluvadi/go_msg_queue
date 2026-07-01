package protocol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSSEClientStreamsDataLines(t *testing.T) {
	// Server emits the brief's example events plus a comment and blank line.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": a comment line\n")
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, `data: {"appointmentId":"123","sequence":0,"text":"hello","isFinal":false}`+"\n\n")
		fmt.Fprint(w, `data: {"appointmentId":"123","sequence":1,"text":"world","isFinal":true}`+"\n\n")
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	var got []string
	client := &SSEClient{}
	err := client.Stream(context.Background(), req, func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d data payloads, want 2: %v", len(got), got)
	}
	if got[0] != `{"appointmentId":"123","sequence":0,"text":"hello","isFinal":false}` {
		t.Errorf("payload[0] = %s", got[0])
	}
}

func TestSSEClientNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	err := (&SSEClient{}).Stream(context.Background(), req, func([]byte) error { return nil })
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestSSEClientStopsOnCallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: %d\n\n", i)
		}
	}))
	defer srv.Close()

	stopErr := fmt.Errorf("stop")
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	calls := 0
	err := (&SSEClient{HTTPClient: &http.Client{Timeout: 2 * time.Second}}).
		Stream(context.Background(), req, func([]byte) error {
			calls++
			return stopErr
		})
	if err != stopErr {
		t.Fatalf("err = %v, want stopErr", err)
	}
	if calls != 1 {
		t.Errorf("callback called %d times, want 1 (stops on first error)", calls)
	}
}
