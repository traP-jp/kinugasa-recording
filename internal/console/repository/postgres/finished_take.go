package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func (s *Store) ListFinishedTakes(
	ctx context.Context,
	sessionName string,
	page repository.PageRequest,
) (repository.FinishedTakePage, error) {
	var sessionID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM sessions WHERE name = $1`, sessionName).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return repository.FinishedTakePage{}, repository.ErrNotFound
	} else if err != nil {
		return repository.FinishedTakePage{}, fmt.Errorf("load session for finished takes: %w", err)
	}
	result := repository.FinishedTakePage{Items: make([]domain.FinishedTake, 0)}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM takes WHERE session_id = $1 AND phase = 'finished'`, sessionID,
	).Scan(&result.Total); err != nil {
		return repository.FinishedTakePage{}, fmt.Errorf("count finished takes: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, session_id::text, name, state, started_at, finished_at, coalesce(error, '')
		FROM takes
		WHERE session_id = $1 AND phase = 'finished'
		ORDER BY finished_at DESC, name ASC
		LIMIT $2 OFFSET $3`, sessionID, page.PageSize, page.Offset())
	if err != nil {
		return repository.FinishedTakePage{}, fmt.Errorf("list finished takes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var take domain.FinishedTake
		if err := rows.Scan(
			&take.ID, &take.SessionID, &take.Name, &take.State,
			&take.StartedAt, &take.FinishedAt, &take.Error,
		); err != nil {
			return repository.FinishedTakePage{}, fmt.Errorf("scan finished take: %w", err)
		}
		result.Items = append(result.Items, take)
	}
	if err := rows.Err(); err != nil {
		return repository.FinishedTakePage{}, fmt.Errorf("iterate finished takes: %w", err)
	}
	return result, nil
}

func (s *Store) GetFinishedTake(
	ctx context.Context,
	sessionName, takeName string,
) (repository.FinishedTakeDetail, error) {
	result := repository.FinishedTakeDetail{CameraNames: make(map[domain.CameraIdentityID]string)}
	err := s.pool.QueryRow(ctx, `
		SELECT takes.id::text, takes.session_id::text, takes.name, takes.state,
		       takes.started_at, takes.finished_at, coalesce(takes.error, '')
		FROM takes JOIN sessions ON sessions.id = takes.session_id
		WHERE sessions.name = $1 AND takes.name = $2 AND takes.phase = 'finished'`,
		sessionName, takeName,
	).Scan(
		&result.Take.ID, &result.Take.SessionID, &result.Take.Name, &result.Take.State,
		&result.Take.StartedAt, &result.Take.FinishedAt, &result.Take.Error,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.FinishedTakeDetail{}, repository.ErrNotFound
	}
	if err != nil {
		return repository.FinishedTakeDetail{}, fmt.Errorf("get finished take: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT video_files.camera_identity_id::text, camera_identities.name,
		       video_files.state, video_files.started_at, video_files.finished_at,
		       video_files.object_key, video_files.hash, video_files.size, video_files.error
		FROM video_files
		JOIN camera_identities ON camera_identities.id = video_files.camera_identity_id
		WHERE video_files.take_id = $1
		ORDER BY camera_identities.name`, result.Take.ID)
	if err != nil {
		return repository.FinishedTakeDetail{}, fmt.Errorf("list finished take video files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file domain.VideoFile
		var cameraName string
		var objectKey, errorReason *string
		var hashBytes []byte
		if err := rows.Scan(
			&file.CameraIdentityID, &cameraName, &file.State, &file.StartedAt, &file.FinishedAt,
			&objectKey, &hashBytes, &file.Size, &errorReason,
		); err != nil {
			return repository.FinishedTakeDetail{}, fmt.Errorf("scan video file: %w", err)
		}
		file.FinishedTakeID = result.Take.ID
		if objectKey != nil {
			file.ObjectKey = *objectKey
		}
		if hashBytes != nil {
			hash, err := domain.ContentHashFromBytes(hashBytes)
			if err != nil {
				return repository.FinishedTakeDetail{}, fmt.Errorf("decode video file hash: %w", err)
			}
			file.Hash = &hash
		}
		if errorReason != nil {
			file.Error = *errorReason
		}
		result.Take.VideoFiles = append(result.Take.VideoFiles, file)
		result.CameraNames[file.CameraIdentityID] = cameraName
	}
	if err := rows.Err(); err != nil {
		return repository.FinishedTakeDetail{}, fmt.Errorf("iterate video files: %w", err)
	}
	if err := result.Take.Validate(); err != nil {
		return repository.FinishedTakeDetail{}, fmt.Errorf("validate stored finished take: %w", err)
	}
	return result, nil
}
