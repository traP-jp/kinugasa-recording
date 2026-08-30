package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func (s *Store) CreateCamera(
	ctx context.Context,
	identity domain.CameraIdentity,
	connection domain.CameraConnection,
) error {
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("validate camera identity: %w", err)
	}
	if err := connection.Validate(); err != nil {
		return fmt.Errorf("validate camera connection: %w", err)
	}
	if connection.CameraIdentityID != identity.ID {
		return fmt.Errorf("validate camera: %w", &domain.ValidationError{
			Field:  "connection.cameraIdentityId",
			Reason: "must match the camera identity",
		})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin camera transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO camera_identities (id, session_id, name, created_at)
		VALUES ($1, $2, $3, $4)`,
		identity.ID, identity.SessionID, identity.Name, identity.CreatedAt,
	); err != nil {
		return fmt.Errorf("create camera identity: %w", classifyMissingParent(err, "camera_identities_session_id_fkey"))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO camera_connections (camera_identity_id, url, status, error, video_worker_id)
		VALUES ($1, nullif($2, ''), $3, nullif($4, ''), nullif($5, '')::uuid)`,
		connection.CameraIdentityID,
		connection.URL,
		connection.Status,
		connection.Error,
		connection.VideoWorkerID,
	); err != nil {
		return fmt.Errorf("create camera connection: %w", classifyError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit camera transaction: %w", classifyError(err))
	}
	return nil
}

func (s *Store) ListCameras(ctx context.Context, sessionName string) ([]repository.Camera, error) {
	var sessionExists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sessions WHERE name = $1)`, sessionName,
	).Scan(&sessionExists); err != nil {
		return nil, fmt.Errorf("find camera session: %w", err)
	}
	if !sessionExists {
		return nil, repository.ErrNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT camera_identities.id, camera_identities.session_id,
		       camera_identities.name, camera_identities.created_at,
		       coalesce(camera_connections.url, ''), camera_connections.status,
		       coalesce(camera_connections.error, ''),
		       coalesce(camera_connections.video_worker_id::text, '')
		FROM camera_identities
		JOIN sessions ON sessions.id = camera_identities.session_id
		JOIN camera_connections ON camera_connections.camera_identity_id = camera_identities.id
		WHERE sessions.name = $1
		ORDER BY camera_identities.created_at ASC, camera_identities.name ASC`, sessionName)
	if err != nil {
		return nil, fmt.Errorf("list cameras: %w", err)
	}
	defer rows.Close()

	result := make([]repository.Camera, 0)
	for rows.Next() {
		camera, err := scanCamera(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, camera)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cameras: %w", err)
	}
	return result, nil
}

func (s *Store) ListCameraResources(ctx context.Context) ([]repository.CameraResource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT camera_identities.id, camera_identities.session_id,
		       camera_identities.name, camera_identities.created_at,
		       coalesce(camera_connections.url, ''), camera_connections.status,
		       coalesce(camera_connections.error, ''),
		       coalesce(camera_connections.video_worker_id::text, ''),
		       sessions.name
		FROM camera_identities
		JOIN sessions ON sessions.id = camera_identities.session_id
		JOIN camera_connections ON camera_connections.camera_identity_id = camera_identities.id
		ORDER BY camera_identities.id`)
	if err != nil {
		return nil, fmt.Errorf("list camera resources: %w", err)
	}
	defer rows.Close()

	result := make([]repository.CameraResource, 0)
	for rows.Next() {
		var resource repository.CameraResource
		if err := rows.Scan(
			&resource.Identity.ID,
			&resource.Identity.SessionID,
			&resource.Identity.Name,
			&resource.Identity.CreatedAt,
			&resource.Connection.URL,
			&resource.Connection.Status,
			&resource.Connection.Error,
			&resource.Connection.VideoWorkerID,
			&resource.SessionName,
		); err != nil {
			return nil, fmt.Errorf("scan camera resource: %w", err)
		}
		resource.Connection.CameraIdentityID = resource.Identity.ID
		result = append(result, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate camera resources: %w", err)
	}
	return result, nil
}

func (s *Store) GetCamera(ctx context.Context, sessionName, cameraName string) (repository.Camera, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT camera_identities.id, camera_identities.session_id,
		       camera_identities.name, camera_identities.created_at,
		       coalesce(camera_connections.url, ''), camera_connections.status,
		       coalesce(camera_connections.error, ''),
		       coalesce(camera_connections.video_worker_id::text, '')
		FROM camera_identities
		JOIN sessions ON sessions.id = camera_identities.session_id
		JOIN camera_connections ON camera_connections.camera_identity_id = camera_identities.id
		WHERE sessions.name = $1 AND camera_identities.name = $2`, sessionName, cameraName)
	camera, err := scanCamera(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.Camera{}, repository.ErrNotFound
	}
	return camera, err
}

func (s *Store) DeleteCamera(ctx context.Context, sessionName, cameraName string) error {
	commandTag, err := s.pool.Exec(ctx, `
		DELETE FROM camera_connections
		USING camera_identities, sessions
		WHERE camera_connections.camera_identity_id = camera_identities.id
		  AND camera_identities.session_id = sessions.id
		  AND sessions.name = $1
		  AND camera_identities.name = $2`, sessionName, cameraName)
	if err != nil {
		return fmt.Errorf("delete camera: %w", classifyError(err))
	}
	if commandTag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) ActivateCameraConnection(ctx context.Context, cameraID, cameraURL string) error {
	commandTag, err := s.pool.Exec(ctx, `
		UPDATE camera_connections SET url = $2, status = 'waiting', error = NULL
		WHERE camera_identity_id = $1 AND status = 'activating'`, cameraID, cameraURL)
	if err != nil {
		return fmt.Errorf("activate camera connection: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM camera_connections WHERE camera_identity_id = $1)`, cameraID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("find camera connection for activation: %w", err)
		}
		if !exists {
			return repository.ErrNotFound
		}
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanCamera(row rowScanner) (repository.Camera, error) {
	var camera repository.Camera
	err := row.Scan(
		&camera.Identity.ID,
		&camera.Identity.SessionID,
		&camera.Identity.Name,
		&camera.Identity.CreatedAt,
		&camera.Connection.URL,
		&camera.Connection.Status,
		&camera.Connection.Error,
		&camera.Connection.VideoWorkerID,
	)
	if err != nil {
		return repository.Camera{}, fmt.Errorf("scan camera: %w", err)
	}
	camera.Connection.CameraIdentityID = camera.Identity.ID
	return camera, nil
}
