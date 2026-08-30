package command

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/worker/recording"
	workerstate "github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

const (
	executorWorkerID   = "019c27ca-9e8e-7d9d-aeb0-67d8f5ced7c3"
	executorCommandID1 = "019c27ca-a889-77c4-8347-5ce515e73090"
	executorCommandID2 = "019c27ca-b0e7-7afa-88ec-d2333994d17c"
	executorCommandID3 = "019c27ca-b9a2-7a41-b217-66a7769f3544"
	executorCommandID4 = "019c27ca-c1df-797a-b1ac-52b06ff9ef84"
	executorCommandID5 = "019c27ca-ca5a-761c-b033-a357158aa027"
	executorCommandID6 = "019c27ca-cea0-78b4-b59a-ec06963e475b"
	executorEventID1   = "019c27ca-d246-7fd8-b213-93c73825e97c"
	executorEventID2   = "019c27ca-da9a-7910-9fc8-d00095d83475"
)

var executorTestTime = time.Date(2026, 8, 31, 6, 0, 0, 0, time.UTC)

func TestExecutorRunsRecordingStateMachineIdempotently(t *testing.T) {
	store := newExecutorState(t)
	recorder := &recorderStub{
		startedAt: executorTestTime,
		finalized: recording.FinalizedRecording{
			RelativePath: "recordings/take-id/video.mp4",
			StartedAt:    executorTestTime,
			FinishedAt:   executorTestTime.Add(time.Minute),
		},
	}
	executor := newTestExecutor(t, store, recorder)

	result, err := executor.Execute(context.Background(), startRecordingCommand(executorCommandID1, "take-id"))
	if err != nil || result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED {
		t.Fatalf("Execute(start) = %+v, %v", result, err)
	}
	if recorder.startCalls.Load() != 1 {
		t.Fatalf("recorder start calls = %d, want 1", recorder.startCalls.Load())
	}
	status := recordingStatus(t, store)
	if status.State != workerv1.RecordingState_RECORDING_STATE_RECORDING ||
		status.StartedAt.AsTime() != executorTestTime {
		t.Fatalf("recording status after start = %+v", status)
	}

	result, err = executor.Execute(context.Background(), startRecordingCommand(executorCommandID2, "take-id"))
	if err != nil || result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED {
		t.Fatalf("Execute(duplicate desired start) = %+v, %v", result, err)
	}
	shutdownResult, err := executor.Execute(context.Background(), shutdownCommand(executorCommandID3))
	if err != nil || shutdownResult.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED {
		t.Fatalf("Execute(shutdown while recording) = %+v, %v", shutdownResult, err)
	}

	result, err = executor.Execute(context.Background(), finishRecordingCommand(executorCommandID4, "take-id"))
	if err != nil || result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED {
		t.Fatalf("Execute(finish) = %+v, %v", result, err)
	}
	if recorder.finishCalls.Load() != 1 {
		t.Fatalf("recorder finish calls = %d, want 1", recorder.finishCalls.Load())
	}
	uploads := executor.uploads.(*uploadQueueStub)
	if uploads.publishCalls.Load() != 1 {
		t.Fatalf("upload manifest publish calls = %d, want 1", uploads.publishCalls.Load())
	}
	status = recordingStatus(t, store)
	if status.State != workerv1.RecordingState_RECORDING_STATE_FINISHED ||
		status.FinalizedFile.RelativePath != "recordings/take-id/video.mp4" {
		t.Fatalf("recording status after finish = %+v", status)
	}
	pending, err := store.PendingEvents()
	if err != nil {
		t.Fatalf("PendingEvents() error = %v", err)
	}
	if len(pending) != 2 ||
		pending[0].GetRecordingStatusChanged().State != workerv1.RecordingState_RECORDING_STATE_RECORDING ||
		pending[1].GetRecordingStatusChanged().State != workerv1.RecordingState_RECORDING_STATE_FINISHED {
		t.Fatalf("recording events = %+v", pending)
	}

	result, err = executor.Execute(context.Background(), finishRecordingCommand(executorCommandID5, "take-id"))
	if err != nil || result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED {
		t.Fatalf("Execute(duplicate desired finish) = %+v, %v", result, err)
	}
	result, err = executor.Execute(context.Background(), shutdownCommand(executorCommandID6))
	if err != nil || result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED {
		t.Fatalf("Execute(shutdown after finish) = %+v, %v", result, err)
	}
	if uploads.completeCalls.Load() != 1 {
		t.Fatalf("worker completion marker calls = %d, want 1", uploads.completeCalls.Load())
	}
}

func TestExecutorPersistsStartFailureBeforeReturningFailedResult(t *testing.T) {
	store := newExecutorState(t)
	recorder := &recorderStub{startError: errors.New("input contained secret://credential")}
	executor := newTestExecutor(t, store, recorder)
	result, err := executor.Execute(context.Background(), startRecordingCommand(executorCommandID1, "take-id"))
	if err != nil {
		t.Fatalf("Execute(start failure) error = %v", err)
	}
	if result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED ||
		result.Error.Code != workerv1.ErrorCode_ERROR_CODE_MEDIA_PIPELINE_FAILURE {
		t.Fatalf("Execute(start failure) = %+v", result)
	}
	if result.Error.Message == "input contained secret://credential" {
		t.Fatal("raw media process error was exposed in the command result")
	}
	status := recordingStatus(t, store)
	if status.State != workerv1.RecordingState_RECORDING_STATE_ERROR || status.Error.Code != result.Error.Code {
		t.Fatalf("persisted start failure = %+v, result = %+v", status, result)
	}
	pending, err := store.PendingEvents()
	if err != nil || len(pending) != 1 {
		t.Fatalf("PendingEvents() = %+v, %v", pending, err)
	}
	second, err := executor.Execute(context.Background(), startRecordingCommand(executorCommandID2, "take-id"))
	if err != nil || second.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED {
		t.Fatalf("Execute(start after terminal failure) = %+v, %v", second, err)
	}
	if recorder.startCalls.Load() != 1 {
		t.Fatalf("recorder retried terminal take: calls = %d", recorder.startCalls.Load())
	}
}

func TestExecutorPersistsFinalizationFailure(t *testing.T) {
	store := newExecutorState(t)
	recorder := &recorderStub{
		startedAt:   executorTestTime,
		finishError: errors.New("flush failed"),
	}
	executor := newTestExecutor(t, store, recorder)
	if _, err := executor.Execute(context.Background(), startRecordingCommand(executorCommandID1, "take-id")); err != nil {
		t.Fatalf("Execute(start) error = %v", err)
	}
	result, err := executor.Execute(context.Background(), finishRecordingCommand(executorCommandID2, "take-id"))
	if err != nil {
		t.Fatalf("Execute(finish failure) error = %v", err)
	}
	if result.Status != workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED ||
		result.Error.Code != workerv1.ErrorCode_ERROR_CODE_FINALIZATION_FAILURE {
		t.Fatalf("Execute(finish failure) = %+v", result)
	}
	status := recordingStatus(t, store)
	if status.State != workerv1.RecordingState_RECORDING_STATE_ERROR || status.StartedAt == nil {
		t.Fatalf("persisted finalization failure = %+v", status)
	}
}

func TestExecutorAbortsRecordingWhenInputDisconnects(t *testing.T) {
	store := newExecutorState(t)
	recorder := &recorderStub{startedAt: executorTestTime}
	executor := newTestExecutor(t, store, recorder)
	if _, err := executor.Execute(context.Background(), startRecordingCommand(executorCommandID1, "take-id")); err != nil {
		t.Fatalf("Execute(start) error = %v", err)
	}
	if err := executor.InputDisconnected(); err != nil {
		t.Fatalf("InputDisconnected() error = %v", err)
	}
	if recorder.abortCalls.Load() != 1 {
		t.Fatalf("recorder abort calls = %d, want 1", recorder.abortCalls.Load())
	}
	status := recordingStatus(t, store)
	if status.State != workerv1.RecordingState_RECORDING_STATE_ERROR ||
		status.Error.Code != workerv1.ErrorCode_ERROR_CODE_INPUT_DISCONNECTED {
		t.Fatalf("recording status after disconnect = %+v", status)
	}
}

type recorderStub struct {
	startCalls  atomic.Int32
	finishCalls atomic.Int32
	abortCalls  atomic.Int32
	startedAt   time.Time
	finalized   recording.FinalizedRecording
	startError  error
	finishError error
}

func (r *recorderStub) Start(context.Context, string) (time.Time, error) {
	r.startCalls.Add(1)
	return r.startedAt, r.startError
}

func (r *recorderStub) Finish(context.Context) (recording.FinalizedRecording, error) {
	r.finishCalls.Add(1)
	return r.finalized, r.finishError
}

func (r *recorderStub) Abort() error {
	r.abortCalls.Add(1)
	return nil
}

func newExecutorState(t *testing.T) *workerstate.Store {
	t.Helper()
	store, err := workerstate.Open(t.TempDir(), executorWorkerID, executorTestTime)
	if err != nil {
		t.Fatalf("state.Open() error = %v", err)
	}
	return store
}

func newTestExecutor(t *testing.T, store *workerstate.Store, recorder Recorder) *Executor {
	t.Helper()
	executor, err := NewExecutor(store, recorder, &uploadQueueStub{})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	var clockStep atomic.Int64
	executor.now = func() time.Time {
		return executorTestTime.Add(time.Duration(clockStep.Add(1)) * time.Millisecond)
	}
	eventIDs := []string{executorEventID1, executorEventID2}
	executor.newEventID = func() (string, error) {
		value := eventIDs[0]
		eventIDs = eventIDs[1:]
		return value, nil
	}
	return executor
}

type uploadQueueStub struct {
	publishCalls  atomic.Int32
	completeCalls atomic.Int32
}

func (q *uploadQueueStub) Publish(string, string, time.Time, time.Time) error {
	q.publishCalls.Add(1)
	return nil
}

func (q *uploadQueueStub) MarkWorkerComplete() error {
	q.completeCalls.Add(1)
	return nil
}

func recordingStatus(t *testing.T, store *workerstate.Store) *workerv1.RecordingStatus {
	t.Helper()
	status, found, err := store.RecordingStatus()
	if err != nil || !found {
		t.Fatalf("RecordingStatus() = %+v, %v, %v", status, found, err)
	}
	return status
}

func startRecordingCommand(commandID, takeID string) *workerv1.WorkerCommand {
	return &workerv1.WorkerCommand{
		CommandId: commandID,
		IssuedAt:  timestamppb.New(executorTestTime),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId:       takeID,
			RelativePath: "recordings/" + takeID + "/video.mp4",
		}},
	}
}

func finishRecordingCommand(commandID, takeID string) *workerv1.WorkerCommand {
	return &workerv1.WorkerCommand{
		CommandId: commandID,
		IssuedAt:  timestamppb.New(executorTestTime),
		Command: &workerv1.WorkerCommand_FinishRecording{FinishRecording: &workerv1.FinishRecording{
			TakeId: takeID,
		}},
	}
}

func shutdownCommand(commandID string) *workerv1.WorkerCommand {
	return &workerv1.WorkerCommand{
		CommandId: commandID,
		IssuedAt:  timestamppb.New(executorTestTime),
		Command:   &workerv1.WorkerCommand_Shutdown{Shutdown: &workerv1.Shutdown{Reason: "test complete"}},
	}
}
