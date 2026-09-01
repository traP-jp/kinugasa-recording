package workerprotocol

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

const testUUID = "019c240d-a6de-7de0-a826-0f26e8803fc0"

func TestValidateRelativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path  string
		valid bool
	}{
		{path: "recordings/take-1/video.mp4", valid: true},
		{path: "video.mp4", valid: true},
		{path: "", valid: false},
		{path: "/video.mp4", valid: false},
		{path: "recordings/../video.mp4", valid: false},
		{path: "recordings/./video.mp4", valid: false},
		{path: "recordings//video.mp4", valid: false},
		{path: `recordings\video.mp4`, valid: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			err := ValidateRelativePath("path", test.path)
			if (err == nil) != test.valid {
				t.Fatalf("ValidateRelativePath(%q) error = %v, valid = %v", test.path, err, test.valid)
			}
		})
	}
}

func TestValidateWorkerHello(t *testing.T) {
	t.Parallel()

	hello := &workerv1.WorkerHello{
		WorkerId:         testUUID,
		SessionId:        "session-id",
		CameraIdentityId: "camera-id",
		ObservedAt:       timestamppb.New(time.Now()),
		Snapshot: &workerv1.WorkerSnapshot{Input: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_WAITING,
		}},
	}
	if err := ValidateWorkerHello(hello); err != nil {
		t.Fatalf("ValidateWorkerHello() error = %v", err)
	}
	hello.Snapshot.Input.Error = workerError()
	if err := ValidateWorkerHello(hello); err == nil {
		t.Fatal("ValidateWorkerHello() error = nil for WAITING with error")
	}
}

func TestValidateRecordingStatus(t *testing.T) {
	t.Parallel()

	now := timestamppb.New(time.Now())
	finished := &workerv1.RecordingStatus{
		TakeId:     "take-id",
		State:      workerv1.RecordingState_RECORDING_STATE_FINISHED,
		StartedAt:  now,
		FinishedAt: now,
		FinalizedFile: &workerv1.FinalizedFile{
			RelativePath: "recordings/take-id/video.mp4",
			MediaType:    "video/mp4",
		},
	}
	if err := ValidateRecordingStatus("recording", finished); err != nil {
		t.Fatalf("ValidateRecordingStatus() error = %v", err)
	}
	finished.Error = workerError()
	if err := ValidateRecordingStatus("recording", finished); err == nil {
		t.Fatal("ValidateRecordingStatus() error = nil for FINISHED with error")
	}
}

func TestValidateCommandResultErrorInvariant(t *testing.T) {
	t.Parallel()

	result := &workerv1.CommandResult{
		CommandId:   testUUID,
		Status:      workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED,
		CompletedAt: timestamppb.Now(),
	}
	if err := ValidateCommandResult(result); err == nil {
		t.Fatal("ValidateCommandResult() error = nil for REJECTED without error")
	}
	result.Error = workerError()
	if err := ValidateCommandResult(result); err != nil {
		t.Fatalf("ValidateCommandResult() error = %v", err)
	}
}

func workerError() *workerv1.WorkerError {
	return &workerv1.WorkerError{
		Code:    workerv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: "test failure",
	}
}
