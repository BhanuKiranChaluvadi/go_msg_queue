package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"medconnect/internal/domain"
)

// heartbeatInterval bounds how often the SSE stream writes a keep-alive comment.
// The write also serves as the disconnect probe: once the client is gone the
// write fails and the handler returns, releasing its subscription.
const heartbeatInterval = 200 * time.Millisecond

// internalAuth guards the /internal/* endpoints with a shared bearer token so
// only the hub's own worker processes may consume them. A missing or wrong token
// yields 401.
func (s *Server) internalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.InternalToken == "" || r.Header.Get("X-Internal-Token") != s.InternalToken {
			writeJSON(w, http.StatusUnauthorized, errorBody{
				Error: errorDetail{Code: "unauthorized", Message: "invalid internal token"},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleInternalEvents streams the hub's published events to a connected worker
// as Server-Sent Events. It is the split-mode transport for the webhook
// dispatcher; in embedded mode the dispatcher subscribes to the Publisher
// directly. Delivery here is best-effort: a slow client drops events rather than
// blocking Publish, and the subscription is removed on disconnect.
func (s *Server) handleInternalEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.Publisher == nil {
		writeError(w, fmt.Errorf("events streaming unavailable"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := newChanSub(64)
	s.Publisher.Subscribe(sub)
	defer s.Publisher.Unsubscribe(sub)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Keep-alive doubles as a disconnect probe: a failed write means the
			// client is gone, so we return and Unsubscribe.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e := <-sub.ch:
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// chanSub is an events.Subscriber that buffers events onto a channel. Notify
// never blocks: when the buffer is full the event is dropped, so a slow SSE
// client cannot stall the publisher's request path.
type chanSub struct {
	ch chan domain.Event
}

func newChanSub(buf int) *chanSub {
	return &chanSub{ch: make(chan domain.Event, buf)}
}

func (c *chanSub) Notify(_ context.Context, e domain.Event) {
	select {
	case c.ch <- e:
	default:
	}
}
