package workerprotocol

import (
	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

func ValidateWorkerMessage(message *workerv1.WorkerMessage) error {
	if message == nil {
		return invalid("worker_message", "must be set")
	}
	switch payload := message.Payload.(type) {
	case *workerv1.WorkerMessage_Hello:
		return ValidateWorkerHello(payload.Hello)
	case *workerv1.WorkerMessage_Event:
		return ValidateWorkerEvent(payload.Event)
	case *workerv1.WorkerMessage_CommandResult:
		return ValidateCommandResult(payload.CommandResult)
	default:
		return invalid("worker_message.payload", "must contain exactly one payload")
	}
}

func ValidateWorkerEvent(event *workerv1.WorkerEvent) error {
	if event == nil {
		return invalid("event", "must be set")
	}
	if err := ValidateUUID("event.event_id", event.EventId); err != nil {
		return err
	}
	if err := ValidateTimestamp("event.occurred_at", event.OccurredAt); err != nil {
		return err
	}
	if event.Sequence == 0 {
		return invalid("event.sequence", "must be greater than zero")
	}
	switch payload := event.Event.(type) {
	case *workerv1.WorkerEvent_InputStatusChanged:
		return ValidateInputStatus("event.input_status_changed", payload.InputStatusChanged)
	case *workerv1.WorkerEvent_RecordingStatusChanged:
		return ValidateRecordingStatus("event.recording_status_changed", payload.RecordingStatusChanged)
	default:
		return invalid("event.event", "must contain exactly one event")
	}
}

func ValidateCommandResult(result *workerv1.CommandResult) error {
	if result == nil {
		return invalid("command_result", "must be set")
	}
	if err := ValidateUUID("command_result.command_id", result.CommandId); err != nil {
		return err
	}
	if err := ValidateTimestamp("command_result.completed_at", result.CompletedAt); err != nil {
		return err
	}
	requiresError := result.Status == workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED ||
		result.Status == workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED
	switch result.Status {
	case workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED,
		workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED,
		workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED,
		workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED:
	default:
		return invalid("command_result.status", "must not be UNSPECIFIED")
	}
	if requiresError {
		if result.Error == nil {
			return invalid("command_result.error", "must be set for REJECTED or FAILED")
		}
		return ValidateWorkerError("command_result.error", result.Error)
	}
	if result.Error != nil {
		return invalid("command_result.error", "must only be set for REJECTED or FAILED")
	}
	return nil
}

func ValidateConsoleMessage(message *workerv1.ConsoleMessage) error {
	if message == nil {
		return invalid("console_message", "must be set")
	}
	switch payload := message.Payload.(type) {
	case *workerv1.ConsoleMessage_Registered:
		if payload.Registered == nil {
			return invalid("registered", "must be set")
		}
		if err := ValidateUUID("registered.worker_id", payload.Registered.WorkerId); err != nil {
			return err
		}
		return ValidateTimestamp("registered.registered_at", payload.Registered.RegisteredAt)
	case *workerv1.ConsoleMessage_EventsAcknowledged:
		if payload.EventsAcknowledged == nil {
			return invalid("events_acknowledged", "must be set")
		}
		for _, eventID := range payload.EventsAcknowledged.EventIds {
			if err := ValidateUUID("events_acknowledged.event_ids", eventID); err != nil {
				return err
			}
		}
		return nil
	case *workerv1.ConsoleMessage_Command:
		return ValidateWorkerCommand(payload.Command)
	default:
		return invalid("console_message.payload", "must contain exactly one payload")
	}
}

func ValidateWorkerCommand(command *workerv1.WorkerCommand) error {
	if command == nil {
		return invalid("command", "must be set")
	}
	if err := ValidateUUID("command.command_id", command.CommandId); err != nil {
		return err
	}
	if err := ValidateTimestamp("command.issued_at", command.IssuedAt); err != nil {
		return err
	}
	switch payload := command.Command.(type) {
	case *workerv1.WorkerCommand_StartRecording:
		if payload.StartRecording == nil || payload.StartRecording.TakeId == "" {
			return invalid("command.start_recording.take_id", "must not be empty")
		}
		return ValidateRelativePath("command.start_recording.relative_path", payload.StartRecording.RelativePath)
	case *workerv1.WorkerCommand_FinishRecording:
		if payload.FinishRecording == nil || payload.FinishRecording.TakeId == "" {
			return invalid("command.finish_recording.take_id", "must not be empty")
		}
		return nil
	case *workerv1.WorkerCommand_Shutdown:
		if payload.Shutdown == nil {
			return invalid("command.shutdown", "must be set")
		}
		return nil
	default:
		return invalid("command.command", "must contain exactly one command")
	}
}
