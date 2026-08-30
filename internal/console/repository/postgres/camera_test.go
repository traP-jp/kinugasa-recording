package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

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

	if err := store.DeleteCamera(ctx, session.Name, identity.Name); err != nil {
		t.Fatalf("DeleteCamera() error = %v", err)
	}
	if _, err := store.GetCamera(ctx, session.Name, identity.Name); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetCamera(deleted) error = %v, want ErrNotFound", err)
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
	if len(resources) != 0 {
		t.Fatalf("ListCameraResources(after delete) = %+v, want empty", resources)
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
