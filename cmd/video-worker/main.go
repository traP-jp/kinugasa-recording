package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
	workercommand "github.com/traP-jp/kinugasa-recording/internal/worker/command"
	workerconfig "github.com/traP-jp/kinugasa-recording/internal/worker/config"
	"github.com/traP-jp/kinugasa-recording/internal/worker/control"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	"github.com/traP-jp/kinugasa-recording/internal/worker/recording"
	"github.com/traP-jp/kinugasa-recording/internal/worker/state"
	"github.com/traP-jp/kinugasa-recording/internal/worker/upload"
	s3store "github.com/traP-jp/kinugasa-recording/internal/worker/upload/objectstore/s3"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) == 2 && (os.Args[1] == recording.SegmentCreatedHookArgument ||
		os.Args[1] == recording.SegmentCompletedHookArgument) {
		if err := runRecordingHook(os.Args[1]); err != nil {
			logger.Error("MediaMTX recording hook failed", "error", err)
			os.Exit(1)
		}
		return
	}
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
	objects, err := s3store.New(ctx, config.S3)
	if err != nil {
		return fmt.Errorf("configure object storage: %w", err)
	}
	uploadProcessor, err := upload.NewProcessor(config.SharedVolume, uploads, objects, config.UploadMaxAttempts)
	if err != nil {
		return err
	}
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- media.RunRTPMPEGTSBridge(
			runtimeContext,
			config.RTPAddress,
			config.MPEGTSAddress,
			logger,
		)
	}()
	recordPath, err := recording.MediaMTXRecordPath(config.SharedVolume)
	if err != nil {
		return fmt.Errorf("prepare MediaMTX recording path: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve video worker executable: %w", err)
	}
	mediaServer, err := media.Start(runtimeContext, media.Config{
		BinaryPath:                 config.MediaMTXBinary,
		MPEGTSAddress:              config.MPEGTSAddress,
		RTSPAddress:                config.RTSPAddress,
		APIAddress:                 config.MediaAPIAddress,
		PathName:                   config.MediaPath,
		WHIPURL:                    config.LiveKitWHIPURL,
		WHIPToken:                  config.LiveKitWHIPToken,
		RecordPath:                 recordPath,
		RunOnRecordSegmentCreate:   recordingHookCommand(executable, recording.SegmentCreatedHookArgument),
		RunOnRecordSegmentComplete: recordingHookCommand(executable, recording.SegmentCompletedHookArgument),
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
		SharedVolume: config.SharedVolume,
		Controller:   mediaServer,
	})
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
	workerService := workerv1.NewConsoleVideoWorkerServiceClient(connection)
	controlClient, err := control.NewClient(
		workerService,
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
	uploadReporter, err := upload.NewReporter(uploads, workerService)
	if err != nil {
		cancelRuntime()
		_ = mediaServer.Wait()
		return err
	}
	go observeInput(
		runtimeContext,
		mediaServer,
		config.FFprobeBinary,
		store,
		controlClient,
		executor,
		config.InputPollInterval,
		logger,
	)
	controlDone := make(chan error, 1)
	go func() { controlDone <- controlClient.Run(runtimeContext) }()
	uploadDone := make(chan error, 1)
	go func() {
		uploadDone <- runUploads(
			runtimeContext,
			uploadProcessor,
			uploadReporter,
			config.UploadPollInterval,
			logger,
		)
	}()
	mediaDone := make(chan error, 1)
	go func() { mediaDone <- mediaServer.Wait() }()
	select {
	case err := <-controlDone:
		cancelRuntime()
		mediaError := <-mediaDone
		uploadError := <-uploadDone
		return errors.Join(err, ignoredCanceledProcess(mediaError), uploadError)
	case err := <-mediaDone:
		cancelRuntime()
		<-controlDone
		<-uploadDone
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return fmt.Errorf("MediaMTX exited unexpectedly")
		}
		return fmt.Errorf("run MediaMTX: %w", err)
	case err := <-bridgeDone:
		cancelRuntime()
		mediaError := <-mediaDone
		<-controlDone
		<-uploadDone
		if ctx.Err() != nil && err == nil {
			return ignoredCanceledProcess(mediaError)
		}
		if err == nil {
			err = fmt.Errorf("RTP/MP2T bridge exited unexpectedly")
		}
		return errors.Join(err, ignoredCanceledProcess(mediaError))
	case err := <-uploadDone:
		cancelRuntime()
		controlError := <-controlDone
		mediaError := <-mediaDone
		if ctx.Err() != nil && err == nil {
			return errors.Join(controlError, ignoredCanceledProcess(mediaError))
		}
		if err == nil {
			err = fmt.Errorf("upload loop exited unexpectedly")
		}
		return errors.Join(err, controlError, ignoredCanceledProcess(mediaError))
	case <-ctx.Done():
		cancelRuntime()
		controlError := <-controlDone
		mediaError := <-mediaDone
		uploadError := <-uploadDone
		return errors.Join(controlError, ignoredCanceledProcess(mediaError), uploadError)
	}
}

func runRecordingHook(argument string) error {
	sharedVolume := os.Getenv("KINUGASA_SHARED_VOLUME")
	if sharedVolume == "" {
		sharedVolume = "/recordings"
	}
	segmentPath := os.Getenv("MTX_SEGMENT_PATH")
	if segmentPath == "" {
		return fmt.Errorf("MTX_SEGMENT_PATH is required")
	}
	return recording.WriteSegmentHook(sharedVolume, argument, segmentPath, time.Now().UTC())
}

func recordingHookCommand(executable, argument string) string {
	return strconv.Quote(executable) + " " + argument
}

func ignoredCanceledProcess(err error) error {
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return nil
	}
	return err
}
