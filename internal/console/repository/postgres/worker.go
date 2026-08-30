package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (s *Store) RegisterWorker(ctx context.Context, hello *workerv1.WorkerHello, registeredAt time.Time) error {
	if err := workerprotocol.ValidateWorkerHello(hello); err != nil {
		return fmt.Errorf("validate worker hello: %w", err)
	}
	snapshot, err := proto.Marshal(hello.Snapshot)
	if err != nil {
		return fmt.Errorf("marshal worker snapshot: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin worker registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cameraExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM camera_identities
			JOIN camera_connections ON camera_connections.camera_identity_id = camera_identities.id
			WHERE camera_identities.id = $1 AND camera_identities.session_id = $2
		)`, hello.CameraIdentityId, hello.SessionId,
	).Scan(&cameraExists); err != nil {
		return fmt.Errorf("verify worker identity: %w", err)
	}
	if !cameraExists {
		return repository.ErrWorkerIdentityMismatch
	}

	var existingSessionID, existingCameraID string
	var existingSnapshotSequence, existingEventSequence uint64
	err = tx.QueryRow(ctx, `
		SELECT worker_processes.session_id::text,
		       worker_processes.camera_identity_id::text,
		       worker_processes.last_snapshot_sequence,
		       worker_processes.last_event_sequence
		FROM worker_processes
		WHERE worker_id = $1
		FOR UPDATE`, hello.WorkerId,
	).Scan(&existingSessionID, &existingCameraID, &existingSnapshotSequence, &existingEventSequence)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, `
			INSERT INTO worker_processes (
				worker_id, session_id, camera_identity_id,
				first_registered_at, last_registered_at,
				last_snapshot_sequence, last_event_sequence, snapshot
			) VALUES ($1, $2, $3, $4, $4, $5, $5, $6)`,
			hello.WorkerId,
			hello.SessionId,
			hello.CameraIdentityId,
			registeredAt.UTC(),
			hello.LastEventSequence,
			snapshot,
		)
		if err != nil {
			return fmt.Errorf("insert worker process: %w", err)
		}
	case err != nil:
		return fmt.Errorf("load worker process: %w", err)
	default:
		if existingSessionID != hello.SessionId || existingCameraID != hello.CameraIdentityId {
			return repository.ErrWorkerIdentityMismatch
		}
		if hello.LastEventSequence < existingSnapshotSequence || hello.LastEventSequence < existingEventSequence {
			return repository.ErrWorkerEventSequence
		}
		_, err = tx.Exec(ctx, `
			UPDATE worker_processes
			SET last_registered_at = $2,
			    last_snapshot_sequence = $3,
			    last_event_sequence = greatest(last_event_sequence, $3),
			    snapshot = $4
			WHERE worker_id = $1`,
			hello.WorkerId, registeredAt.UTC(), hello.LastEventSequence, snapshot,
		)
		if err != nil {
			return fmt.Errorf("update worker process: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE camera_connections
		SET video_worker_id = $2
		WHERE camera_identity_id = $1`, hello.CameraIdentityId, hello.WorkerId,
	); err != nil {
		return fmt.Errorf("update camera worker ID: %w", err)
	}
	if err := applyInputStatus(ctx, tx, hello.CameraIdentityId, hello.Snapshot.Input); err != nil {
		return err
	}
	if hello.Snapshot.Recording != nil {
		if err := applyRecordingStatus(ctx, tx, hello.SessionId, hello.CameraIdentityId, hello.Snapshot.Recording); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit worker registration: %w", err)
	}
	return nil
}

func (s *Store) ApplyWorkerEvent(ctx context.Context, workerID string, event *workerv1.WorkerEvent) error {
	if err := workerprotocol.ValidateUUID("worker_id", workerID); err != nil {
		return err
	}
	if err := workerprotocol.ValidateWorkerEvent(event); err != nil {
		return err
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal worker event: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin worker event transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sessionID, cameraID string
	var snapshotSequence, lastEventSequence uint64
	if err := tx.QueryRow(ctx, `
		SELECT worker_processes.session_id::text,
		       worker_processes.camera_identity_id::text,
		       worker_processes.last_snapshot_sequence,
		       worker_processes.last_event_sequence
		FROM worker_processes
		JOIN camera_connections
		  ON camera_connections.camera_identity_id = worker_processes.camera_identity_id
		 AND camera_connections.video_worker_id = worker_processes.worker_id
		WHERE worker_processes.worker_id = $1
		FOR UPDATE`, workerID,
	).Scan(&sessionID, &cameraID, &snapshotSequence, &lastEventSequence); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrWorkerIdentityMismatch
	} else if err != nil {
		return fmt.Errorf("load worker process for event: %w", err)
	}

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO worker_events (event_id, worker_id, sequence, occurred_at, payload)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`,
		event.EventId, workerID, event.Sequence, event.OccurredAt.AsTime(), payload,
	)
	if err != nil {
		return fmt.Errorf("insert worker event: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		if err := verifyDuplicateEvent(ctx, tx, workerID, event, payload); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if event.Sequence > snapshotSequence {
		if event.Sequence != lastEventSequence+1 {
			return repository.ErrWorkerEventSequence
		}
		if input := event.GetInputStatusChanged(); input != nil {
			if err := applyInputStatus(ctx, tx, cameraID, input); err != nil {
				return err
			}
		}
		if recording := event.GetRecordingStatusChanged(); recording != nil {
			if err := applyRecordingStatus(ctx, tx, sessionID, cameraID, recording); err != nil {
				return err
			}
		}
		lastEventSequence = event.Sequence
	}
	if _, err := tx.Exec(ctx, `
		UPDATE worker_processes
		SET last_event_sequence = greatest(last_event_sequence, $2)
		WHERE worker_id = $1`, workerID, lastEventSequence,
	); err != nil {
		return fmt.Errorf("advance worker event sequence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit worker event: %w", err)
	}
	return nil
}

func verifyDuplicateEvent(
	ctx context.Context,
	tx pgx.Tx,
	workerID string,
	event *workerv1.WorkerEvent,
	payload []byte,
) error {
	var storedWorkerID, storedEventID string
	var storedSequence uint64
	var storedPayload []byte
	err := tx.QueryRow(ctx, `
		SELECT worker_id::text, event_id::text, sequence, payload
		FROM worker_events
		WHERE event_id = $1 OR (worker_id = $2 AND sequence = $3)`,
		event.EventId, workerID, event.Sequence,
	).Scan(&storedWorkerID, &storedEventID, &storedSequence, &storedPayload)
	if err != nil {
		return fmt.Errorf("verify duplicate worker event: %w", err)
	}
	if storedWorkerID != workerID || storedEventID != event.EventId ||
		storedSequence != event.Sequence || !bytes.Equal(storedPayload, payload) {
		return repository.ErrWorkerEventSequence
	}
	return nil
}

func applyInputStatus(ctx context.Context, tx pgx.Tx, cameraID string, input *workerv1.InputStatus) error {
	var status string
	var errorReason *string
	switch input.State {
	case workerv1.InputState_INPUT_STATE_WAITING:
		status = "waiting"
	case workerv1.InputState_INPUT_STATE_CONNECTED:
		status = "connected"
	case workerv1.InputState_INPUT_STATE_ERROR:
		status = "error"
		errorReason = &input.Error.Message
	default:
		return fmt.Errorf("apply input status: unsupported state %s", input.State)
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE camera_connections
		SET status = $2, error = $3
		WHERE camera_identity_id = $1`, cameraID, status, errorReason,
	)
	if err != nil {
		return fmt.Errorf("apply worker input status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return repository.ErrWorkerIdentityMismatch
	}
	return nil
}

func applyRecordingStatus(
	ctx context.Context,
	tx pgx.Tx,
	sessionID, cameraID string,
	recording *workerv1.RecordingStatus,
) error {
	switch recording.State {
	case workerv1.RecordingState_RECORDING_STATE_STARTING,
		workerv1.RecordingState_RECORDING_STATE_FINALIZING:
		return nil
	case workerv1.RecordingState_RECORDING_STATE_RECORDING:
		commandTag, err := tx.Exec(ctx, `
			UPDATE recording_cameras
			SET state = 'recording', started_at = $4, error = NULL
			WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3`,
			recording.TakeId, cameraID, sessionID, recording.StartedAt.AsTime(),
		)
		if err != nil {
			return fmt.Errorf("apply recording state: %w", err)
		}
		if commandTag.RowsAffected() == 0 {
			return repository.ErrWorkerStateMismatch
		}
		return nil
	case workerv1.RecordingState_RECORDING_STATE_ERROR:
		_, err := tx.Exec(ctx, `
			UPDATE recording_cameras
			SET state = 'errored', error = $4
			WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3`,
			recording.TakeId, cameraID, sessionID, recording.Error.Message,
		)
		if err != nil {
			return fmt.Errorf("apply recording error: %w", err)
		}
		return nil
	case workerv1.RecordingState_RECORDING_STATE_FINISHED:
		commandTag, err := tx.Exec(ctx, `
			INSERT INTO finalized_recordings (
				take_id, camera_identity_id, session_id, started_at, finished_at,
				relative_path, media_type
			)
			SELECT $1, $2, $3, $4, $5, $6, $7
			WHERE EXISTS (
				SELECT 1 FROM recording_cameras
				WHERE take_id = $1 AND camera_identity_id = $2 AND session_id = $3
			)
			ON CONFLICT (take_id, camera_identity_id) DO UPDATE
			SET started_at = excluded.started_at,
			    finished_at = excluded.finished_at,
			    relative_path = excluded.relative_path,
			    media_type = excluded.media_type`,
			recording.TakeId,
			cameraID,
			sessionID,
			recording.StartedAt.AsTime(),
			recording.FinishedAt.AsTime(),
			recording.FinalizedFile.RelativePath,
			recording.FinalizedFile.MediaType,
		)
		if err != nil {
			return fmt.Errorf("store finalized recording: %w", err)
		}
		if commandTag.RowsAffected() == 0 {
			return repository.ErrWorkerStateMismatch
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO video_files (
				take_id, camera_identity_id, session_id, state, started_at, finished_at
			) VALUES ($1, $2, $3, 'uploading', $4, $5)
			ON CONFLICT (take_id, camera_identity_id) DO NOTHING`,
			recording.TakeId, cameraID, sessionID,
			recording.StartedAt.AsTime(), recording.FinishedAt.AsTime(),
		); err != nil {
			return fmt.Errorf("stage video file upload: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("apply recording status: unsupported state %s", recording.State)
	}
}
