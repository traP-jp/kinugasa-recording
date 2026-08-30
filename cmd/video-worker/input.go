package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/gateway"
	workercommand "github.com/traP-jp/kinugasa-recording/internal/worker/command"
	"github.com/traP-jp/kinugasa-recording/internal/worker/control"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	"github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

func observeInput(
	ctx context.Context,
	gatewayClient interface {
		Status(context.Context) (gateway.Status, error)
	},
	server *media.Server,
	store *state.Store,
	client *control.Client,
	executor *workercommand.Executor,
	interval time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_UNSPECIFIED}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		gatewayStatus, err := gatewayClient.Status(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("query video gateway input state", "error", err)
			}
			continue
		}
		online, err := server.Online(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("query MediaMTX input state", "error", err)
			}
			continue
		}
		inputStatus := workerInputStatus(gatewayStatus, online)
		if proto.Equal(inputStatus, previous) {
			continue
		}
		eventID, err := uuid.NewV7()
		if err != nil {
			logger.Error("generate input event ID", "error", err)
			continue
		}
		if _, err := store.AppendEvent(&workerv1.WorkerEvent{
			EventId:    eventID.String(),
			OccurredAt: timestamppb.Now(),
			Event: &workerv1.WorkerEvent_InputStatusChanged{
				InputStatusChanged: inputStatus,
			},
		}); err != nil {
			logger.Error("persist input state event", "error", err)
			continue
		}
		previous = proto.Clone(inputStatus).(*workerv1.InputStatus)
		if inputStatus.State != workerv1.InputState_INPUT_STATE_CONNECTED {
			if err := executor.InputDisconnected(); err != nil {
				logger.Error("persist recording input disconnection", "error", err)
			}
		}
		client.NotifyEvents()
	}
}

func workerInputStatus(status gateway.Status, mediaOnline bool) *workerv1.InputStatus {
	if status.State == gateway.StateError {
		return &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_ERROR,
			Error: &workerv1.WorkerError{Code: gatewayWorkerErrorCode(status.Code), Message: status.Error},
		}
	}
	if status.State == gateway.StateConnected && mediaOnline {
		return &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_CONNECTED}
	}
	return &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_WAITING}
}

func gatewayWorkerErrorCode(code gateway.ErrorCode) workerv1.ErrorCode {
	switch code {
	case gateway.ErrorCodeUnsupportedCodec:
		return workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VIDEO_CODEC
	case gateway.ErrorCodeUnsupportedFPS:
		return workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_FRAME_RATE
	case gateway.ErrorCodeMediaPipeline:
		return workerv1.ErrorCode_ERROR_CODE_MEDIA_PIPELINE_FAILURE
	default:
		return workerv1.ErrorCode_ERROR_CODE_INPUT_UNAVAILABLE
	}
}
