package repository

import (
	"context"
	"errors"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
)

var (
	ErrNotFound = errors.New("repository: not found")
	ErrConflict = errors.New("repository: conflict")
)

type PageRequest struct {
	Page     int
	PageSize int
}

func (p PageRequest) Offset() int {
	return (p.Page - 1) * p.PageSize
}

type SessionPage struct {
	Items []domain.Session
	Total int64
}

type SessionDetail struct {
	Session         domain.Session
	OngoingTakeName string
}

type Camera struct {
	Identity   domain.CameraIdentity
	Connection domain.CameraConnection
}

type CameraResource struct {
	Camera
	SessionName string
}

type Repository interface {
	CreateSession(context.Context, domain.Session) error
	ListSessions(context.Context, PageRequest) (SessionPage, error)
	GetSession(context.Context, string) (SessionDetail, error)

	CreateCamera(context.Context, domain.CameraIdentity, domain.CameraConnection) error
	ListCameras(context.Context, string) ([]Camera, error)
	GetCamera(context.Context, string, string) (Camera, error)
	DeleteCamera(context.Context, string, string) error
}
