package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
	"github.com/traP-jp/kinugasa-recording/internal/uploader"
	s3store "github.com/traP-jp/kinugasa-recording/internal/uploader/objectstore/s3"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("video uploader stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	config, err := uploaderConfigFromEnvironment()
	if err != nil {
		return err
	}
	queue, err := uploadqueue.Open(config.SharedVolume, config.SessionID, config.CameraIdentityID)
	if err != nil {
		return fmt.Errorf("open upload queue: %w", err)
	}
	objects, err := s3store.New(ctx, config.S3)
	if err != nil {
		return err
	}
	processor, err := uploader.NewProcessor(config.SharedVolume, queue, objects, config.MaximumAttempts)
	if err != nil {
		return err
	}
	connection, err := grpc.NewClient(
		config.ConsoleAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(4<<20)),
	)
	if err != nil {
		return fmt.Errorf("create console gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	reporter, err := uploader.NewReporter(queue, upv1.NewConsoleVideoUploaderServiceClient(connection))
	if err != nil {
		return err
	}
	delay := config.PollInterval
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
		complete, err := uploadWorkComplete(queue)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
		if failed {
			delay = nextRetryDelay(delay, 30*time.Second)
		} else {
			delay = config.PollInterval
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

func uploadWorkComplete(queue *uploadqueue.Queue) (bool, error) {
	workerComplete, err := queue.WorkerComplete()
	if err != nil || !workerComplete {
		return false, err
	}
	manifests, err := queue.List()
	if err != nil {
		return false, err
	}
	for _, manifest := range manifests {
		if !manifest.Terminal() || !manifest.Reported() {
			return false, nil
		}
	}
	return true, nil
}
