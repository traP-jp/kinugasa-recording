use std::num::NonZeroU32;

use async_trait::async_trait;
use chrono::NaiveDateTime;
use kinugasa_core::{
    domain::{CameraIdentityId, FinalizedRecording, RecordingCamera, TakeId, VideoFile},
    ports::{PendingUpload, RecordingRepository, RepositoryError},
};
use sqlx::FromRow;
use uuid::Uuid;

use crate::{
    MySqlRepository, MySqlUnitOfWork,
    error::classify_sqlx,
    model::{FinalizedRecordingRow, RecordingCameraRow, VideoFileRow, naive},
    take::{recording_state, video_state},
};

#[async_trait]
impl RecordingRepository<MySqlUnitOfWork> for MySqlRepository {
    async fn list_active_recordings(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
    ) -> Result<Vec<RecordingCamera>, RepositoryError> {
        load_recordings(
            unit_of_work.connection()?,
            r#"
            SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state, started_at, error
            FROM recording_cameras
            WHERE state = 'recording'
            ORDER BY take_id, camera_identity_id
            "#,
            None,
        )
        .await
    }

    async fn get_ongoing_recording_for_camera(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        camera_id: CameraIdentityId,
    ) -> Result<Option<RecordingCamera>, RepositoryError> {
        let rows = load_recordings(
            unit_of_work.connection()?,
            r#"
            SELECT rc.take_id, rc.camera_identity_id, CAST(rc.state AS CHAR) AS state,
                   rc.started_at, rc.error
            FROM recording_cameras AS rc
            JOIN takes AS t ON t.id = rc.take_id
            WHERE rc.camera_identity_id = ? AND t.phase = 'ongoing'
            ORDER BY rc.take_id LIMIT 2
            "#,
            Some(camera_id.into_uuid()),
        )
        .await?;
        if rows.len() > 1 {
            return Err(crate::model::invalid(
                "camera has more than one ongoing recording",
            ));
        }
        Ok(rows.into_iter().next())
    }

    async fn get_recording_camera_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<Option<RecordingCamera>, RepositoryError> {
        sqlx::query_as::<_, RecordingCameraRow>(
            r#"
            SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state, started_at, error
            FROM recording_cameras
            WHERE take_id = ? AND camera_identity_id = ?
            FOR UPDATE
            "#,
        )
        .bind(take_id.into_uuid())
        .bind(camera_id.into_uuid())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .map(RecordingCameraRow::into_domain)
        .transpose()
    }

    async fn save_recording_camera(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        recording: &RecordingCamera,
    ) -> Result<(), RepositoryError> {
        let (state, error) = recording_state(recording.state());
        sqlx::query(
            r#"
            UPDATE recording_cameras SET state = ?, started_at = ?, error = ?
            WHERE take_id = ? AND camera_identity_id = ?
            "#,
        )
        .bind(state)
        .bind(naive(recording.started_at()))
        .bind(error)
        .bind(recording.ongoing_take_id().into_uuid())
        .bind(recording.camera_identity_id().into_uuid())
        .execute(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn get_video_file_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<Option<VideoFile>, RepositoryError> {
        sqlx::query_as::<_, VideoFileRow>(
            r#"
            SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state,
                   started_at, finished_at, object_key, hash, size, error,
                   NULL AS camera_name
            FROM video_files
            WHERE take_id = ? AND camera_identity_id = ?
            FOR UPDATE
            "#,
        )
        .bind(take_id.into_uuid())
        .bind(camera_id.into_uuid())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .map(|row| row.into_domain().map(|(video, _)| video))
        .transpose()
    }

    async fn list_video_files_for_take_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
    ) -> Result<Vec<VideoFile>, RepositoryError> {
        load_video_files(
            unit_of_work.connection()?,
            r#"
            SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state,
                   started_at, finished_at, object_key, hash, size, error,
                   NULL AS camera_name
            FROM video_files WHERE take_id = ?
            ORDER BY camera_identity_id FOR UPDATE
            "#,
            take_id.into_uuid(),
        )
        .await
    }

    async fn list_uploading_video_files_for_camera(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        camera_id: CameraIdentityId,
    ) -> Result<Vec<VideoFile>, RepositoryError> {
        load_video_files(
            unit_of_work.connection()?,
            r#"
            SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state,
                   started_at, finished_at, object_key, hash, size, error,
                   NULL AS camera_name
            FROM video_files
            WHERE camera_identity_id = ? AND state = 'uploading'
            ORDER BY take_id
            "#,
            camera_id.into_uuid(),
        )
        .await
    }

    async fn save_video_file(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        video_file: &VideoFile,
    ) -> Result<(), RepositoryError> {
        let (state, object_key, hash, size, error) = video_state(video_file.state());
        sqlx::query(
            r#"
            UPDATE video_files
            SET state = ?, started_at = ?, finished_at = ?, object_key = ?, hash = ?, size = ?, error = ?
            WHERE take_id = ? AND camera_identity_id = ?
            "#,
        )
        .bind(state)
        .bind(naive(video_file.started_at()))
        .bind(naive(video_file.finished_at()))
        .bind(object_key)
        .bind(hash)
        .bind(size)
        .bind(error)
        .bind(video_file.finished_take_id().into_uuid())
        .bind(video_file.camera_identity_id().into_uuid())
        .execute(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn get_finalized_recording_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<Option<FinalizedRecording>, RepositoryError> {
        sqlx::query_as::<_, FinalizedRecordingRow>(
            r#"
            SELECT take_id, camera_identity_id, session_id, started_at, finished_at,
                   relative_path, media_type
            FROM finalized_recordings
            WHERE take_id = ? AND camera_identity_id = ? FOR UPDATE
            "#,
        )
        .bind(take_id.into_uuid())
        .bind(camera_id.into_uuid())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .map(FinalizedRecordingRow::into_domain)
        .transpose()
    }

    async fn insert_finalized_recording(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        recording: &FinalizedRecording,
    ) -> Result<(), RepositoryError> {
        sqlx::query(
            r#"
            INSERT INTO finalized_recordings (
                take_id, camera_identity_id, session_id, started_at,
                finished_at, relative_path, media_type
            ) VALUES (?, ?, ?, ?, ?, ?, ?)
            "#,
        )
        .bind(recording.take_id().into_uuid())
        .bind(recording.camera_identity_id().into_uuid())
        .bind(recording.session_id().into_uuid())
        .bind(naive(recording.started_at()))
        .bind(naive(recording.finished_at()))
        .bind(recording.relative_path().as_str())
        .bind(recording.media_type().as_str())
        .execute(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn list_pending_uploads(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        limit: NonZeroU32,
    ) -> Result<Vec<PendingUpload>, RepositoryError> {
        sqlx::query_as::<_, PendingUploadRow>(
            r#"
            SELECT fr.take_id, fr.camera_identity_id, fr.session_id,
                   fr.started_at AS recording_started_at,
                   fr.finished_at AS recording_finished_at,
                   fr.relative_path, fr.media_type,
                   CAST(vf.state AS CHAR) AS video_state,
                   vf.started_at AS video_started_at,
                   vf.finished_at AS video_finished_at,
                   vf.object_key, vf.hash, vf.size, vf.error
            FROM finalized_recordings AS fr
            JOIN video_files AS vf
              ON vf.take_id = fr.take_id
             AND vf.camera_identity_id = fr.camera_identity_id
             AND vf.session_id = fr.session_id
            WHERE vf.state = 'uploading'
            ORDER BY vf.finished_at ASC, vf.take_id, vf.camera_identity_id
            LIMIT ?
            "#,
        )
        .bind(u64::from(limit.get()))
        .fetch_all(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .into_iter()
        .map(PendingUploadRow::into_domain)
        .collect()
    }
}

async fn load_recordings(
    database: &mut sqlx::MySqlConnection,
    query: &str,
    id: Option<Uuid>,
) -> Result<Vec<RecordingCamera>, RepositoryError> {
    let query = sqlx::query_as::<_, RecordingCameraRow>(query);
    let rows = match id {
        Some(id) => query.bind(id).fetch_all(&mut *database).await,
        None => query.fetch_all(&mut *database).await,
    }
    .map_err(classify_sqlx)?;
    rows.into_iter()
        .map(RecordingCameraRow::into_domain)
        .collect()
}

async fn load_video_files(
    database: &mut sqlx::MySqlConnection,
    query: &str,
    id: Uuid,
) -> Result<Vec<VideoFile>, RepositoryError> {
    sqlx::query_as::<_, VideoFileRow>(query)
        .bind(id)
        .fetch_all(&mut *database)
        .await
        .map_err(classify_sqlx)?
        .into_iter()
        .map(|row| row.into_domain().map(|(video, _)| video))
        .collect()
}

#[derive(Debug, FromRow)]
struct PendingUploadRow {
    take_id: Uuid,
    camera_identity_id: Uuid,
    session_id: Uuid,
    recording_started_at: NaiveDateTime,
    recording_finished_at: NaiveDateTime,
    relative_path: String,
    media_type: String,
    video_state: String,
    video_started_at: NaiveDateTime,
    video_finished_at: NaiveDateTime,
    object_key: Option<String>,
    hash: Option<Vec<u8>>,
    size: Option<u64>,
    error: Option<String>,
}

impl PendingUploadRow {
    fn into_domain(self) -> Result<PendingUpload, RepositoryError> {
        let recording = FinalizedRecordingRow {
            take_id: self.take_id,
            camera_identity_id: self.camera_identity_id,
            session_id: self.session_id,
            started_at: self.recording_started_at,
            finished_at: self.recording_finished_at,
            relative_path: self.relative_path,
            media_type: self.media_type,
        }
        .into_domain()?;
        let (video_file, _) = VideoFileRow {
            take_id: self.take_id,
            camera_identity_id: self.camera_identity_id,
            state: self.video_state,
            started_at: self.video_started_at,
            finished_at: self.video_finished_at,
            object_key: self.object_key,
            hash: self.hash,
            size: self.size,
            error: self.error,
            camera_name: None,
        }
        .into_domain()?;
        PendingUpload::new(recording, video_file).map_err(crate::error::corrupt)
    }
}
