package state

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

func recoverForNewProcess(previous diskState, workerID string, now time.Time) (diskState, error) {
	input, err := proto.Marshal(&workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_WAITING})
	if err != nil {
		return diskState{}, fmt.Errorf("marshal recovered input state: %w", err)
	}
	recovered := cloneDiskState(previous)
	recovered.WorkerID = workerID
	recovered.LastEventSequence = 0
	recovered.Input = input
	recovered.Outbox = make([]diskEvent, 0)
	if len(previous.Recording) == 0 {
		return recovered, nil
	}

	recording := &workerv1.RecordingStatus{}
	if err := proto.Unmarshal(previous.Recording, recording); err != nil {
		return diskState{}, fmt.Errorf("decode recording during recovery: %w", err)
	}
	switch recording.State {
	case workerv1.RecordingState_RECORDING_STATE_STARTING,
		workerv1.RecordingState_RECORDING_STATE_RECORDING,
		workerv1.RecordingState_RECORDING_STATE_FINALIZING:
		recording.State = workerv1.RecordingState_RECORDING_STATE_ERROR
		recording.FinishedAt = timestamppb.New(now.UTC())
		recording.FinalizedFile = nil
		recording.Error = &workerv1.WorkerError{
			Code:      workerv1.ErrorCode_ERROR_CODE_RECORDING_INTERRUPTED,
			Message:   "video-worker restarted before recording finalization completed",
			Retryable: false,
		}
		encoded, err := proto.Marshal(recording)
		if err != nil {
			return diskState{}, fmt.Errorf("marshal interrupted recording state: %w", err)
		}
		recovered.Recording = encoded
	}
	return recovered, nil
}
