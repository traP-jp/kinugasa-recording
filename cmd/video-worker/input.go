package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	workercommand "github.com/traP-jp/kinugasa-recording/internal/worker/command"
	"github.com/traP-jp/kinugasa-recording/internal/worker/control"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	"github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

func observeInput(
	ctx context.Context,
	server *media.Server,
	ffprobeBinary string,
	store *state.Store,
	client *control.Client,
	executor *workercommand.Executor,
	interval time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_UNSPECIFIED}
	var validated *workerv1.InputStatus
	var nextProbe time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		online, err := server.Online(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("query MediaMTX input state", "error", err)
			}
			continue
		}
		var inputStatus *workerv1.InputStatus
		if !online {
			validated = nil
			nextProbe = time.Time{}
			inputStatus = &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_WAITING}
		} else {
			if validated == nil || (validated.State == workerv1.InputState_INPUT_STATE_ERROR && !time.Now().Before(nextProbe)) {
				probeContext, cancelProbe := context.WithTimeout(ctx, 10*time.Second)
				validated = probeMedia(probeContext, ffprobeBinary, server.RTSPURL())
				cancelProbe()
				if validated == nil {
					continue
				}
				nextProbe = time.Now().Add(2 * time.Second)
			}
			inputStatus = proto.Clone(validated).(*workerv1.InputStatus)
		}
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
