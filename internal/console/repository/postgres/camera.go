package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		WHERE sessions.name = $1 AND camera_connections.deletion_requested_at IS NULL
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
		       sessions.name,
		       camera_connections.deletion_requested_at IS NOT NULL
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
			&resource.Deleting,
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
		WHERE sessions.name = $1 AND camera_identities.name = $2
		  AND camera_connections.deletion_requested_at IS NULL`, sessionName, cameraName)
	camera, err := scanCamera(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.Camera{}, repository.ErrNotFound
	}
	return camera, err
}

func (s *Store) RequestCameraDeletion(
	ctx context.Context,
	sessionName, cameraName string,
	command repository.CameraCommand,
	requestedAt time.Time,
	force bool,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin camera deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cameraID string
	err = tx.QueryRow(ctx, `
		SELECT camera_connections.camera_identity_id::text
		FROM camera_connections
		JOIN camera_identities ON camera_identities.id = camera_connections.camera_identity_id
		JOIN sessions ON sessions.id = camera_identities.session_id
		WHERE sessions.name = $1 AND camera_identities.name = $2
		  AND camera_connections.deletion_requested_at IS NULL
		FOR UPDATE`, sessionName, cameraName,
	).Scan(&cameraID)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load camera for deletion: %w", err)
	}
	var recording bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM recording_cameras
			JOIN takes ON takes.id = recording_cameras.take_id
			WHERE recording_cameras.camera_identity_id = $1 AND takes.phase = 'ongoing'
		)`, cameraID,
	).Scan(&recording); err != nil {
		return fmt.Errorf("check active recording before camera deletion: %w", err)
	}
	if recording {
		return repository.ErrConflict
	}
	if command.CameraIdentityID != cameraID || command.Command.GetShutdown() == nil {
		return repository.ErrConflict
	}
	var uploading bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM video_files
			WHERE camera_identity_id = $1 AND state = 'uploading'
		)`, cameraID,
	).Scan(&uploading); err != nil {
		return fmt.Errorf("check active uploads before camera deletion: %w", err)
	}
	if uploading && !force {
		return repository.ErrConflict
	}
	if uploading {
		if err := errorUploadingVideoFiles(ctx, tx, cameraID, "upload aborted by forced camera deletion"); err != nil {
			return err
		}
	}
	if err := insertWorkerCommand(ctx, tx, command); err != nil {
		return fmt.Errorf("persist camera shutdown command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE camera_connections SET deletion_requested_at = $2
		WHERE camera_identity_id = $1`, cameraID, requestedAt.UTC(),
	); err != nil {
		return fmt.Errorf("request camera deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit camera deletion request: %w", err)
	}
	return nil
}

func (s *Store) CompleteCameraDeletion(ctx context.Context, cameraID string) error {
	commandTag, err := s.pool.Exec(ctx, `
		DELETE FROM camera_connections
		WHERE camera_identity_id = $1 AND deletion_requested_at IS NOT NULL`, cameraID)
	if err != nil {
		return fmt.Errorf("complete camera deletion: %w", classifyError(err))
	}
	if commandTag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (s *Store) ActivateCameraConnection(ctx context.Context, cameraID, cameraURL string) error {
	commandTag, err := s.pool.Exec(ctx, `
		UPDATE camera_connections
		SET url = $2,
		    status = CASE WHEN status = 'activating' THEN 'waiting' ELSE status END,
		    error = CASE WHEN status = 'activating' THEN NULL ELSE error END
		WHERE camera_identity_id = $1
		  AND deletion_requested_at IS NULL`, cameraID, cameraURL)
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
