package repository

import (
	"context"
	"time"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
)

type CameraCommand struct {
	CameraIdentityID string
	Command          *workerv1.WorkerCommand
}

type StartTakeRequest struct {
	Take        domain.OngoingTake
	CameraNames []string
	Commands    []CameraCommand
}

type FinishTakeRequest struct {
	SessionName string
	FinishedAt  time.Time
	Commands    []CameraCommand
}

type TakeRepository interface {
	CreateTake(context.Context, StartTakeRequest) error
	GetOngoingTake(context.Context, string) (*domain.OngoingTake, error)
	FinishTake(context.Context, FinishTakeRequest) (domain.FinishedTake, error)
}
