package command

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
	"github.com/traP-jp/kinugasa-recording/internal/worker/recording"
)

type StateStore interface {
	SetRecordingStatus(*workerv1.RecordingStatus) error
	RecordingStatus() (*workerv1.RecordingStatus, bool, error)
	AppendEvent(*workerv1.WorkerEvent) (*workerv1.WorkerEvent, error)
}

type Recorder interface {
	Start(context.Context, string) (time.Time, error)
	Finish(context.Context) (recording.FinalizedRecording, error)
	Abort() error
}

type Executor struct {
	mu            sync.Mutex
	state         StateStore
	recorder      Recorder
	now           func() time.Time
	newEventID    func() (string, error)
	activeTake    string
	activePath    string
	completedTake string
}

func NewExecutor(state StateStore, recorder Recorder) (*Executor, error) {
	if state == nil {
		return nil, fmt.Errorf("worker state store must be set")
	}
	if recorder == nil {
		return nil, fmt.Errorf("recording process must be set")
	}
	executor := &Executor{
		state:      state,
		recorder:   recorder,
		now:        time.Now,
		newEventID: uuidV7,
	}
	status, found, err := state.RecordingStatus()
	if err != nil {
		return nil, fmt.Errorf("load recording state: %w", err)
	}
	if found && (status.State == workerv1.RecordingState_RECORDING_STATE_FINISHED ||
		status.State == workerv1.RecordingState_RECORDING_STATE_ERROR) {
		executor.completedTake = status.TakeId
	}
	return executor, nil
}

func (e *Executor) Execute(
	ctx context.Context,
	command *workerv1.WorkerCommand,
) (*workerv1.CommandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := workerprotocol.ValidateWorkerCommand(command); err != nil {
		return nil, err
	}
	switch payload := command.Command.(type) {
	case *workerv1.WorkerCommand_StartRecording:
		return e.start(ctx, command.CommandId, payload.StartRecording)
	case *workerv1.WorkerCommand_FinishRecording:
		return e.finish(ctx, command.CommandId, payload.FinishRecording)
	case *workerv1.WorkerCommand_Shutdown:
		return e.shutdown(command.CommandId), nil
	default:
		return nil, fmt.Errorf("unsupported worker command")
	}
}

func (e *Executor) InputDisconnected() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeTake == "" {
		return nil
	}
	current, found, err := e.state.RecordingStatus()
	if err != nil {
		return fmt.Errorf("load disconnected recording state: %w", err)
	}
	if !found {
		return fmt.Errorf("active recording state is missing")
	}
	if err := e.recorder.Abort(); err != nil {
		return fmt.Errorf("abort disconnected recording: %w", err)
	}
	takeID := e.activeTake
	e.activeTake = ""
	e.activePath = ""
	e.completedTake = takeID
	workerError := &workerv1.WorkerError{
		Code:      workerv1.ErrorCode_ERROR_CODE_INPUT_DISCONNECTED,
		Message:   "camera input disconnected while recording",
		Retryable: false,
	}
	return e.appendRecordingEvent(&workerv1.RecordingStatus{
		TakeId:     takeID,
		State:      workerv1.RecordingState_RECORDING_STATE_ERROR,
		StartedAt:  current.StartedAt,
		FinishedAt: timestamppb.New(e.now().UTC()),
		Error:      workerError,
	})
}

func (e *Executor) start(
	ctx context.Context,
	commandID string,
	start *workerv1.StartRecording,
) (*workerv1.CommandResult, error) {
	if e.activeTake != "" {
		if e.activeTake == start.TakeId && e.activePath == start.RelativePath {
			return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED, nil), nil
		}
		return e.rejected(
			commandID,
			workerv1.ErrorCode_ERROR_CODE_RECORDING_ALREADY_ACTIVE,
			"another recording is already active",
		), nil
	}
	if e.completedTake == start.TakeId {
		return e.rejected(
			commandID,
			workerv1.ErrorCode_ERROR_CODE_INVALID_COMMAND,
			"the take already has a terminal recording state",
		), nil
	}
	starting := &workerv1.RecordingStatus{
		TakeId: start.TakeId,
		State:  workerv1.RecordingState_RECORDING_STATE_STARTING,
	}
	if err := e.state.SetRecordingStatus(starting); err != nil {
		return nil, fmt.Errorf("persist starting recording: %w", err)
	}
	startedAt, err := e.recorder.Start(ctx, start.RelativePath)
	if err != nil {
		workerError := &workerv1.WorkerError{
			Code:      workerv1.ErrorCode_ERROR_CODE_MEDIA_PIPELINE_FAILURE,
			Message:   "recording pipeline failed before the first frame",
			Retryable: true,
		}
		if persistError := e.appendRecordingEvent(&workerv1.RecordingStatus{
			TakeId:     start.TakeId,
			State:      workerv1.RecordingState_RECORDING_STATE_ERROR,
			FinishedAt: timestamppb.New(e.now().UTC()),
			Error:      workerError,
		}); persistError != nil {
			return nil, fmt.Errorf("persist recording start failure: %w", persistError)
		}
		e.completedTake = start.TakeId
		return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED, workerError), nil
	}
	e.activeTake = start.TakeId
	e.activePath = start.RelativePath
	e.completedTake = ""
	if err := e.appendRecordingEvent(&workerv1.RecordingStatus{
		TakeId:    start.TakeId,
		State:     workerv1.RecordingState_RECORDING_STATE_RECORDING,
		StartedAt: timestamppb.New(startedAt.UTC()),
	}); err != nil {
		abortError := e.recorder.Abort()
		e.activeTake = ""
		e.activePath = ""
		if abortError != nil {
			return nil, fmt.Errorf("persist active recording: %w; abort recording: %v", err, abortError)
		}
		return nil, fmt.Errorf("persist active recording: %w", err)
	}
	return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED, nil), nil
}

func (e *Executor) finish(
	ctx context.Context,
	commandID string,
	finish *workerv1.FinishRecording,
) (*workerv1.CommandResult, error) {
	if e.activeTake == "" {
		if e.completedTake == finish.TakeId {
			return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED, nil), nil
		}
		return e.rejected(
			commandID,
			workerv1.ErrorCode_ERROR_CODE_TAKE_MISMATCH,
			"the requested take is not being recorded",
		), nil
	}
	if e.activeTake != finish.TakeId {
		return e.rejected(
			commandID,
			workerv1.ErrorCode_ERROR_CODE_TAKE_MISMATCH,
			"the active recording belongs to another take",
		), nil
	}
	current, found, err := e.state.RecordingStatus()
	if err != nil {
		return nil, fmt.Errorf("load active recording state: %w", err)
	}
	if !found || current.StartedAt == nil {
		return nil, fmt.Errorf("active recording state is missing its start time")
	}
	finalizing := &workerv1.RecordingStatus{
		TakeId:    current.TakeId,
		State:     workerv1.RecordingState_RECORDING_STATE_FINALIZING,
		StartedAt: current.StartedAt,
	}
	if err := e.state.SetRecordingStatus(finalizing); err != nil {
		return nil, fmt.Errorf("persist finalizing recording: %w", err)
	}
	expectedPath := e.activePath
	finalized, err := e.recorder.Finish(ctx)
	e.activeTake = ""
	e.activePath = ""
	e.completedTake = finish.TakeId
	if err != nil {
		workerError := &workerv1.WorkerError{
			Code:      workerv1.ErrorCode_ERROR_CODE_FINALIZATION_FAILURE,
			Message:   "recording pipeline failed while finalizing the MP4 file",
			Retryable: false,
		}
		if persistError := e.appendRecordingEvent(&workerv1.RecordingStatus{
			TakeId:     finish.TakeId,
			State:      workerv1.RecordingState_RECORDING_STATE_ERROR,
			StartedAt:  current.StartedAt,
			FinishedAt: timestamppb.New(e.now().UTC()),
			Error:      workerError,
		}); persistError != nil {
			return nil, fmt.Errorf("persist recording finalization failure: %w", persistError)
		}
		return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED, workerError), nil
	}
	if finalized.RelativePath != expectedPath {
		return nil, fmt.Errorf("finalized recording path does not match active recording")
	}
	if err := e.appendRecordingEvent(&workerv1.RecordingStatus{
		TakeId:     finish.TakeId,
		State:      workerv1.RecordingState_RECORDING_STATE_FINISHED,
		StartedAt:  timestamppb.New(finalized.StartedAt.UTC()),
		FinishedAt: timestamppb.New(finalized.FinishedAt.UTC()),
		FinalizedFile: &workerv1.FinalizedFile{
			RelativePath: finalized.RelativePath,
			MediaType:    "video/mp4",
		},
	}); err != nil {
		return nil, fmt.Errorf("persist finalized recording: %w", err)
	}
	return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED, nil), nil
}

func (e *Executor) shutdown(commandID string) *workerv1.CommandResult {
	if e.activeTake != "" {
		return e.rejected(
			commandID,
			workerv1.ErrorCode_ERROR_CODE_RECORDING_ALREADY_ACTIVE,
			"cannot shut down while recording",
		)
	}
	return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED, nil)
}

func (e *Executor) appendRecordingEvent(status *workerv1.RecordingStatus) error {
	eventID, err := e.newEventID()
	if err != nil {
		return fmt.Errorf("generate recording event ID: %w", err)
	}
	_, err = e.state.AppendEvent(&workerv1.WorkerEvent{
		EventId:    eventID,
		OccurredAt: timestamppb.New(e.now().UTC()),
		Event: &workerv1.WorkerEvent_RecordingStatusChanged{
			RecordingStatusChanged: status,
		},
	})
	return err
}

func (e *Executor) rejected(
	commandID string,
	code workerv1.ErrorCode,
	message string,
) *workerv1.CommandResult {
	return e.result(commandID, workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED, &workerv1.WorkerError{
		Code:      code,
		Message:   message,
		Retryable: false,
	})
}

func (e *Executor) result(
	commandID string,
	status workerv1.CommandResultStatus,
	workerError *workerv1.WorkerError,
) *workerv1.CommandResult {
	return &workerv1.CommandResult{
		CommandId:   commandID,
		Status:      status,
		CompletedAt: timestamppb.New(e.now().UTC()),
		Error:       workerError,
	}
}

func uuidV7() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
