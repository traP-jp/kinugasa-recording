package workerprotocol

import (
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

func ValidateUUID(field, value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return invalid(field, "must be a canonical UUID")
	}
	return nil
}

func ValidateTimestamp(field string, value *timestamppb.Timestamp) error {
	if value == nil {
		return invalid(field, "must be set")
	}
	if err := value.CheckValid(); err != nil {
		return invalid(field, "must be a valid protobuf timestamp")
	}
	return nil
}

func ValidateRelativePath(field, value string) error {
	if value == "" {
		return invalid(field, "must not be empty")
	}
	if strings.Contains(value, `\`) {
		return invalid(field, "must use slash separators")
	}
	if path.IsAbs(value) || path.Clean(value) != value {
		return invalid(field, "must be a normalized relative path")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return invalid(field, "must not contain empty, dot, or parent components")
		}
	}
	return nil
}

func ValidateWorkerHello(hello *workerv1.WorkerHello) error {
	if hello == nil {
		return invalid("hello", "must be set")
	}
	if err := ValidateUUID("hello.worker_id", hello.WorkerId); err != nil {
		return err
	}
	if hello.SessionId == "" {
		return invalid("hello.session_id", "must not be empty")
	}
	if hello.CameraIdentityId == "" {
		return invalid("hello.camera_identity_id", "must not be empty")
	}
	if err := ValidateTimestamp("hello.observed_at", hello.ObservedAt); err != nil {
		return err
	}
	if hello.Snapshot == nil {
		return invalid("hello.snapshot", "must be set")
	}
	if err := ValidateInputStatus("hello.snapshot.input", hello.Snapshot.Input); err != nil {
		return err
	}
	if hello.Snapshot.Recording != nil {
		return ValidateRecordingStatus("hello.snapshot.recording", hello.Snapshot.Recording)
	}
	return nil
}

func ValidateInputStatus(field string, status *workerv1.InputStatus) error {
	if status == nil {
		return invalid(field, "must be set")
	}
	switch status.State {
	case workerv1.InputState_INPUT_STATE_WAITING, workerv1.InputState_INPUT_STATE_CONNECTED:
		if status.Error != nil {
			return invalid(field+".error", "must only be set when state is ERROR")
		}
	case workerv1.InputState_INPUT_STATE_ERROR:
		if status.Error == nil {
			return invalid(field+".error", "must be set when state is ERROR")
		}
		return ValidateWorkerError(field+".error", status.Error)
	default:
		return invalid(field+".state", "must not be UNSPECIFIED")
	}
	return nil
}

func ValidateRecordingStatus(field string, status *workerv1.RecordingStatus) error {
	if status == nil {
		return invalid(field, "must be set")
	}
	if status.TakeId == "" {
		return invalid(field+".take_id", "must not be empty")
	}
	terminal := status.State == workerv1.RecordingState_RECORDING_STATE_FINISHED ||
		status.State == workerv1.RecordingState_RECORDING_STATE_ERROR
	if terminal {
		if err := ValidateTimestamp(field+".finished_at", status.FinishedAt); err != nil {
			return err
		}
	} else if status.FinishedAt != nil {
		return invalid(field+".finished_at", "must only be set for a terminal state")
	}

	switch status.State {
	case workerv1.RecordingState_RECORDING_STATE_STARTING:
		if status.StartedAt != nil {
			return invalid(field+".started_at", "must be absent until the first frame")
		}
	case workerv1.RecordingState_RECORDING_STATE_RECORDING,
		workerv1.RecordingState_RECORDING_STATE_FINALIZING:
		if err := ValidateTimestamp(field+".started_at", status.StartedAt); err != nil {
			return err
		}
	case workerv1.RecordingState_RECORDING_STATE_FINISHED:
		if err := ValidateTimestamp(field+".started_at", status.StartedAt); err != nil {
			return err
		}
		if status.FinalizedFile == nil {
			return invalid(field+".finalized_file", "must be set when state is FINISHED")
		}
		if err := ValidateRelativePath(field+".finalized_file.relative_path", status.FinalizedFile.RelativePath); err != nil {
			return err
		}
		if status.FinalizedFile.MediaType != "video/mp4" {
			return invalid(field+".finalized_file.media_type", "must be video/mp4")
		}
	case workerv1.RecordingState_RECORDING_STATE_ERROR:
		if status.Error == nil {
			return invalid(field+".error", "must be set when state is ERROR")
		}
		if err := ValidateWorkerError(field+".error", status.Error); err != nil {
			return err
		}
	default:
		return invalid(field+".state", "must not be UNSPECIFIED")
	}
	if status.State != workerv1.RecordingState_RECORDING_STATE_FINISHED && status.FinalizedFile != nil {
		return invalid(field+".finalized_file", "must only be set when state is FINISHED")
	}
	if status.State != workerv1.RecordingState_RECORDING_STATE_ERROR && status.Error != nil {
		return invalid(field+".error", "must only be set when state is ERROR")
	}
	return nil
}

func ValidateWorkerError(field string, workerError *workerv1.WorkerError) error {
	if workerError == nil {
		return invalid(field, "must be set")
	}
	if workerError.Code == workerv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		return invalid(field+".code", "must not be UNSPECIFIED")
	}
	if strings.TrimSpace(workerError.Message) == "" {
		return invalid(field+".message", "must not be empty")
	}
	return nil
}
