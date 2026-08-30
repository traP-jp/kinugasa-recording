package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/worker/control"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	"github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

func observeInput(
	ctx context.Context,
	server *media.Server,
	store *state.Store,
	client *control.Client,
	interval time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previousOnline := false
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
		if online == previousOnline {
			continue
		}
		inputState := workerv1.InputState_INPUT_STATE_WAITING
		if online {
			inputState = workerv1.InputState_INPUT_STATE_CONNECTED
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
				InputStatusChanged: &workerv1.InputStatus{State: inputState},
			},
		}); err != nil {
			logger.Error("persist input state event", "error", err)
			continue
		}
		previousOnline = online
		client.NotifyEvents()
	}
}
