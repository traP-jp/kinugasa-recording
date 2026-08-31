package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadprotocol"
)

func (s *Store) ApplyUploadReport(ctx context.Context, report *workerv1.UploadReport) error {
	if err := uploadprotocol.ValidateReport(report); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upload report transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var relativePath, mediaType string
	var startedAt, finishedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT relative_path, media_type, started_at, finished_at
		FROM finalized_recordings
		WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3
		FOR UPDATE`, report.TakeId, report.CameraIdentityId, report.SessionId,
	).Scan(&relativePath, &mediaType, &startedAt, &finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrUploadReportMismatch
	}
	if err != nil {
		return fmt.Errorf("load finalized recording: %w", err)
	}
	if relativePath != report.RelativePath || mediaType != "video/mp4" ||
		!samePostgresTimestamp(startedAt, report.StartedAt.AsTime()) ||
		!samePostgresTimestamp(finishedAt, report.FinishedAt.AsTime()) {
		return repository.ErrUploadReportMismatch
	}

	var currentState string
	var currentObjectKey, currentError *string
	var currentHash []byte
	var currentSize *int64
	err = tx.QueryRow(ctx, `
		SELECT state, object_key, hash, size, error
		FROM video_files
		WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3
		FOR UPDATE`, report.TakeId, report.CameraIdentityId, report.SessionId,
	).Scan(&currentState, &currentObjectKey, &currentHash, &currentSize, &currentError)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrUploadReportMismatch
	}
	if err != nil {
		return fmt.Errorf("load video file: %w", err)
	}
	wantedState := "completed"
	if report.State == workerv1.UploadState_UPLOAD_STATE_ERRORED {
		wantedState = "errored"
	}
	if currentState != "uploading" {
		if !sameUploadResult(currentState, currentObjectKey, currentHash, currentSize, currentError, report) {
			return repository.ErrUploadReportMismatch
		}
		return tx.Commit(ctx)
	}
	var objectKey any
	var hash any
	var size any
	var reportError any
	if wantedState == "completed" {
		objectKey, hash, size = report.ObjectKey, report.Sha256, report.Size
	} else {
		reportError = report.Error
	}
	if _, err := tx.Exec(ctx, `
		UPDATE video_files
		SET state = $4, object_key = $5, hash = $6, size = $7, error = $8
		WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3`,
		report.TakeId, report.CameraIdentityId, report.SessionId,
		wantedState, objectKey, hash, size, reportError,
	); err != nil {
		return fmt.Errorf("apply upload report: %w", err)
	}
	if err := convergeFinishedTake(ctx, tx, report.TakeId, report.SessionId); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upload report: %w", err)
	}
	return nil
}

func samePostgresTimestamp(stored, reported time.Time) bool {
	return stored.Truncate(time.Microsecond).Equal(reported.Truncate(time.Microsecond))
}

func sameUploadResult(
	state string,
	objectKey *string,
	hash []byte,
	size *int64,
	reportError *string,
	report *workerv1.UploadReport,
) bool {
	if report.State == workerv1.UploadState_UPLOAD_STATE_COMPLETED {
		return state == "completed" && objectKey != nil && *objectKey == report.ObjectKey &&
			bytes.Equal(hash, report.Sha256) && size != nil && *size == report.Size && reportError == nil
	}
	return state == "errored" && reportError != nil && *reportError == report.Error
}

func convergeFinishedTake(ctx context.Context, tx pgx.Tx, takeID, sessionID string) error {
	var expected, total, uploading, errored int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM recording_cameras WHERE take_id = $1 AND session_id = $2),
		       count(*),
		       count(*) FILTER (WHERE state = 'uploading'),
		       count(*) FILTER (WHERE state = 'errored')
		FROM video_files WHERE take_id = $1 AND session_id = $2`, takeID, sessionID,
	).Scan(&expected, &total, &uploading, &errored); err != nil {
		return fmt.Errorf("summarize take uploads: %w", err)
	}
	if expected == 0 || total != expected || uploading != 0 {
		return nil
	}
	var updateError error
	if errored != 0 {
		_, updateError = tx.Exec(ctx, `
			UPDATE takes SET state = 'errored', error = 'one or more video uploads failed'
			WHERE id = $1 AND session_id = $2 AND phase = 'finished'`, takeID, sessionID)
	} else {
		_, updateError = tx.Exec(ctx, `
			UPDATE takes SET state = 'completed', error = NULL
			WHERE id = $1 AND session_id = $2 AND phase = 'finished'`, takeID, sessionID)
	}
	if updateError != nil {
		return updateError
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM recording_cameras WHERE take_id = $1 AND session_id = $2`, takeID, sessionID); err != nil {
		return fmt.Errorf("release terminal recording cameras: %w", err)
	}
	return nil
}

func errorUploadingVideoFiles(ctx context.Context, tx pgx.Tx, cameraID, reason string) error {
	rows, err := tx.Query(ctx, `
		UPDATE video_files
		SET state = 'errored', object_key = NULL, hash = NULL, size = NULL, error = $2
		WHERE camera_identity_id = $1 AND state = 'uploading'
		RETURNING take_id::text, session_id::text`, cameraID, reason)
	if err != nil {
		return fmt.Errorf("mark worker uploads errored: %w", err)
	}
	type takeKey struct {
		takeID    string
		sessionID string
	}
	takes := make(map[takeKey]struct{})
	for rows.Next() {
		var key takeKey
		if err := rows.Scan(&key.takeID, &key.sessionID); err != nil {
			rows.Close()
			return fmt.Errorf("scan errored worker upload: %w", err)
		}
		takes[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate errored worker uploads: %w", err)
	}
	rows.Close()
	for key := range takes {
		if err := convergeFinishedTake(ctx, tx, key.takeID, key.sessionID); err != nil {
			return err
		}
	}
	return nil
}
