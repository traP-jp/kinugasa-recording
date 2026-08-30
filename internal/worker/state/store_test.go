package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

const (
	workerID1  = "019c240d-a6de-7de0-a826-0f26e8803fc0"
	workerID2  = "019c240e-3eb4-72d6-a6fa-adfe1df795c8"
	eventID1   = "019c240e-4a04-73e3-8328-a32a246b8c47"
	eventID2   = "019c240e-5141-75e4-8b4b-5c611e7fab65"
	commandID1 = "019c240e-5a60-7770-afad-af8697c0b37a"
)

var stateTestTime = time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)

func TestOpenCreatesDurableWaitingSnapshot(t *testing.T) {
	sharedVolume := t.TempDir()
	store, err := Open(sharedVolume, workerID1, stateTestTime)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	hello, err := store.Hello("session-id", "camera-id", stateTestTime)
	if err != nil {
		t.Fatalf("Hello() error = %v", err)
	}
	if hello.WorkerId != workerID1 || hello.Snapshot.Input.State != workerv1.InputState_INPUT_STATE_WAITING {
		t.Fatalf("Hello() = %+v", hello)
	}
	if hello.LastEventSequence != 0 || hello.Snapshot.Recording != nil {
		t.Fatalf("initial snapshot = %+v", hello.Snapshot)
	}

	reopened, err := Open(sharedVolume, workerID1, stateTestTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("Open(existing process) error = %v", err)
	}
	if reopened.WorkerID() != workerID1 {
		t.Fatalf("WorkerID() = %q, want %q", reopened.WorkerID(), workerID1)
	}
	info, err := os.Stat(filepath.Join(sharedVolume, metadataDirName, stateFileName))
	if err != nil {
		t.Fatalf("stat state file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestEventOutboxAndAcknowledgement(t *testing.T) {
	store, err := Open(t.TempDir(), workerID1, stateTestTime)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	connected, err := store.AppendEvent(&workerv1.WorkerEvent{
		EventId:    eventID1,
		OccurredAt: timestamppb.New(stateTestTime),
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_CONNECTED,
		}},
	})
	if err != nil {
		t.Fatalf("AppendEvent(input) error = %v", err)
	}
	finishedStatus := &workerv1.RecordingStatus{
		TakeId:     "take-id",
		State:      workerv1.RecordingState_RECORDING_STATE_FINISHED,
		StartedAt:  timestamppb.New(stateTestTime),
		FinishedAt: timestamppb.New(stateTestTime.Add(time.Minute)),
		FinalizedFile: &workerv1.FinalizedFile{
			RelativePath: "recordings/take-id/video.mp4",
			MediaType:    "video/mp4",
		},
	}
	finished, err := store.AppendEvent(&workerv1.WorkerEvent{
		EventId:    eventID2,
		OccurredAt: timestamppb.New(stateTestTime.Add(time.Minute)),
		Event: &workerv1.WorkerEvent_RecordingStatusChanged{
			RecordingStatusChanged: finishedStatus,
		},
	})
	if err != nil {
		t.Fatalf("AppendEvent(recording) error = %v", err)
	}
	if connected.Sequence != 1 || finished.Sequence != 2 {
		t.Fatalf("event sequences = %d, %d; want 1, 2", connected.Sequence, finished.Sequence)
	}
	pending, err := store.PendingEvents()
	if err != nil {
		t.Fatalf("PendingEvents() error = %v", err)
	}
	if len(pending) != 2 || pending[0].EventId != eventID1 || pending[1].EventId != eventID2 {
		t.Fatalf("PendingEvents() = %+v", pending)
	}

	if err := store.AcknowledgeEvents([]string{eventID1}); err != nil {
		t.Fatalf("AcknowledgeEvents(input) error = %v", err)
	}
	hello, err := store.Hello("session-id", "camera-id", stateTestTime)
	if err != nil {
		t.Fatalf("Hello() error = %v", err)
	}
	if hello.Snapshot.Recording == nil || !proto.Equal(hello.Snapshot.Recording, finishedStatus) {
		t.Fatalf("terminal recording was cleared before its event acknowledgement")
	}
	if err := store.AcknowledgeEvents([]string{eventID2}); err != nil {
		t.Fatalf("AcknowledgeEvents(recording) error = %v", err)
	}
	hello, err = store.Hello("session-id", "camera-id", stateTestTime)
	if err != nil {
		t.Fatalf("Hello() after acknowledgement error = %v", err)
	}
	if hello.Snapshot.Recording != nil {
		t.Fatalf("terminal recording remains after acknowledgement: %+v", hello.Snapshot.Recording)
	}
	if hello.LastEventSequence != 2 {
		t.Fatalf("last event sequence = %d, want 2", hello.LastEventSequence)
	}
}

func TestNewProcessRecoversInterruptedRecording(t *testing.T) {
	sharedVolume := t.TempDir()
	store, err := Open(sharedVolume, workerID1, stateTestTime)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	recording := &workerv1.RecordingStatus{
		TakeId:    "take-id",
		State:     workerv1.RecordingState_RECORDING_STATE_RECORDING,
		StartedAt: timestamppb.New(stateTestTime),
	}
	if err := store.SetRecordingStatus(recording); err != nil {
		t.Fatalf("SetRecordingStatus() error = %v", err)
	}
	if _, err := store.AppendEvent(&workerv1.WorkerEvent{
		EventId:    eventID1,
		OccurredAt: timestamppb.New(stateTestTime),
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_CONNECTED,
		}},
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	result := &workerv1.CommandResult{
		CommandId:   commandID1,
		Status:      workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED,
		CompletedAt: timestamppb.New(stateTestTime),
	}
	if err := store.SaveCommandResult(result); err != nil {
		t.Fatalf("SaveCommandResult() error = %v", err)
	}

	recovered, err := Open(sharedVolume, workerID2, stateTestTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("Open(new process) error = %v", err)
	}
	hello, err := recovered.Hello("session-id", "camera-id", stateTestTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("Hello() error = %v", err)
	}
	if hello.WorkerId != workerID2 || hello.LastEventSequence != 0 {
		t.Fatalf("recovered hello worker/sequence = %q/%d", hello.WorkerId, hello.LastEventSequence)
	}
	if hello.Snapshot.Input.State != workerv1.InputState_INPUT_STATE_WAITING {
		t.Fatalf("recovered input = %s, want WAITING", hello.Snapshot.Input.State)
	}
	if hello.Snapshot.Recording == nil ||
		hello.Snapshot.Recording.State != workerv1.RecordingState_RECORDING_STATE_ERROR ||
		hello.Snapshot.Recording.Error.Code != workerv1.ErrorCode_ERROR_CODE_RECORDING_INTERRUPTED {
		t.Fatalf("recovered recording = %+v", hello.Snapshot.Recording)
	}
	pending, err := recovered.PendingEvents()
	if err != nil {
		t.Fatalf("PendingEvents() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("new process retained old outbox = %+v", pending)
	}
	storedResult, found, err := recovered.CommandResult(commandID1)
	if err != nil || !found || !proto.Equal(storedResult, result) {
		t.Fatalf("CommandResult() = %+v, %v, %v", storedResult, found, err)
	}
}

func TestCommandResultIsImmutable(t *testing.T) {
	store, err := Open(t.TempDir(), workerID1, stateTestTime)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	result := &workerv1.CommandResult{
		CommandId:   commandID1,
		Status:      workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED,
		CompletedAt: timestamppb.New(stateTestTime),
	}
	if err := store.SaveCommandResult(result); err != nil {
		t.Fatalf("SaveCommandResult() error = %v", err)
	}
	if err := store.SaveCommandResult(proto.Clone(result).(*workerv1.CommandResult)); err != nil {
		t.Fatalf("SaveCommandResult(idempotent) error = %v", err)
	}
	result.Status = workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED
	if err := store.SaveCommandResult(result); err == nil {
		t.Fatal("SaveCommandResult(different result) error = nil")
	}
}
