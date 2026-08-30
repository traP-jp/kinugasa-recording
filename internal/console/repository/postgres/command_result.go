package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (s *Store) SaveCommandResult(
	ctx context.Context,
	workerID string,
	result *workerv1.CommandResult,
) error {
	if err := workerprotocol.ValidateUUID("worker_id", workerID); err != nil {
		return err
	}
	if err := workerprotocol.ValidateCommandResult(result); err != nil {
		return err
	}
	encoded, err := proto.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal command result: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin command result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cameraID string
	if err := tx.QueryRow(ctx, `
		SELECT worker_processes.camera_identity_id::text
		FROM worker_processes
		JOIN camera_connections
		  ON camera_connections.camera_identity_id = worker_processes.camera_identity_id
		 AND camera_connections.video_worker_id = worker_processes.worker_id
		WHERE worker_processes.worker_id = $1
		FOR UPDATE`, workerID,
	).Scan(&cameraID); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrWorkerIdentityMismatch
	} else if err != nil {
		return fmt.Errorf("load worker for command result: %w", err)
	}

	var existingStatus string
	var existingResult []byte
	err = tx.QueryRow(ctx, `
		SELECT status, result
		FROM worker_commands
		WHERE command_id = $1 AND camera_identity_id = $2
		FOR UPDATE`, result.CommandId, cameraID,
	).Scan(&existingStatus, &existingResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrWorkerCommandMismatch
	}
	if err != nil {
		return fmt.Errorf("load worker command: %w", err)
	}
	if existingStatus != "pending" {
		stored := &workerv1.CommandResult{}
		if err := proto.Unmarshal(existingResult, stored); err != nil {
			return fmt.Errorf("unmarshal stored command result: %w", err)
		}
		if !proto.Equal(stored, result) {
			return repository.ErrWorkerCommandMismatch
		}
		return tx.Commit(ctx)
	}

	statusValue, err := commandResultStatus(result.Status)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE worker_commands
		SET status = $2, completed_at = $3, result = $4
		WHERE command_id = $1`,
		result.CommandId, statusValue, result.CompletedAt.AsTime(), encoded,
	); err != nil {
		return fmt.Errorf("save command result: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit command result: %w", err)
	}
	return nil
}

func commandResultStatus(status workerv1.CommandResultStatus) (string, error) {
	switch status {
	case workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED:
		return "applied", nil
	case workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED:
		return "already_applied", nil
	case workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_REJECTED:
		return "rejected", nil
	case workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_FAILED:
		return "failed", nil
	default:
		return "", fmt.Errorf("unsupported command result status %s", status)
	}
}
