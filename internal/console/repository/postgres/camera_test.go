package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func TestCameraRepositoryPreservesIdentityAndName(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID:        "019c240d-a6de-7de0-a826-0f26e8803fc0",
		Name:      "session",
		State:     domain.SessionStateActive,
		CreatedAt: createdAt,
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	identity := domain.CameraIdentity{
		ID:        "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
		SessionID: session.ID,
		Name:      "camera-1",
		CreatedAt: createdAt,
	}
	connection := domain.CameraConnection{
		CameraIdentityID: identity.ID,
		Status:           domain.CameraConnectionStatusActivating,
	}
	if err := store.CreateCamera(ctx, identity, connection); err != nil {
		t.Fatalf("CreateCamera() error = %v", err)
	}

	cameras, err := store.ListCameras(ctx, session.Name)
	if err != nil {
		t.Fatalf("ListCameras() error = %v", err)
	}
	if len(cameras) != 1 || cameras[0].Identity.Name != identity.Name || cameras[0].Connection.URL != "" {
		t.Fatalf("ListCameras() = %+v", cameras)
	}
	got, err := store.GetCamera(ctx, session.Name, identity.Name)
	if err != nil {
		t.Fatalf("GetCamera() error = %v", err)
	}
	if got.Connection.CameraIdentityID != identity.ID {
		t.Fatalf("GetCamera() camera identity = %q, want %q", got.Connection.CameraIdentityID, identity.ID)
	}
	resources, err := store.ListCameraResources(ctx)
	if err != nil {
		t.Fatalf("ListCameraResources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].SessionName != session.Name || resources[0].Identity.ID != identity.ID {
		t.Fatalf("ListCameraResources() = %+v", resources)
	}
	if err := store.ActivateCameraConnection(ctx, string(identity.ID), "rist://camera.example.com:9000"); err != nil {
		t.Fatalf("ActivateCameraConnection() error = %v", err)
	}
	activated, err := store.GetCamera(ctx, session.Name, identity.Name)
	if err != nil || activated.Connection.Status != domain.CameraConnectionStatusWaiting ||
		activated.Connection.URL != "rist://camera.example.com:9000" {
		t.Fatalf("activated camera = %+v, %v", activated, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE camera_connections SET status = 'connected' WHERE camera_identity_id = $1`, identity.ID); err != nil {
		t.Fatalf("set connected camera: %v", err)
	}
	if err := store.ActivateCameraConnection(ctx, string(identity.ID), "rist://127.0.0.1:32000"); err != nil {
		t.Fatalf("update camera connection URL: %v", err)
	}
	reassigned, err := store.GetCamera(ctx, session.Name, identity.Name)
	if err != nil || reassigned.Connection.Status != domain.CameraConnectionStatusConnected ||
		reassigned.Connection.URL != "rist://127.0.0.1:32000" {
		t.Fatalf("reassigned camera = %+v, %v", reassigned, err)
	}

	shutdown := repository.CameraCommand{CameraIdentityID: string(identity.ID), Command: &workerv1.WorkerCommand{
		CommandId: "019c240e-5141-75e4-8b4b-5c611e7fab65", IssuedAt: timestamppb.New(createdAt),
		Command: &workerv1.WorkerCommand_Shutdown{Shutdown: &workerv1.Shutdown{Reason: "test deletion"}},
	}}
	if err := store.RequestCameraDeletion(ctx, session.Name, identity.Name, shutdown, createdAt, false); err != nil {
		t.Fatalf("RequestCameraDeletion() error = %v", err)
	}
	if _, err := store.GetCamera(ctx, session.Name, identity.Name); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetCamera(deleting) error = %v, want ErrNotFound", err)
	}
	var identities int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM camera_identities WHERE id = $1`, identity.ID,
	).Scan(&identities); err != nil {
		t.Fatalf("count camera identities: %v", err)
	}
	if identities != 1 {
		t.Fatalf("camera identity count = %d, want 1", identities)
	}
	resources, err = store.ListCameraResources(ctx)
	if err != nil {
		t.Fatalf("ListCameraResources(after delete) error = %v", err)
	}
	if len(resources) != 1 || !resources[0].Deleting {
		t.Fatalf("ListCameraResources(during delete) = %+v, want deleting resource", resources)
	}
	if err := store.CompleteCameraDeletion(ctx, string(identity.ID)); err != nil {
		t.Fatalf("CompleteCameraDeletion() error = %v", err)
	}
	resources, err = store.ListCameraResources(ctx)
	if err != nil || len(resources) != 0 {
		t.Fatalf("ListCameraResources(after delete) = %+v, %v", resources, err)
	}

	identity.ID = "019c240e-4a04-73e3-8328-a32a246b8c47"
	connection.CameraIdentityID = identity.ID
	if err := store.CreateCamera(ctx, identity, connection); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("reused camera name error = %v, want ErrConflict", err)
	}
}

func TestListCamerasRequiresSession(t *testing.T) {
	store := New(resetDatabase(t))

	if _, err := store.ListCameras(context.Background(), "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("ListCameras() error = %v, want ErrNotFound", err)
	}
}

func TestCreateCameraRequiresSession(t *testing.T) {
	store := New(resetDatabase(t))
	identity := domain.CameraIdentity{
		ID:        "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
		SessionID: "019c240d-a6de-7de0-a826-0f26e8803fc0",
		Name:      "camera-1",
		CreatedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
	}
	connection := domain.CameraConnection{
		CameraIdentityID: identity.ID,
		Status:           domain.CameraConnectionStatusActivating,
	}

	if err := store.CreateCamera(context.Background(), identity, connection); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("CreateCamera() error = %v, want ErrNotFound", err)
	}
}

func TestCameraDeletionRequiresForceForUploadingVideo(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	createTakeTestCamera(t, store, now)
	take := domain.OngoingTake{
		ID: takeTestTakeID, SessionID: takeTestSessionID, Name: "take-1", StartedAt: now,
		Cameras: []domain.RecordingCamera{{
			OngoingTakeID: takeTestTakeID, CameraIdentityID: takeTestCameraID,
			State: domain.RecordingCameraStateRecording, StartedAt: now,
		}},
	}
	start := repository.CameraCommand{CameraIdentityID: takeTestCameraID, Command: &workerv1.WorkerCommand{
		CommandId: takeTestStartID, IssuedAt: timestamppb.New(now),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId: takeTestTakeID, RelativePath: "recording/session-1/take-1/camera-1/video.mp4",
		}},
	}}
	if err := store.CreateTake(ctx, repository.StartTakeRequest{
		Take: take, CameraNames: []string{"camera-1"}, Commands: []repository.CameraCommand{start},
	}); err != nil {
		t.Fatal(err)
	}
	finish := repository.CameraCommand{CameraIdentityID: takeTestCameraID, Command: &workerv1.WorkerCommand{
		CommandId: takeTestFinishID, IssuedAt: timestamppb.New(now.Add(time.Second)),
		Command: &workerv1.WorkerCommand_FinishRecording{FinishRecording: &workerv1.FinishRecording{TakeId: takeTestTakeID}},
	}}
	if _, err := store.FinishTake(ctx, repository.FinishTakeRequest{
		SessionName: "session-1", FinishedAt: now.Add(time.Second), Commands: []repository.CameraCommand{finish},
	}); err != nil {
		t.Fatal(err)
	}
	shutdown := repository.CameraCommand{CameraIdentityID: takeTestCameraID, Command: &workerv1.WorkerCommand{
		CommandId: "019c2923-73d4-7df9-a278-624178a7817a", IssuedAt: timestamppb.New(now.Add(2 * time.Second)),
		Command: &workerv1.WorkerCommand_Shutdown{Shutdown: &workerv1.Shutdown{Reason: "delete"}},
	}}
	if err := store.RequestCameraDeletion(ctx, "session-1", "camera-1", shutdown, now.Add(2*time.Second), false); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("RequestCameraDeletion(normal) error = %v, want conflict", err)
	}
	var videoState string
	if err := pool.QueryRow(ctx, `SELECT state FROM video_files WHERE take_id = $1 AND camera_identity_id = $2`,
		takeTestTakeID, takeTestCameraID).Scan(&videoState); err != nil || videoState != "uploading" {
		t.Fatalf("video before force delete = %q, %v", videoState, err)
	}
	if err := store.RequestCameraDeletion(ctx, "session-1", "camera-1", shutdown, now.Add(2*time.Second), true); err != nil {
		t.Fatalf("RequestCameraDeletion(force) error = %v", err)
	}
	var videoError, takeState, takeError string
	if err := pool.QueryRow(ctx, `SELECT state, error FROM video_files WHERE take_id = $1 AND camera_identity_id = $2`,
		takeTestTakeID, takeTestCameraID).Scan(&videoState, &videoError); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state, error FROM takes WHERE id = $1`, takeTestTakeID).Scan(&takeState, &takeError); err != nil {
		t.Fatal(err)
	}
	if videoState != "errored" || videoError != "upload aborted by forced camera deletion" ||
		takeState != "errored" || takeError == "" {
		t.Fatalf("forced deletion states = video %q/%q, take %q/%q", videoState, videoError, takeState, takeError)
	}
}
