package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

const (
	workerTestSessionID = "019c250d-a6de-7de0-a826-0f26e8803fc0"
	workerTestCameraID  = "019c250e-3eb4-72d6-a6fa-adfe1df795c8"
	workerTestTakeID    = "019c250e-4a04-73e3-8328-a32a246b8c47"
	workerTestWorkerID1 = "019c250e-5141-75e4-8b4b-5c611e7fab65"
	workerTestWorkerID2 = "019c250e-5a60-7770-afad-af8697c0b37a"
	workerTestEventID1  = "019c250e-60c4-7747-b886-bd17d9c72588"
	workerTestEventID2  = "019c250e-67d7-755d-a00a-e11e891f6228"
	workerTestCommandID = "019c250e-6cf6-7db7-837f-10e37742097f"
)

func TestWorkerRegistrationAndEventsAreTransactional(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	createWorkerDomainState(t, store, now)

	hello := workerHello(workerTestWorkerID1, now)
	if err := store.RegisterWorker(ctx, hello, now); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	var connectionStatus, videoWorkerID, recordingState string
	if err := pool.QueryRow(ctx, `
		SELECT camera_connections.status,
		       camera_connections.video_worker_id::text,
		       recording_cameras.state
		FROM camera_connections
		JOIN recording_cameras USING (camera_identity_id)
		WHERE camera_connections.camera_identity_id = $1`, workerTestCameraID,
	).Scan(&connectionStatus, &videoWorkerID, &recordingState); err != nil {
		t.Fatalf("query registered worker state: %v", err)
	}
	if connectionStatus != "connected" || videoWorkerID != workerTestWorkerID1 || recordingState != "recording" {
		t.Fatalf("registered states = %q, %q, %q", connectionStatus, videoWorkerID, recordingState)
	}

	inputError := &workerv1.WorkerEvent{
		EventId:    workerTestEventID1,
		OccurredAt: timestamppb.New(now.Add(time.Second)),
		Sequence:   1,
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_ERROR,
			Error: &workerv1.WorkerError{
				Code:    workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_FRAME_RATE,
				Message: "input is not 30 fps",
			},
		}},
	}
	if err := store.ApplyWorkerEvent(ctx, workerTestWorkerID1, inputError); err != nil {
		t.Fatalf("ApplyWorkerEvent(input error) error = %v", err)
	}
	if err := store.ApplyWorkerEvent(ctx, workerTestWorkerID1, inputError); err != nil {
		t.Fatalf("ApplyWorkerEvent(duplicate) error = %v", err)
	}
	var eventCount int
	var errorReason string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_events`).Scan(&eventCount); err != nil {
		t.Fatalf("count worker events: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT error FROM camera_connections WHERE camera_identity_id = $1`, workerTestCameraID,
	).Scan(&errorReason); err != nil {
		t.Fatalf("query camera error: %v", err)
	}
	if eventCount != 1 || errorReason != "input is not 30 fps" {
		t.Fatalf("event count/error = %d/%q", eventCount, errorReason)
	}
	if err := store.RegisterWorker(ctx, hello, now.Add(2*time.Second)); !errors.Is(err, repository.ErrWorkerEventSequence) {
		t.Fatalf("RegisterWorker(stale snapshot) error = %v, want ErrWorkerEventSequence", err)
	}

	gap := &workerv1.WorkerEvent{
		EventId:    workerTestEventID2,
		OccurredAt: timestamppb.New(now.Add(2 * time.Second)),
		Sequence:   3,
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_WAITING,
		}},
	}
	if err := store.ApplyWorkerEvent(ctx, workerTestWorkerID1, gap); !errors.Is(err, repository.ErrWorkerEventSequence) {
		t.Fatalf("ApplyWorkerEvent(gap) error = %v, want ErrWorkerEventSequence", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_events`).Scan(&eventCount); err != nil {
		t.Fatalf("count worker events after gap: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event count after rolled back gap = %d, want 1", eventCount)
	}

	finished := &workerv1.WorkerEvent{
		EventId:    workerTestEventID2,
		OccurredAt: timestamppb.New(now.Add(time.Minute)),
		Sequence:   2,
		Event: &workerv1.WorkerEvent_RecordingStatusChanged{RecordingStatusChanged: &workerv1.RecordingStatus{
			TakeId:     workerTestTakeID,
			State:      workerv1.RecordingState_RECORDING_STATE_FINISHED,
			StartedAt:  timestamppb.New(now),
			FinishedAt: timestamppb.New(now.Add(time.Minute)),
			FinalizedFile: &workerv1.FinalizedFile{
				RelativePath: "recordings/take-1/video.mp4",
				MediaType:    "video/mp4",
			},
		}},
	}
	if err := store.ApplyWorkerEvent(ctx, workerTestWorkerID1, finished); err != nil {
		t.Fatalf("ApplyWorkerEvent(finished) error = %v", err)
	}
	var relativePath string
	if err := pool.QueryRow(ctx, `
		SELECT relative_path FROM finalized_recordings
		WHERE take_id = $1 AND camera_identity_id = $2`, workerTestTakeID, workerTestCameraID,
	).Scan(&relativePath); err != nil {
		t.Fatalf("query finalized recording: %v", err)
	}
	if relativePath != "recordings/take-1/video.mp4" {
		t.Fatalf("finalized relative path = %q", relativePath)
	}
}

func TestNewWorkerSupersedesOldWorkerEvents(t *testing.T) {
	store := New(resetDatabase(t))
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	createWorkerDomainState(t, store, now)
	if err := store.RegisterWorker(ctx, workerHello(workerTestWorkerID1, now), now); err != nil {
		t.Fatalf("RegisterWorker(old) error = %v", err)
	}
	if err := store.RegisterWorker(ctx, workerHello(workerTestWorkerID2, now), now.Add(time.Second)); err != nil {
		t.Fatalf("RegisterWorker(new) error = %v", err)
	}
	event := &workerv1.WorkerEvent{
		EventId:    workerTestEventID1,
		OccurredAt: timestamppb.New(now.Add(2 * time.Second)),
		Sequence:   1,
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_WAITING,
		}},
	}
	if err := store.ApplyWorkerEvent(ctx, workerTestWorkerID1, event); !errors.Is(err, repository.ErrWorkerIdentityMismatch) {
		t.Fatalf("ApplyWorkerEvent(old worker) error = %v, want ErrWorkerIdentityMismatch", err)
	}
}

func TestRegisterWorkerRejectsMismatchedIdentity(t *testing.T) {
	store := New(resetDatabase(t))
	now := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	createWorkerDomainState(t, store, now)
	hello := workerHello(workerTestWorkerID1, now)
	hello.SessionId = "019c250e-6cf6-7db7-837f-10e37742097f"

	if err := store.RegisterWorker(context.Background(), hello, now); !errors.Is(err, repository.ErrWorkerIdentityMismatch) {
		t.Fatalf("RegisterWorker() error = %v, want ErrWorkerIdentityMismatch", err)
	}
}

func TestSaveCommandResultIsIdempotent(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	createWorkerDomainState(t, store, now)
	if err := store.RegisterWorker(ctx, workerHello(workerTestWorkerID1, now), now); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	command := &workerv1.WorkerCommand{
		CommandId: workerTestCommandID,
		IssuedAt:  timestamppb.New(now),
		Command: &workerv1.WorkerCommand_FinishRecording{FinishRecording: &workerv1.FinishRecording{
			TakeId: workerTestTakeID,
		}},
	}
	encodedCommand, err := proto.Marshal(command)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO worker_commands (
			command_id, camera_identity_id, take_id, issued_at, payload
		) VALUES ($1, $2, $3, $4, $5)`,
		workerTestCommandID, workerTestCameraID, workerTestTakeID, now, encodedCommand,
	); err != nil {
		t.Fatalf("insert worker command: %v", err)
	}
	result := &workerv1.CommandResult{
		CommandId:   workerTestCommandID,
		Status:      workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED,
		CompletedAt: timestamppb.New(now.Add(time.Second)),
	}
	if err := store.SaveCommandResult(ctx, workerTestWorkerID1, result); err != nil {
		t.Fatalf("SaveCommandResult() error = %v", err)
	}
	if err := store.SaveCommandResult(ctx, workerTestWorkerID1, proto.Clone(result).(*workerv1.CommandResult)); err != nil {
		t.Fatalf("SaveCommandResult(duplicate) error = %v", err)
	}
	var commandStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM worker_commands WHERE command_id = $1`, workerTestCommandID,
	).Scan(&commandStatus); err != nil {
		t.Fatalf("query command status: %v", err)
	}
	if commandStatus != "applied" {
		t.Fatalf("command status = %q, want applied", commandStatus)
	}
	result.Status = workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED
	if err := store.SaveCommandResult(ctx, workerTestWorkerID1, result); !errors.Is(err, repository.ErrWorkerCommandMismatch) {
		t.Fatalf("SaveCommandResult(different) error = %v, want ErrWorkerCommandMismatch", err)
	}
}

func createWorkerDomainState(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	session := domain.Session{
		ID:        workerTestSessionID,
		Name:      "session-worker",
		State:     domain.SessionStateActive,
		CreatedAt: now,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	identity := domain.CameraIdentity{
		ID:        workerTestCameraID,
		SessionID: workerTestSessionID,
		Name:      "camera-worker",
		CreatedAt: now,
	}
	connection := domain.CameraConnection{
		CameraIdentityID: workerTestCameraID,
		URL:              "rist://recording.example.com:9200",
		Status:           domain.CameraConnectionStatusWaiting,
	}
	if err := store.CreateCamera(ctx, identity, connection); err != nil {
		t.Fatalf("CreateCamera() error = %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO takes (id, session_id, name, phase, started_at)
		VALUES ($1, $2, 'take-worker', 'ongoing', $3)`,
		workerTestTakeID, workerTestSessionID, now,
	); err != nil {
		t.Fatalf("create ongoing take: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO recording_cameras (
			take_id, camera_identity_id, session_id, state, started_at
		) VALUES ($1, $2, $3, 'recording', $4)`,
		workerTestTakeID, workerTestCameraID, workerTestSessionID, now,
	); err != nil {
		t.Fatalf("create ongoing recording: %v", err)
	}
}

func workerHello(workerID string, now time.Time) *workerv1.WorkerHello {
	return &workerv1.WorkerHello{
		WorkerId:         workerID,
		SessionId:        workerTestSessionID,
		CameraIdentityId: workerTestCameraID,
		ObservedAt:       timestamppb.New(now),
		Snapshot: &workerv1.WorkerSnapshot{
			Input: &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_CONNECTED},
			Recording: &workerv1.RecordingStatus{
				TakeId:    workerTestTakeID,
				State:     workerv1.RecordingState_RECORDING_STATE_RECORDING,
				StartedAt: timestamppb.New(now),
			},
		},
	}
}
