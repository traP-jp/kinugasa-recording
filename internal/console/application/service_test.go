package application

import (
	"context"
	"errors"
	"testing"
	"time"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
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

func TestStartTakePersistsCommandsBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	repo := &repositoryStub{
		session: repository.SessionDetail{Session: domain.Session{
			ID: "019c293a-f80d-7c30-9f9a-466492fa9320", Name: "session-1", State: domain.SessionStateActive, CreatedAt: now,
		}},
		cameras: map[string]repository.Camera{"camera-1": {
			Identity:   domain.CameraIdentity{ID: "019c293b-0362-7823-ac1e-83cbc6ba195d", Name: "camera-1"},
			Connection: domain.CameraConnection{Status: domain.CameraConnectionStatusConnected},
		}},
	}
	ids := []string{"019c293b-0b88-7bd7-beb9-f6ad39c9384d", "019c293b-13b8-7818-bbd1-d69065ee71a9"}
	dispatcher := &dispatcherStub{}
	service := New(repo).WithRuntime(func() time.Time { return now }, func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}).WithCommandDispatcher(dispatcher)
	view, err := service.StartTake(context.Background(), "session-1", "take-1", []string{"camera-1"})
	if err != nil {
		t.Fatalf("StartTake() error = %v", err)
	}
	if view.Take.Name != "take-1" || len(repo.startTake.Commands) != 1 || len(dispatcher.commands) != 1 {
		t.Fatalf("StartTake() = %+v, repository = %+v, dispatch = %+v", view, repo.startTake, dispatcher.commands)
	}
	start := repo.startTake.Commands[0].Command.GetStartRecording()
	if start == nil || start.RelativePath != "recording/session-1/take-1/camera-1/video.mp4" {
		t.Fatalf("StartRecording command = %+v", start)
	}
}

func TestFinishTakeOnlyDispatchesToRecordingCameras(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 30, 0, 0, time.UTC)
	repo := &repositoryStub{
		session: repository.SessionDetail{Session: domain.Session{ID: "019c293a-f80d-7c30-9f9a-466492fa9320"}},
		ongoingTake: &domain.OngoingTake{
			ID: "019c293b-0b88-7bd7-beb9-f6ad39c9384d",
			Cameras: []domain.RecordingCamera{
				{CameraIdentityID: "019c293b-0362-7823-ac1e-83cbc6ba195d", State: domain.RecordingCameraStateRecording},
				{CameraIdentityID: "019c293b-13b8-7818-bbd1-d69065ee71a9", State: domain.RecordingCameraStateErrored},
			},
		},
	}
	dispatcher := &dispatcherStub{}
	service := New(repo).WithRuntime(func() time.Time { return now }, func() (string, error) {
		return "019c293b-1ee3-718f-9420-dd1f1ff16c5b", nil
	}).WithCommandDispatcher(dispatcher)

	if _, err := service.FinishTake(context.Background(), "session-1"); err != nil {
		t.Fatalf("FinishTake() error = %v", err)
	}
	if len(repo.finishTake.Commands) != 1 || len(dispatcher.commands) != 1 {
		t.Fatalf("finish commands = %d, dispatched = %d", len(repo.finishTake.Commands), len(dispatcher.commands))
	}
	if got := repo.finishTake.Commands[0].CameraIdentityID; got != "019c293b-0362-7823-ac1e-83cbc6ba195d" {
		t.Fatalf("finish camera ID = %q", got)
	}
}

type repositoryStub struct {
	createdSession domain.Session
	createdCamera  repository.Camera
	session        repository.SessionDetail
	cameras        map[string]repository.Camera
	startTake      repository.StartTakeRequest
	finishTake     repository.FinishTakeRequest
	ongoingTake    *domain.OngoingTake
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
	for _, camera := range r.cameras {
		return camera, nil
	}
	return repository.Camera{}, repository.ErrNotFound
}

func (r *repositoryStub) DeleteCamera(context.Context, string, string) error {
	return nil
}

func (r *repositoryStub) CreateTake(_ context.Context, request repository.StartTakeRequest) error {
	r.startTake = request
	return nil
}

func (r *repositoryStub) GetOngoingTake(context.Context, string) (*domain.OngoingTake, error) {
	return r.ongoingTake, nil
}

type dispatcherStub struct{ commands []*workerv1.WorkerCommand }

func (d *dispatcherStub) Enqueue(_ string, command *workerv1.WorkerCommand) bool {
	d.commands = append(d.commands, command)
	return true
}

func (r *repositoryStub) FinishTake(_ context.Context, request repository.FinishTakeRequest) (domain.FinishedTake, error) {
	r.finishTake = request
	return domain.FinishedTake{}, nil
}

func (r *repositoryStub) ListFinishedTakes(context.Context, string, repository.PageRequest) (repository.FinishedTakePage, error) {
	return repository.FinishedTakePage{}, nil
}

func (r *repositoryStub) GetFinishedTake(context.Context, string, string) (repository.FinishedTakeDetail, error) {
	return repository.FinishedTakeDetail{}, nil
}
