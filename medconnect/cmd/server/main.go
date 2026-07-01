// Command server is the medconnect API hub. By default it runs as a single
// binary; the transcription and webhook workers run embedded (added in later
// slices), with an optional split mode selected at the composition root.
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
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("failed to listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	if err := api.Serve(ctx, ln, logger); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}
