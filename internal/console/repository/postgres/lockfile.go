package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func (s *Store) ListLockfileObjects(ctx context.Context, sessionName string) ([]repository.LockfileObject, error) {
	var sessionID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM sessions WHERE name = $1`, sessionName).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("load lockfile session: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT finalized_recordings.relative_path, video_files.object_key,
		       video_files.hash, video_files.size
		FROM video_files
		JOIN finalized_recordings
		  ON finalized_recordings.take_id = video_files.take_id
		 AND finalized_recordings.camera_identity_id = video_files.camera_identity_id
		WHERE video_files.session_id = $1 AND video_files.state = 'completed'
		ORDER BY finalized_recordings.relative_path`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list lockfile objects: %w", err)
	}
	defer rows.Close()
	objects := make([]repository.LockfileObject, 0)
	for rows.Next() {
		var object repository.LockfileObject
		var hashBytes []byte
		if err := rows.Scan(&object.LogicalPath, &object.ObjectKey, &hashBytes, &object.Size); err != nil {
			return nil, fmt.Errorf("scan lockfile object: %w", err)
		}
		hash, err := domain.ContentHashFromBytes(hashBytes)
		if err != nil {
			return nil, fmt.Errorf("decode lockfile object hash: %w", err)
		}
		object.Hash = hash
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lockfile objects: %w", err)
	}
	return objects, nil
}
