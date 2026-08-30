package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

var ErrInvalidArgument = errors.New("application: invalid argument")

type IDGenerator func() (string, error)

type Service struct {
	repository repository.Repository
	now        func() time.Time
	newID      IDGenerator
}

func New(repository repository.Repository) *Service {
	return &Service{
		repository: repository,
		now:        time.Now,
		newID:      domain.NewID,
	}
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
