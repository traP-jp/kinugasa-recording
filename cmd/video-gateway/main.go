package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/traP-jp/kinugasa-recording/internal/gateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := gateway.Run(ctx, configFromEnvironment(), logger); err != nil {
		logger.Error("video gateway stopped", "error", err)
		os.Exit(1)
	}
}
