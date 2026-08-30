package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func TestCreateSession(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.FixedZone("test", 9*60*60))
	repo := &repositoryStub{}
	service := New(repo).WithRuntime(
		func() time.Time { return now },
		func() (string, error) { return "019c240d-a6de-7de0-a826-0f26e8803fc0", nil },
	)

	session, err := service.CreateSession(context.Background(), "studio-a")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if session.State != domain.SessionStateActive || !session.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("CreateSession() = %+v", session)
	}
	if repo.createdSession != session {
		t.Fatalf("repository session = %+v, want %+v", repo.createdSession, session)
	}
}

func TestCreateSessionRejectsInvalidName(t *testing.T) {
	repo := &repositoryStub{}
	service := New(repo).WithRuntime(
		time.Now,
		func() (string, error) { return "019c240d-a6de-7de0-a826-0f26e8803fc0", nil },
	)

	if _, err := service.CreateSession(context.Background(), "Invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateSession() error = %v, want ErrInvalidArgument", err)
	}
}

func TestListSessionsValidatesPagination(t *testing.T) {
	service := New(&repositoryStub{})

	if _, err := service.ListSessions(context.Background(), PageRequest{Page: 0, PageSize: 20}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListSessions() error = %v, want ErrInvalidArgument", err)
	}
	if _, err := service.ListSessions(context.Background(), PageRequest{Page: 1, PageSize: 101}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListSessions() error = %v, want ErrInvalidArgument", err)
	}
}

type repositoryStub struct {
	createdSession domain.Session
	createdCamera  repository.Camera
	session        repository.SessionDetail
}

func (r *repositoryStub) CreateSession(_ context.Context, session domain.Session) error {
	r.createdSession = session
	return nil
}

func (r *repositoryStub) ListSessions(context.Context, repository.PageRequest) (repository.SessionPage, error) {
	return repository.SessionPage{}, nil
}

func (r *repositoryStub) GetSession(context.Context, string) (repository.SessionDetail, error) {
	return r.session, nil
}

func (r *repositoryStub) CreateCamera(_ context.Context, identity domain.CameraIdentity, connection domain.CameraConnection) error {
	r.createdCamera = repository.Camera{Identity: identity, Connection: connection}
	return nil
}

func (r *repositoryStub) ListCameras(context.Context, string) ([]repository.Camera, error) {
	return nil, nil
}

func (r *repositoryStub) GetCamera(context.Context, string, string) (repository.Camera, error) {
	return repository.Camera{}, nil
}

func (r *repositoryStub) DeleteCamera(context.Context, string, string) error {
	return nil
}
