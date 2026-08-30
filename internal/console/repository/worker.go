package repository

import (
	"context"
	"errors"
	"time"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

var (
	ErrWorkerIdentityMismatch = errors.New("repository: worker identity mismatch")
	ErrWorkerEventSequence    = errors.New("repository: worker event sequence mismatch")
	ErrWorkerStateMismatch    = errors.New("repository: worker state mismatch")
)

type WorkerControlRepository interface {
	RegisterWorker(context.Context, *workerv1.WorkerHello, time.Time) error
	ApplyWorkerEvent(context.Context, string, *workerv1.WorkerEvent) error
}
