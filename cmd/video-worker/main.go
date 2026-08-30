package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/gateway"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
	workercommand "github.com/traP-jp/kinugasa-recording/internal/worker/command"
	workerconfig "github.com/traP-jp/kinugasa-recording/internal/worker/config"
	"github.com/traP-jp/kinugasa-recording/internal/worker/control"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	workerpreview "github.com/traP-jp/kinugasa-recording/internal/worker/preview"
	"github.com/traP-jp/kinugasa-recording/internal/worker/recording"
	"github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("video worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	config, err := workerconfig.FromEnvironment()
	if err != nil {
		return err
	}
	workerID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate worker ID: %w", err)
	}
	store, err := state.Open(config.SharedVolume, workerID.String(), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("open worker state: %w", err)
	}
	uploads, err := uploadqueue.Open(config.SharedVolume, config.SessionID, config.CameraIdentityID)
	if err != nil {
		return fmt.Errorf("open upload queue: %w", err)
	}
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	mediaServer, err := media.Start(runtimeContext, media.Config{
		BinaryPath:  config.MediaMTXBinary,
		RTPAddress:  config.RTPAddress,
		RTSPAddress: config.RTSPAddress,
		APIAddress:  config.MediaAPIAddress,
		PathName:    config.MediaPath,
		RTPSDP:      config.RTPSDP,
	}, logger)
	if err != nil {
		return err
	}
	readyContext, cancelReady := context.WithTimeout(runtimeContext, 10*time.Second)
	err = mediaServer.WaitReady(readyContext)
	cancelReady()
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	recorder, err := recording.NewRecorder(recording.Config{
		SharedVolume:   config.SharedVolume,
		FFmpegPath:     config.FFmpegBinary,
		InputArguments: []string{"-rtsp_transport", "tcp", "-i", mediaServer.RTSPURL()},
	})
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	previewRelay, err := workerpreview.NewRelay(workerpreview.Config{
		FFmpegPath:     config.FFmpegBinary,
		InputArguments: []string{"-rtsp_transport", "tcp", "-i", mediaServer.RTSPURL()},
		WHIPURL:        config.LiveKitWHIPURL,
		BearerToken:    config.LiveKitWHIPToken,
	}, logger)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	executor, err := workercommand.NewExecutor(store, recorder, uploads)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	gatewayClient, err := gateway.NewClient(config.GatewayStatusURL)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	connection, err := grpc.NewClient(
		config.ConsoleAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(4<<20)),
	)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return fmt.Errorf("create console gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	controlClient, err := control.NewClient(
		workerv1.NewConsoleVideoWorkerServiceClient(connection),
		store,
		executor,
		control.Config{SessionID: config.SessionID, CameraIdentityID: config.CameraIdentityID},
		logger,
	)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	go observeInput(runtimeContext, gatewayClient, mediaServer, store, controlClient, executor, config.InputPollInterval, logger)
	controlDone := make(chan error, 1)
	go func() { controlDone <- controlClient.Run(runtimeContext) }()
	mediaDone := make(chan error, 1)
	go func() { mediaDone <- mediaServer.Wait() }()
	previewDone := make(chan error, 1)
	go func() { previewDone <- previewRelay.Run(runtimeContext) }()
	select {
	case err := <-controlDone:
		cancelRuntime()
		mediaError := <-mediaDone
		previewError := <-previewDone
		return errors.Join(err, ignoredCanceledProcess(mediaError), previewError)
	case err := <-mediaDone:
		cancelRuntime()
		<-controlDone
		<-previewDone
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return fmt.Errorf("MediaMTX exited unexpectedly")
		}
		return fmt.Errorf("run MediaMTX: %w", err)
	case err := <-previewDone:
		cancelRuntime()
		controlError := <-controlDone
		mediaError := <-mediaDone
		if ctx.Err() != nil {
			return errors.Join(controlError, ignoredCanceledProcess(mediaError))
		}
		if err == nil {
			err = fmt.Errorf("preview relay exited unexpectedly")
		}
		return errors.Join(err, controlError, ignoredCanceledProcess(mediaError))
	case <-ctx.Done():
		cancelRuntime()
		controlError := <-controlDone
		mediaError := <-mediaDone
		previewError := <-previewDone
		return errors.Join(controlError, ignoredCanceledProcess(mediaError), previewError)
	}
}

func ignoredCanceledProcess(err error) error {
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return nil
	}
	return err
}
