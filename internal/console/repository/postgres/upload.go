package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadprotocol"
)

func (s *Store) ApplyUploadReport(ctx context.Context, report *upv1.UploadReport) error {
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
		!startedAt.Equal(report.StartedAt.AsTime()) || !finishedAt.Equal(report.FinishedAt.AsTime()) {
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
	if report.State == upv1.UploadState_UPLOAD_STATE_ERRORED {
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

func sameUploadResult(
	state string,
	objectKey *string,
	hash []byte,
	size *int64,
	reportError *string,
	report *upv1.UploadReport,
) bool {
	if report.State == upv1.UploadState_UPLOAD_STATE_COMPLETED {
		return state == "completed" && objectKey != nil && *objectKey == report.ObjectKey &&
			bytes.Equal(hash, report.Sha256) && size != nil && *size == report.Size && reportError == nil
	}
	return state == "errored" && reportError != nil && *reportError == report.Error
}

func convergeFinishedTake(ctx context.Context, tx pgx.Tx, takeID, sessionID string) error {
	var uploading, errored int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'uploading'),
		       count(*) FILTER (WHERE state = 'errored')
		FROM video_files
		WHERE take_id = $1 AND session_id = $2`, takeID, sessionID,
	).Scan(&uploading, &errored); err != nil {
		return fmt.Errorf("summarize take uploads: %w", err)
	}
	if uploading != 0 {
		return nil
	}
	if errored != 0 {
		_, err := tx.Exec(ctx, `
			UPDATE takes SET state = 'errored', error = 'one or more video uploads failed'
			WHERE id = $1 AND session_id = $2 AND phase = 'finished'`, takeID, sessionID)
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE takes SET state = 'completed', error = NULL
		WHERE id = $1 AND session_id = $2 AND phase = 'finished'`, takeID, sessionID)
	return err
}
