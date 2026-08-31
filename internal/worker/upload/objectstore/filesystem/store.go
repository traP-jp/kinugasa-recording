package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/traP-jp/kinugasa-recording/internal/worker/storage"
)

type Store struct{ root string }

func New(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: absolute}, nil
}

func (s *Store) Put(ctx context.Context, key string, source io.ReadSeeker, size int64, _, _ string) error {
	target, err := storage.ResolvePath(s.root, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "object-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	written, err := io.Copy(temporary, &contextReader{ctx: ctx, reader: source})
	if err != nil {
		return err
	}
	if written != size {
		return fmt.Errorf("uploaded object size %d does not match expected %d", written, size)
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(value []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(value)
	}
}
