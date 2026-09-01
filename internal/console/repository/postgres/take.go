package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (s *Store) CreateTake(ctx context.Context, request repository.StartTakeRequest) error {
	if err := request.Take.Validate(); err != nil {
		return err
	}
	if len(request.CameraNames) != len(request.Take.Cameras) || len(request.Commands) != len(request.Take.Cameras) {
		return fmt.Errorf("take cameras, names, and commands must have equal lengths")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sessionState string
	err = tx.QueryRow(ctx, `SELECT state FROM sessions WHERE id = $1 FOR UPDATE`, request.Take.SessionID).Scan(&sessionState)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	}
	if err != nil {
		return err
	}
	if sessionState != "active" {
		return repository.ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO takes (id, session_id, name, phase, started_at)
		VALUES ($1, $2, $3, 'ongoing', $4)`,
		request.Take.ID, request.Take.SessionID, request.Take.Name, request.Take.StartedAt,
	); err != nil {
		return classifyError(err)
	}
	for index, camera := range request.Take.Cameras {
		var status string
		err := tx.QueryRow(ctx, `
			SELECT camera_connections.status
			FROM camera_identities JOIN camera_connections ON camera_connections.camera_identity_id = camera_identities.id
			WHERE camera_identities.id = $1 AND camera_identities.session_id = $2 AND camera_identities.name = $3
			FOR UPDATE`, camera.CameraIdentityID, request.Take.SessionID, request.CameraNames[index],
		).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrNotFound
		}
		if err != nil {
			return err
		}
		start := request.Commands[index].Command.GetStartRecording()
		if status != "connected" || request.Commands[index].CameraIdentityID != string(camera.CameraIdentityID) ||
			start == nil || start.TakeId != string(request.Take.ID) {
			return repository.ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO recording_cameras (take_id, camera_identity_id, session_id, state, started_at)
			VALUES ($1, $2, $3, 'recording', $4)`,
			request.Take.ID, camera.CameraIdentityID, request.Take.SessionID, camera.StartedAt,
		); err != nil {
			return err
		}
		if err := insertWorkerCommand(ctx, tx, request.Commands[index]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) GetOngoingTake(ctx context.Context, sessionName string) (*domain.OngoingTake, error) {
	var take domain.OngoingTake
	err := s.pool.QueryRow(ctx, `
		SELECT takes.id::text, takes.session_id::text, takes.name, takes.started_at
		FROM takes JOIN sessions ON sessions.id = takes.session_id
		WHERE sessions.name = $1 AND takes.phase = 'ongoing'`, sessionName,
	).Scan(&take.ID, &take.SessionID, &take.Name, &take.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if scanError := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE name = $1)`, sessionName).Scan(&exists); scanError != nil {
			return nil, scanError
		}
		if !exists {
			return nil, repository.ErrNotFound
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT take_id::text, camera_identity_id::text, state, started_at, coalesce(error, '')
		FROM recording_cameras WHERE take_id = $1 ORDER BY camera_identity_id`, take.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var camera domain.RecordingCamera
		if err := rows.Scan(&camera.OngoingTakeID, &camera.CameraIdentityID, &camera.State, &camera.StartedAt, &camera.Error); err != nil {
			return nil, err
		}
		take.Cameras = append(take.Cameras, camera)
	}
	return &take, rows.Err()
}

func (s *Store) FinishTake(ctx context.Context, request repository.FinishTakeRequest) (domain.FinishedTake, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FinishedTake{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var take domain.FinishedTake
	err = tx.QueryRow(ctx, `
		SELECT takes.id::text, takes.session_id::text, takes.name, takes.started_at
		FROM takes JOIN sessions ON sessions.id = takes.session_id
		WHERE sessions.name = $1 AND takes.phase = 'ongoing' FOR UPDATE`, request.SessionName,
	).Scan(&take.ID, &take.SessionID, &take.Name, &take.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FinishedTake{}, repository.ErrConflict
	}
	if err != nil {
		return domain.FinishedTake{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT camera_identity_id::text, state, started_at, coalesce(error, '')
		FROM recording_cameras WHERE take_id = $1 FOR UPDATE`, take.ID)
	if err != nil {
		return domain.FinishedTake{}, err
	}
	expected := make(map[string]struct{})
	type cameraState struct {
		id        string
		state     string
		startedAt time.Time
		error     string
	}
	var cameras []cameraState
	for rows.Next() {
		var camera cameraState
		if err := rows.Scan(&camera.id, &camera.state, &camera.startedAt, &camera.error); err != nil {
			rows.Close()
			return domain.FinishedTake{}, err
		}
		cameras = append(cameras, camera)
		if camera.state == "recording" {
			expected[camera.id] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.FinishedTake{}, err
	}
	if len(expected) != len(request.Commands) {
		return domain.FinishedTake{}, repository.ErrConflict
	}
	for _, command := range request.Commands {
		if _, ok := expected[command.CameraIdentityID]; !ok || command.Command.GetFinishRecording() == nil ||
			command.Command.GetFinishRecording().TakeId != string(take.ID) {
			return domain.FinishedTake{}, repository.ErrConflict
		}
		delete(expected, command.CameraIdentityID)
		if err := insertWorkerCommand(ctx, tx, command); err != nil {
			return domain.FinishedTake{}, err
		}
	}
	for _, camera := range cameras {
		state := "uploading"
		var errorReason any
		if camera.state == "errored" {
			state = "errored"
			errorReason = camera.error
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO video_files (
				take_id, camera_identity_id, session_id, state, started_at, finished_at, error
			) VALUES ($1, $2, $3, $4, $5::timestamptz, greatest($5::timestamptz, $6::timestamptz), $7)`,
			take.ID, camera.id, take.SessionID, state, camera.startedAt, request.FinishedAt, errorReason,
		); err != nil {
			return domain.FinishedTake{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE takes SET phase = 'finished', state = 'uploading', finished_at = $2
		WHERE id = $1`, take.ID, request.FinishedAt,
	); err != nil {
		return domain.FinishedTake{}, err
	}
	if err := convergeFinishedTake(ctx, tx, string(take.ID), string(take.SessionID)); err != nil {
		return domain.FinishedTake{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT state, coalesce(error, '') FROM takes WHERE id = $1`, take.ID,
	).Scan(&take.State, &take.Error); err != nil {
		return domain.FinishedTake{}, err
	}
	take.FinishedAt = request.FinishedAt
	if err := tx.Commit(ctx); err != nil {
		return domain.FinishedTake{}, err
	}
	return take, nil
}

func insertWorkerCommand(ctx context.Context, tx pgx.Tx, command repository.CameraCommand) error {
	if err := workerprotocol.ValidateWorkerCommand(command.Command); err != nil {
		return err
	}
	encoded, err := proto.Marshal(command.Command)
	if err != nil {
		return err
	}
	var takeID any
	if start := command.Command.GetStartRecording(); start != nil {
		takeID = start.TakeId
	} else if finish := command.Command.GetFinishRecording(); finish != nil {
		takeID = finish.TakeId
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO worker_commands (command_id, camera_identity_id, take_id, issued_at, payload)
		VALUES ($1, $2, $3, $4, $5)`, command.Command.CommandId, command.CameraIdentityID,
		takeID, command.Command.IssuedAt.AsTime(), encoded)
	return err
}
