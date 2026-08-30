package repository

import (
	"context"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
)

type LockfileObject struct {
	LogicalPath string
	ObjectKey   string
	Hash        domain.ContentHash
	Size        int64
}

type LockfileRepository interface {
	ListLockfileObjects(context.Context, string) ([]LockfileObject, error)
}
