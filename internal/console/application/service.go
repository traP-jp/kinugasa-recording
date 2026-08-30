package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

var ErrInvalidArgument = errors.New("application: invalid argument")

type IDGenerator func() (string, error)

type Service struct {
	repository   dataRepository
	dispatcher   commandDispatcher
	objectBucket string
	now          func() time.Time
	newID        IDGenerator
}

type dataRepository interface {
	repository.Repository
	repository.TakeRepository
	repository.LockfileRepository
}

type commandDispatcher interface {
	Enqueue(cameraID string, command *workerv1.WorkerCommand) bool
}

func New(repository dataRepository) *Service {
	return &Service{
		repository: repository,
		now:        time.Now,
		newID:      domain.NewID,
	}
}

func (s *Service) WithCommandDispatcher(dispatcher commandDispatcher) *Service {
	s.dispatcher = dispatcher
	return s
}

func (s *Service) WithObjectBucket(bucket string) *Service {
	s.objectBucket = bucket
	return s
}

// WithRuntime replaces nondeterministic runtime dependencies. It is intended
// for tests and deterministic tooling.
func (s *Service) WithRuntime(now func() time.Time, newID IDGenerator) *Service {
	s.now = now
	s.newID = newID
	return s
}

type PageRequest struct {
	Page     int
	PageSize int
}

func (p PageRequest) validate() error {
	if p.Page < 1 {
		return fmt.Errorf("%w: page must be at least 1", ErrInvalidArgument)
	}
	if p.PageSize < 1 || p.PageSize > 100 {
		return fmt.Errorf("%w: pageSize must be between 1 and 100", ErrInvalidArgument)
	}
	return nil
}

type SessionPage struct {
	Items    []domain.Session
	Page     int
	PageSize int
	Total    int64
}

func (s *Service) CreateSession(ctx context.Context, name string) (domain.Session, error) {
	id, err := s.newID()
	if err != nil {
		return domain.Session{}, fmt.Errorf("generate session ID: %w", err)
	}
	session := domain.Session{
		ID:        domain.SessionID(id),
		Name:      name,
		State:     domain.SessionStateActive,
		CreatedAt: s.now().UTC(),
	}
	if err := session.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Service) ListSessions(ctx context.Context, request PageRequest) (SessionPage, error) {
	if err := request.validate(); err != nil {
		return SessionPage{}, err
	}
	page, err := s.repository.ListSessions(ctx, repository.PageRequest{
		Page:     request.Page,
		PageSize: request.PageSize,
	})
	if err != nil {
		return SessionPage{}, err
	}
	return SessionPage{
		Items:    page.Items,
		Page:     request.Page,
		PageSize: request.PageSize,
		Total:    page.Total,
	}, nil
}

func (s *Service) GetSession(ctx context.Context, name string) (repository.SessionDetail, error) {
	return s.repository.GetSession(ctx, name)
}

func (s *Service) CreateCamera(ctx context.Context, sessionName, cameraName string) (repository.Camera, error) {
	session, err := s.repository.GetSession(ctx, sessionName)
	if err != nil {
		return repository.Camera{}, err
	}
	id, err := s.newID()
	if err != nil {
		return repository.Camera{}, fmt.Errorf("generate camera ID: %w", err)
	}
	identity := domain.CameraIdentity{
		ID:        domain.CameraIdentityID(id),
		SessionID: session.Session.ID,
		Name:      cameraName,
		CreatedAt: s.now().UTC(),
	}
	connection := domain.CameraConnection{
		CameraIdentityID: identity.ID,
		Status:           domain.CameraConnectionStatusActivating,
	}
	if err := identity.Validate(); err != nil {
		return repository.Camera{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if err := s.repository.CreateCamera(ctx, identity, connection); err != nil {
		return repository.Camera{}, err
	}
	return repository.Camera{Identity: identity, Connection: connection}, nil
}

func (s *Service) ListCameras(ctx context.Context, sessionName string) ([]repository.Camera, error) {
	return s.repository.ListCameras(ctx, sessionName)
}

func (s *Service) GetCamera(ctx context.Context, sessionName, cameraName string) (repository.Camera, error) {
	return s.repository.GetCamera(ctx, sessionName, cameraName)
}

func (s *Service) DeleteCamera(ctx context.Context, sessionName, cameraName string) error {
	return s.repository.DeleteCamera(ctx, sessionName, cameraName)
}

type OngoingTakeView struct {
	Take        domain.OngoingTake
	CameraNames map[domain.CameraIdentityID]string
}

type FinishedTakePage struct {
	Items    []domain.FinishedTake
	Page     int
	PageSize int
	Total    int64
}

func (s *Service) StartTake(
	ctx context.Context,
	sessionName, takeName string,
	cameraNames []string,
) (OngoingTakeView, error) {
	if len(cameraNames) == 0 {
		return OngoingTakeView{}, fmt.Errorf("%w: cameraNames must not be empty", ErrInvalidArgument)
	}
	session, err := s.repository.GetSession(ctx, sessionName)
	if err != nil {
		return OngoingTakeView{}, err
	}
	takeID, err := s.newID()
	if err != nil {
		return OngoingTakeView{}, err
	}
	now := s.now().UTC()
	take := domain.OngoingTake{
		ID: domain.TakeID(takeID), SessionID: session.Session.ID, Name: takeName, StartedAt: now,
	}
	request := repository.StartTakeRequest{Take: take, CameraNames: append([]string(nil), cameraNames...)}
	view := OngoingTakeView{Take: take, CameraNames: make(map[domain.CameraIdentityID]string, len(cameraNames))}
	seen := make(map[string]struct{}, len(cameraNames))
	for _, cameraName := range cameraNames {
		if _, duplicate := seen[cameraName]; duplicate {
			return OngoingTakeView{}, fmt.Errorf("%w: cameraNames must be unique", ErrInvalidArgument)
		}
		seen[cameraName] = struct{}{}
		camera, err := s.repository.GetCamera(ctx, sessionName, cameraName)
		if err != nil {
			return OngoingTakeView{}, err
		}
		if camera.Connection.Status != domain.CameraConnectionStatusConnected {
			return OngoingTakeView{}, repository.ErrConflict
		}
		commandID, err := s.newID()
		if err != nil {
			return OngoingTakeView{}, err
		}
		recordingCamera := domain.RecordingCamera{
			OngoingTakeID: take.ID, CameraIdentityID: camera.Identity.ID,
			State: domain.RecordingCameraStateRecording, StartedAt: now,
		}
		view.Take.Cameras = append(view.Take.Cameras, recordingCamera)
		request.Take.Cameras = append(request.Take.Cameras, recordingCamera)
		view.CameraNames[camera.Identity.ID] = cameraName
		request.Commands = append(request.Commands, repository.CameraCommand{
			CameraIdentityID: string(camera.Identity.ID),
			Command: &workerv1.WorkerCommand{
				CommandId: commandID, IssuedAt: timestamppb.New(now),
				Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
					TakeId:       string(take.ID),
					RelativePath: fmt.Sprintf("recording/%s/%s/%s/video.mp4", sessionName, takeName, cameraName),
				}},
			},
		})
	}
	if err := view.Take.Validate(); err != nil {
		return OngoingTakeView{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	request.Take = view.Take
	if err := s.repository.CreateTake(ctx, request); err != nil {
		return OngoingTakeView{}, err
	}
	s.dispatch(request.Commands)
	return view, nil
}

func (s *Service) GetOngoingTake(ctx context.Context, sessionName string) (*OngoingTakeView, error) {
	take, err := s.repository.GetOngoingTake(ctx, sessionName)
	if err != nil || take == nil {
		return nil, err
	}
	cameras, err := s.repository.ListCameras(ctx, sessionName)
	if err != nil {
		return nil, err
	}
	view := &OngoingTakeView{Take: *take, CameraNames: make(map[domain.CameraIdentityID]string, len(cameras))}
	for _, camera := range cameras {
		view.CameraNames[camera.Identity.ID] = camera.Identity.Name
	}
	return view, nil
}

func (s *Service) FinishTake(ctx context.Context, sessionName string) (domain.FinishedTake, error) {
	if _, err := s.repository.GetSession(ctx, sessionName); err != nil {
		return domain.FinishedTake{}, err
	}
	ongoing, err := s.repository.GetOngoingTake(ctx, sessionName)
	if err != nil {
		return domain.FinishedTake{}, err
	}
	if ongoing == nil {
		return domain.FinishedTake{}, repository.ErrConflict
	}
	now := s.now().UTC()
	request := repository.FinishTakeRequest{SessionName: sessionName, FinishedAt: now}
	for _, camera := range ongoing.Cameras {
		if camera.State == domain.RecordingCameraStateErrored {
			continue
		}
		commandID, err := s.newID()
		if err != nil {
			return domain.FinishedTake{}, err
		}
		request.Commands = append(request.Commands, repository.CameraCommand{
			CameraIdentityID: string(camera.CameraIdentityID),
			Command: &workerv1.WorkerCommand{
				CommandId: commandID, IssuedAt: timestamppb.New(now),
				Command: &workerv1.WorkerCommand_FinishRecording{FinishRecording: &workerv1.FinishRecording{
					TakeId: string(ongoing.ID),
				}},
			},
		})
	}
	finished, err := s.repository.FinishTake(ctx, request)
	if err != nil {
		return domain.FinishedTake{}, err
	}
	s.dispatch(request.Commands)
	return finished, nil
}

func (s *Service) ListFinishedTakes(ctx context.Context, sessionName string, request PageRequest) (FinishedTakePage, error) {
	if err := request.validate(); err != nil {
		return FinishedTakePage{}, err
	}
	page, err := s.repository.ListFinishedTakes(ctx, sessionName, repository.PageRequest{
		Page: request.Page, PageSize: request.PageSize,
	})
	if err != nil {
		return FinishedTakePage{}, err
	}
	return FinishedTakePage{Items: page.Items, Page: request.Page, PageSize: request.PageSize, Total: page.Total}, nil
}

func (s *Service) GetFinishedTake(ctx context.Context, sessionName, takeName string) (repository.FinishedTakeDetail, error) {
	return s.repository.GetFinishedTake(ctx, sessionName, takeName)
}

type LockfileObject struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Lockfile struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Bucket        string                    `json:"bucket"`
	Objects       map[string]LockfileObject `json:"objects"`
}

func (s *Service) GetLockfile(ctx context.Context, sessionName string) (Lockfile, error) {
	if s.objectBucket == "" {
		return Lockfile{}, errors.New("object bucket is not configured")
	}
	stored, err := s.repository.ListLockfileObjects(ctx, sessionName)
	if err != nil {
		return Lockfile{}, err
	}
	lockfile := Lockfile{SchemaVersion: "1.0", Bucket: s.objectBucket, Objects: make(map[string]LockfileObject, len(stored))}
	for _, object := range stored {
		lockfile.Objects[object.LogicalPath] = LockfileObject{
			Key: object.ObjectKey, SHA256: object.Hash.Hex(), Size: object.Size,
		}
	}
	return lockfile, nil
}

func (s *Service) dispatch(commands []repository.CameraCommand) {
	if s.dispatcher == nil {
		return
	}
	for _, command := range commands {
		s.dispatcher.Enqueue(command.CameraIdentityID, command.Command)
	}
}
