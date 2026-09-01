package state

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func validateDiskState(state diskState) error {
	if state.Version != stateVersion {
		return fmt.Errorf("unsupported version %d", state.Version)
	}
	if err := workerprotocol.ValidateUUID("workerId", state.WorkerID); err != nil {
		return err
	}
	input := &workerv1.InputStatus{}
	if err := proto.Unmarshal(state.Input, input); err != nil {
		return fmt.Errorf("decode input status: %w", err)
	}
	if err := workerprotocol.ValidateInputStatus("input", input); err != nil {
		return err
	}
	if len(state.Recording) != 0 {
		recording := &workerv1.RecordingStatus{}
		if err := proto.Unmarshal(state.Recording, recording); err != nil {
			return fmt.Errorf("decode recording status: %w", err)
		}
		if err := workerprotocol.ValidateRecordingStatus("recording", recording); err != nil {
			return err
		}
	}
	var previousSequence uint64
	for _, storedEvent := range state.Outbox {
		event := &workerv1.WorkerEvent{}
		if err := proto.Unmarshal(storedEvent.Message, event); err != nil {
			return fmt.Errorf("decode outbox event: %w", err)
		}
		if err := workerprotocol.ValidateWorkerEvent(event); err != nil {
			return err
		}
		if storedEvent.EventID != event.EventId || storedEvent.Sequence != event.Sequence {
			return fmt.Errorf("outbox event index does not match its payload")
		}
		if storedEvent.Sequence <= previousSequence || storedEvent.Sequence > state.LastEventSequence {
			return fmt.Errorf("outbox sequence %d is not ordered within last sequence %d", storedEvent.Sequence, state.LastEventSequence)
		}
		previousSequence = storedEvent.Sequence
	}
	for commandID, encoded := range state.CommandResults {
		result := &workerv1.CommandResult{}
		if err := proto.Unmarshal(encoded, result); err != nil {
			return fmt.Errorf("decode command result: %w", err)
		}
		if commandID != result.CommandId {
			return fmt.Errorf("command result index does not match its payload")
		}
		if err := workerprotocol.ValidateCommandResult(result); err != nil {
			return err
		}
	}
	return nil
}
