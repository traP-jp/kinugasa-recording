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

func (s *Store) PendingWorkerCommands(ctx context.Context, workerID string) ([]*workerv1.WorkerCommand, error) {
	var cameraID string
	err := s.pool.QueryRow(ctx, `
		SELECT camera_connections.camera_identity_id::text
		FROM camera_connections
		WHERE video_worker_id = $1`, workerID,
	).Scan(&cameraID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrWorkerIdentityMismatch
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT payload FROM worker_commands
		WHERE camera_identity_id = $1 AND status = 'pending'
		ORDER BY issued_at, command_id`, cameraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var commands []*workerv1.WorkerCommand
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		command := &workerv1.WorkerCommand{}
		if err := proto.Unmarshal(encoded, command); err != nil {
			return nil, fmt.Errorf("decode pending worker command: %w", err)
		}
		if err := workerprotocol.ValidateWorkerCommand(command); err != nil {
			return nil, fmt.Errorf("validate pending worker command: %w", err)
		}
		commands = append(commands, command)
	}
	return commands, rows.Err()
}
