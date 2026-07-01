package protocol

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
)

// maxSSELine caps a single SSE line to guard against a hostile or broken server
// forcing an unbounded buffer allocation.
const maxSSELine = 1 << 20 // 1 MiB

// SSEClient consumes a text/event-stream. It yields the payload of each `data:`
// line to onData. The same client consumes the external transcription stream and
// the hub's internal event stream, so both workers share one implementation.
type SSEClient struct {
	// HTTPClient is used for the request; nil means http.DefaultClient.
	HTTPClient *http.Client
}

// Stream issues req (with ctx applied), then reads the response as SSE, calling
// onData for each data payload until the context is cancelled, the stream ends,
// or onData returns an error. A non-2xx response is an error.
func (c *SSEClient) Stream(ctx context.Context, req *http.Request, onData func([]byte) error) error {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("protocol: unexpected status %d", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // ignore comments, blank lines, and other SSE fields
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if err := onData([]byte(payload)); err != nil {
			return err
		}
	}
	return sc.Err()
}
