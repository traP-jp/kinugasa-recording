package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

const (
	takeTestSessionID = "019c2923-33c2-7ef7-98e3-9f640d919ae4"
	takeTestCameraID  = "019c2923-3e23-7c66-ab40-95a3a80ef667"
	takeTestTakeID    = "019c2923-4677-792c-a1a5-d968b9bc3244"
	takeTestWorkerID  = "019c2923-4e88-78b6-bee5-da6e47fdd982"
	takeTestStartID   = "019c2923-56b4-7aa7-8a55-ece93a8d3b73"
	takeTestFinishID  = "019c2923-5ef9-75bf-b2c3-0f93dd364bb1"
	takeTestEventID   = "019c2923-65ca-75eb-b650-05aab41c79de"
)

func TestTakeCommandsAreCommittedWithDesiredState(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	createTakeTestCamera(t, store, now)
	start := &workerv1.WorkerCommand{
		CommandId: takeTestStartID, IssuedAt: timestamppb.New(now),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId: takeTestTakeID, RelativePath: "recording/session-1/take-1/camera-1/video.mp4",
		}},
	}
	take := domain.OngoingTake{
		ID: takeTestTakeID, SessionID: takeTestSessionID, Name: "take-1", StartedAt: now,
		Cameras: []domain.RecordingCamera{{
			OngoingTakeID: takeTestTakeID, CameraIdentityID: takeTestCameraID,
			State: domain.RecordingCameraStateRecording, StartedAt: now,
		}},
	}
	if err := store.CreateTake(ctx, repository.StartTakeRequest{
		Take: take, CameraNames: []string{"camera-1"},
		Commands: []repository.CameraCommand{{CameraIdentityID: takeTestCameraID, Command: start}},
	}); err != nil {
		t.Fatalf("CreateTake() error = %v", err)
	}
	loaded, err := store.GetOngoingTake(ctx, "session-1")
	if err != nil || loaded == nil || loaded.ID != take.ID || len(loaded.Cameras) != 1 {
		t.Fatalf("GetOngoingTake() = %+v, %v", loaded, err)
	}
	pending, err := store.PendingWorkerCommands(ctx, takeTestWorkerID)
	if err != nil || len(pending) != 1 || pending[0].CommandId != takeTestStartID {
		t.Fatalf("PendingWorkerCommands(start) = %+v, %v", pending, err)
	}
	finish := &workerv1.WorkerCommand{
		CommandId: takeTestFinishID, IssuedAt: timestamppb.New(now.Add(time.Minute)),
		Command: &workerv1.WorkerCommand_FinishRecording{FinishRecording: &workerv1.FinishRecording{
			TakeId: takeTestTakeID,
		}},
	}
	finished, err := store.FinishTake(ctx, repository.FinishTakeRequest{
		SessionName: "session-1", FinishedAt: now.Add(time.Minute),
		Commands: []repository.CameraCommand{{CameraIdentityID: takeTestCameraID, Command: finish}},
	})
	if err != nil || finished.ID != take.ID || finished.State != domain.FinishedTakeStateUploading {
		t.Fatalf("FinishTake() = %+v, %v", finished, err)
	}
	var videoState string
	if err := pool.QueryRow(ctx, `
		SELECT state FROM video_files WHERE take_id = $1 AND camera_identity_id = $2`,
		takeTestTakeID, takeTestCameraID,
	).Scan(&videoState); err != nil || videoState != "uploading" {
		t.Fatalf("placeholder video state = %q, %v", videoState, err)
	}
	if ongoing, err := store.GetOngoingTake(ctx, "session-1"); err != nil || ongoing != nil {
		t.Fatalf("GetOngoingTake(after finish) = %+v, %v", ongoing, err)
	}
	pending, err = store.PendingWorkerCommands(ctx, takeTestWorkerID)
	if err != nil || len(pending) != 2 || pending[1].CommandId != takeTestFinishID {
		t.Fatalf("PendingWorkerCommands(finish) = %+v, %v", pending, err)
	}
	hello := &workerv1.WorkerHello{
		WorkerId: takeTestWorkerID, SessionId: takeTestSessionID, CameraIdentityId: takeTestCameraID,
		ObservedAt: timestamppb.New(now.Add(time.Minute)),
		Snapshot: &workerv1.WorkerSnapshot{Input: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_CONNECTED,
		}},
	}
	if err := store.RegisterWorker(ctx, hello, now.Add(time.Minute)); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	mediaStartedAt := now.Add(10 * time.Second)
	mediaFinishedAt := now.Add(70 * time.Second)
	event := &workerv1.WorkerEvent{
		EventId: takeTestEventID, Sequence: 1, OccurredAt: timestamppb.New(mediaFinishedAt),
		Event: &workerv1.WorkerEvent_RecordingStatusChanged{RecordingStatusChanged: &workerv1.RecordingStatus{
			TakeId: takeTestTakeID, State: workerv1.RecordingState_RECORDING_STATE_FINISHED,
			StartedAt: timestamppb.New(mediaStartedAt), FinishedAt: timestamppb.New(mediaFinishedAt),
			FinalizedFile: &workerv1.FinalizedFile{
				RelativePath: "recording/session-1/take-1/camera-1/video.mp4", MediaType: "video/mp4",
			},
		}},
	}
	if err := store.ApplyWorkerEvent(ctx, takeTestWorkerID, event); err != nil {
		t.Fatalf("ApplyWorkerEvent(finished) error = %v", err)
	}
	report := &upv1.UploadReport{
		SessionId: takeTestSessionID, CameraIdentityId: takeTestCameraID, TakeId: takeTestTakeID,
		RelativePath: "recording/session-1/take-1/camera-1/video.mp4",
		StartedAt:    timestamppb.New(mediaStartedAt), FinishedAt: timestamppb.New(mediaFinishedAt),
		State:     upv1.UploadState_UPLOAD_STATE_COMPLETED,
		ObjectKey: "recording/session-1/take-1/camera-1/0000000000000000000000000000000000000000000000000000000000000000-video.mp4",
		Sha256:    make([]byte, 32), Size: 42, ObservedAt: timestamppb.New(mediaFinishedAt.Add(time.Second)),
	}
	if err := store.ApplyUploadReport(ctx, report); err != nil {
		t.Fatalf("ApplyUploadReport() error = %v", err)
	}
	var takeState string
	if err := pool.QueryRow(ctx, `SELECT state FROM takes WHERE id = $1`, takeTestTakeID).Scan(&takeState); err != nil || takeState != "completed" {
		t.Fatalf("converged take state = %q, %v", takeState, err)
	}
	page, err := store.ListFinishedTakes(ctx, "session-1", repository.PageRequest{Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].State != domain.FinishedTakeStateCompleted {
		t.Fatalf("ListFinishedTakes() = %+v, %v", page, err)
	}
	detail, err := store.GetFinishedTake(ctx, "session-1", "take-1")
	if err != nil || len(detail.Take.VideoFiles) != 1 || detail.Take.VideoFiles[0].Hash == nil ||
		detail.CameraNames[takeTestCameraID] != "camera-1" {
		t.Fatalf("GetFinishedTake() = %+v, %v", detail, err)
	}
	objects, err := store.ListLockfileObjects(ctx, "session-1")
	if err != nil || len(objects) != 1 || objects[0].LogicalPath != "recording/session-1/take-1/camera-1/video.mp4" || objects[0].Size != 42 {
		t.Fatalf("ListLockfileObjects() = %+v, %v", objects, err)
	}
}

func TestFinishTakeConvergesErroredRecordings(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	createTakeTestCamera(t, store, now)
	start := &workerv1.WorkerCommand{CommandId: takeTestStartID, IssuedAt: timestamppb.New(now),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId: takeTestTakeID, RelativePath: "recording/session-1/take-1/camera-1/video.mp4"}}}
	take := domain.OngoingTake{ID: takeTestTakeID, SessionID: takeTestSessionID, Name: "take-1", StartedAt: now,
		Cameras: []domain.RecordingCamera{{OngoingTakeID: takeTestTakeID, CameraIdentityID: takeTestCameraID,
			State: domain.RecordingCameraStateRecording, StartedAt: now}}}
	if err := store.CreateTake(ctx, repository.StartTakeRequest{Take: take, CameraNames: []string{"camera-1"},
		Commands: []repository.CameraCommand{{CameraIdentityID: takeTestCameraID, Command: start}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE recording_cameras SET state = 'errored', error = 'input disconnected'
		WHERE take_id = $1 AND camera_identity_id = $2`, takeTestTakeID, takeTestCameraID); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishTake(ctx, repository.FinishTakeRequest{
		SessionName: "session-1", FinishedAt: now.Add(time.Minute),
	})
	if err != nil || finished.State != domain.FinishedTakeStateErrored || finished.Error == "" {
		t.Fatalf("FinishTake() = %+v, %v", finished, err)
	}
	var videoState, videoError string
	if err := pool.QueryRow(ctx, `
		SELECT state, error FROM video_files WHERE take_id = $1 AND camera_identity_id = $2`,
		takeTestTakeID, takeTestCameraID,
	).Scan(&videoState, &videoError); err != nil || videoState != "errored" || videoError != "input disconnected" {
		t.Fatalf("errored video = %q, %q, %v", videoState, videoError, err)
	}
}

func TestCreateTakeRollsBackWhenCameraIsNotConnected(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	now := time.Now().UTC()
	if err := store.CreateSession(context.Background(), domain.Session{
		ID: takeTestSessionID, Name: "session-1", State: domain.SessionStateActive, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCamera(context.Background(), domain.CameraIdentity{
		ID: takeTestCameraID, SessionID: takeTestSessionID, Name: "camera-1", CreatedAt: now,
	}, domain.CameraConnection{CameraIdentityID: takeTestCameraID, Status: domain.CameraConnectionStatusActivating}); err != nil {
		t.Fatal(err)
	}
	take := domain.OngoingTake{ID: takeTestTakeID, SessionID: takeTestSessionID, Name: "take-1", StartedAt: now,
		Cameras: []domain.RecordingCamera{{OngoingTakeID: takeTestTakeID, CameraIdentityID: takeTestCameraID,
			State: domain.RecordingCameraStateRecording, StartedAt: now}}}
	command := &workerv1.WorkerCommand{CommandId: takeTestStartID, IssuedAt: timestamppb.New(now),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId: takeTestTakeID, RelativePath: "recording/session-1/take-1/camera-1/video.mp4"}}}
	err := store.CreateTake(context.Background(), repository.StartTakeRequest{Take: take,
		CameraNames: []string{"camera-1"}, Commands: []repository.CameraCommand{{CameraIdentityID: takeTestCameraID, Command: command}}})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("CreateTake() error = %v, want conflict", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM takes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back take count = %d, %v", count, err)
	}
}

func TestFinishedTakeReadsRequireExistingResources(t *testing.T) {
	store := New(resetDatabase(t))
	ctx := context.Background()
	if _, err := store.ListFinishedTakes(ctx, "missing", repository.PageRequest{Page: 1, PageSize: 20}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("ListFinishedTakes() error = %v", err)
	}
	if _, err := store.GetFinishedTake(ctx, "missing", "take-1"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetFinishedTake() error = %v", err)
	}
	if _, err := store.ListLockfileObjects(ctx, "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("ListLockfileObjects() error = %v", err)
	}
}

func createTakeTestCamera(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateSession(ctx, domain.Session{ID: takeTestSessionID, Name: "session-1", State: domain.SessionStateActive, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCamera(ctx, domain.CameraIdentity{ID: takeTestCameraID, SessionID: takeTestSessionID, Name: "camera-1", CreatedAt: now},
		domain.CameraConnection{CameraIdentityID: takeTestCameraID, Status: domain.CameraConnectionStatusActivating}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE camera_connections SET status = 'connected', url = 'rist://camera', video_worker_id = $2
		WHERE camera_identity_id = $1`, takeTestCameraID, takeTestWorkerID); err != nil {
		t.Fatal(err)
	}
}
