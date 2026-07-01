// Command server is the medconnect API hub. By default it runs as a single
// binary with the transcription and webhook workers embedded; the optional split
// mode (-embed-workers=false) runs them as separate processes against the
// internal contract. Worker wiring is added in later slices.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"medconnect/internal/api"
	"medconnect/internal/events"
	"medconnect/internal/platform"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	embedWorkers := flag.Bool("embed-workers", true, "run transcription and webhook workers in-process (false = split mode)")
	internalToken := flag.String("internal-token", os.Getenv("MEDCONNECT_INTERNAL_TOKEN"), "shared token guarding /internal/* endpoints")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	idgen := platform.NewRandomID()
	publisher := events.NewPublisher(events.NewStore(), platform.SystemClock{}, idgen)

	srv := &api.Server{
		Logger:        logger,
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: *internalToken,
	}

	logger.Info("starting hub", "embedWorkers", *embedWorkers)
	// TODO(tasks 2.3/3.3): when embedWorkers is true, start the webhook dispatcher
	// and transcription worker as goroutines here; otherwise expose the internal
	// contract for cmd/notifier and cmd/transcriber.

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("failed to listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	if err := api.Serve(ctx, ln, srv.Handler(), logger); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}
