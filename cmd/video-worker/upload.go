package main

import (
	"context"
	"log/slog"
	"time"
)

type uploadProcessor interface {
	ProcessPending(context.Context) (int, error)
}

type uploadReporter interface {
	ReportPending(context.Context) (int, error)
}

func runUploads(
	ctx context.Context,
	processor uploadProcessor,
	reporter uploadReporter,
	pollInterval time.Duration,
	logger *slog.Logger,
) error {
	delay := pollInterval
	for {
		if ctx.Err() != nil {
			return nil
		}
		failed := false
		if _, err := processor.ProcessPending(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("process pending recording uploads", "error", err)
			failed = true
		}
		if _, err := reporter.ReportPending(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("report terminal recording uploads", "error", err)
			failed = true
		}
		if failed {
			delay = nextRetryDelay(delay, 30*time.Second)
		} else {
			delay = pollInterval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func nextRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}
