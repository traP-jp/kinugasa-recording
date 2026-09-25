use async_trait::async_trait;
use kinugasa_core::{
    domain::{
        FinishedTake, FinishedTakeState, OngoingTake, SessionName, TakeId, TakeName, VideoFile,
        VideoFileState,
    },
    ports::{
        NamedFinishedTakeDetail, Page, PageRequest, RepositoryError, TakeRepository, TakeSnapshot,
    },
};
use uuid::Uuid;

use crate::{
    MySqlRepository, MySqlUnitOfWork,
    error::classify_sqlx,
    model::{
        FinishedTakeRow, OngoingTakeRow, RecordingCameraRow, VideoFileRow, finished_detail, naive,
    },
};

#[async_trait]
impl TakeRepository<MySqlUnitOfWork> for MySqlRepository {
    async fn insert_ongoing_take(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take: &OngoingTake,
    ) -> Result<(), RepositoryError> {
        let database = unit_of_work.connection()?;
        sqlx::query(
            r#"
            INSERT INTO takes (id, session_id, name, phase, state, started_at, finished_at, error)
            VALUES (?, ?, ?, 'ongoing', NULL, ?, NULL, NULL)
            "#,
        )
        .bind(take.id().into_uuid())
        .bind(take.session_id().into_uuid())
        .bind(take.name().as_str())
        .bind(naive(take.started_at()))
        .execute(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        for camera in take.cameras() {
            let (state, error) = recording_state(camera.state());
            sqlx::query(
                r#"
                INSERT INTO recording_cameras
                    (take_id, camera_identity_id, session_id, state, started_at, error)
                VALUES (?, ?, ?, ?, ?, ?)
                "#,
            )
            .bind(take.id().into_uuid())
            .bind(camera.camera_identity_id().into_uuid())
            .bind(take.session_id().into_uuid())
            .bind(state)
            .bind(naive(camera.started_at()))
            .bind(error)
            .execute(&mut *database)
            .await
            .map_err(classify_sqlx)?;
        }
        Ok(())
    }

    async fn get_ongoing_take(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
    ) -> Result<Option<OngoingTake>, RepositoryError> {
        get_ongoing(unit_of_work, session_name, false).await
    }

    async fn get_ongoing_take_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
    ) -> Result<Option<OngoingTake>, RepositoryError> {
        get_ongoing(unit_of_work, session_name, true).await
    }

    async fn get_take_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
    ) -> Result<TakeSnapshot, RepositoryError> {
        let database = unit_of_work.connection()?;
        let phase = sqlx::query_scalar::<_, String>(
            "SELECT CAST(phase AS CHAR) FROM takes WHERE id = ? FOR UPDATE",
        )
        .bind(take_id.into_uuid())
        .fetch_optional(&mut *database)
        .await
        .map_err(classify_sqlx)?
        .ok_or(RepositoryError::NotFound)?;
        match phase.as_str() {
            "ongoing" => Ok(TakeSnapshot::Ongoing),
            "finished" => load_finished_take(database, take_id.into_uuid())
                .await
                .map(TakeSnapshot::Finished),
            _ => Err(crate::model::invalid("take contains an unknown phase")),
        }
    }

    async fn replace_ongoing_with_finished(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take: &FinishedTake,
        video_files: &[VideoFile],
    ) -> Result<(), RepositoryError> {
        let database = unit_of_work.connection()?;
        let (state, error) = finished_state(take.state());
        sqlx::query(
            r#"
            UPDATE takes
            SET phase = 'finished', state = ?, finished_at = ?, error = ?
            WHERE id = ? AND session_id = ? AND phase = 'ongoing'
            "#,
        )
        .bind(state)
        .bind(naive(take.finished_at()))
        .bind(error)
        .bind(take.id().into_uuid())
        .bind(take.session_id().into_uuid())
        .execute(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        for video in video_files {
            insert_video_file(database, take.session_id().into_uuid(), video).await?;
        }
        Ok(())
    }

    async fn save_finished_take(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take: &FinishedTake,
    ) -> Result<(), RepositoryError> {
        let (state, error) = finished_state(take.state());
        sqlx::query(
            r#"
            UPDATE takes SET state = ?, error = ?, started_at = ?, finished_at = ?
            WHERE id = ? AND session_id = ? AND phase = 'finished'
            "#,
        )
        .bind(state)
        .bind(error)
        .bind(naive(take.started_at()))
        .bind(naive(take.finished_at()))
        .bind(take.id().into_uuid())
        .bind(take.session_id().into_uuid())
        .execute(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn delete_recording_cameras(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        take_id: TakeId,
    ) -> Result<(), RepositoryError> {
        sqlx::query("DELETE FROM recording_cameras WHERE take_id = ?")
            .bind(take_id.into_uuid())
            .execute(&mut *unit_of_work.connection()?)
            .await
            .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn list_finished_takes(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
        page: PageRequest,
    ) -> Result<Page<FinishedTake>, RepositoryError> {
        let database = unit_of_work.connection()?;
        let session_id = sqlx::query_scalar::<_, Uuid>("SELECT id FROM sessions WHERE name = ?")
            .bind(session_name.as_str())
            .fetch_optional(&mut *database)
            .await
            .map_err(classify_sqlx)?
            .ok_or(RepositoryError::NotFound)?;
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM takes WHERE session_id = ? AND phase = 'finished'",
        )
        .bind(session_id)
        .fetch_one(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        let rows = sqlx::query_as::<_, FinishedTakeRow>(
            r#"
            SELECT id, session_id, CAST(name AS CHAR) AS name, CAST(state AS CHAR) AS state,
                   started_at, finished_at, error
            FROM takes
            WHERE session_id = ? AND phase = 'finished'
            ORDER BY finished_at DESC, name ASC
            LIMIT ? OFFSET ?
            "#,
        )
        .bind(session_id)
        .bind(u64::from(page.page_size()))
        .bind(page.offset())
        .fetch_all(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        Ok(Page {
            items: rows
                .into_iter()
                .map(FinishedTakeRow::into_domain)
                .collect::<Result<Vec<_>, _>>()?,
            total: u64::try_from(total).map_err(crate::error::corrupt)?,
        })
    }

    async fn get_finished_take(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
        take_name: &TakeName,
    ) -> Result<NamedFinishedTakeDetail, RepositoryError> {
        let database = unit_of_work.connection()?;
        let take = sqlx::query_as::<_, FinishedTakeRow>(
            r#"
            SELECT t.id, t.session_id, CAST(t.name AS CHAR) AS name,
                   CAST(t.state AS CHAR) AS state, t.started_at, t.finished_at, t.error
            FROM takes AS t
            JOIN sessions AS s ON s.id = t.session_id
            WHERE s.name = ? AND t.name = ? AND t.phase = 'finished'
            "#,
        )
        .bind(session_name.as_str())
        .bind(take_name.as_str())
        .fetch_optional(&mut *database)
        .await
        .map_err(classify_sqlx)?
        .ok_or(RepositoryError::NotFound)?
        .into_domain()?;
        let rows = load_video_file_rows(database, take.id().into_uuid(), true, false).await?;
        finished_detail(take, rows)
    }
}

async fn get_ongoing(
    unit_of_work: &mut MySqlUnitOfWork,
    session_name: &SessionName,
    for_update: bool,
) -> Result<Option<OngoingTake>, RepositoryError> {
    let database = unit_of_work.connection()?;
    let lock = if for_update { "FOR UPDATE" } else { "" };
    let query = format!(
        r#"
        SELECT t.id, t.session_id, CAST(t.name AS CHAR) AS name, t.started_at
        FROM takes AS t
        JOIN sessions AS s ON s.id = t.session_id
        WHERE s.name = ? AND t.phase = 'ongoing'
        {lock}
        "#
    );
    let row = sqlx::query_as::<_, OngoingTakeRow>(&query)
        .bind(session_name.as_str())
        .fetch_optional(&mut *database)
        .await
        .map_err(classify_sqlx)?;
    let Some(row) = row else {
        let exists =
            sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM sessions WHERE name = ?)")
                .bind(session_name.as_str())
                .fetch_one(&mut *database)
                .await
                .map_err(classify_sqlx)?;
        return if exists {
            Ok(None)
        } else {
            Err(RepositoryError::NotFound)
        };
    };
    let cameras = load_recording_cameras(database, row.id, for_update).await?;
    row.into_domain(cameras).map(Some)
}

async fn load_recording_cameras(
    database: &mut sqlx::MySqlConnection,
    take_id: Uuid,
    for_update: bool,
) -> Result<Vec<kinugasa_core::domain::RecordingCamera>, RepositoryError> {
    let lock = if for_update { "FOR UPDATE" } else { "" };
    let query = format!(
        r#"
        SELECT take_id, camera_identity_id, CAST(state AS CHAR) AS state, started_at, error
        FROM recording_cameras WHERE take_id = ? ORDER BY camera_identity_id {lock}
        "#
    );
    sqlx::query_as::<_, RecordingCameraRow>(&query)
        .bind(take_id)
        .fetch_all(&mut *database)
        .await
        .map_err(classify_sqlx)?
        .into_iter()
        .map(RecordingCameraRow::into_domain)
        .collect()
}

async fn load_finished_take(
    database: &mut sqlx::MySqlConnection,
    take_id: Uuid,
) -> Result<FinishedTake, RepositoryError> {
    sqlx::query_as::<_, FinishedTakeRow>(
        r#"
        SELECT id, session_id, CAST(name AS CHAR) AS name, CAST(state AS CHAR) AS state,
               started_at, finished_at, error
        FROM takes WHERE id = ? AND phase = 'finished' FOR UPDATE
        "#,
    )
    .bind(take_id)
    .fetch_one(&mut *database)
    .await
    .map_err(classify_sqlx)?
    .into_domain()
}

async fn load_video_file_rows(
    database: &mut sqlx::MySqlConnection,
    take_id: Uuid,
    with_names: bool,
    for_update: bool,
) -> Result<Vec<VideoFileRow>, RepositoryError> {
    let camera_name = if with_names {
        "CAST(ci.name AS CHAR)"
    } else {
        "NULL"
    };
    let join = if with_names {
        "JOIN camera_identities AS ci ON ci.id = vf.camera_identity_id"
    } else {
        ""
    };
    let lock = if for_update { "FOR UPDATE" } else { "" };
    let query = format!(
        r#"
        SELECT vf.take_id, vf.camera_identity_id, CAST(vf.state AS CHAR) AS state,
               vf.started_at, vf.finished_at, vf.object_key, vf.hash, vf.size,
               vf.error, {camera_name} AS camera_name
        FROM video_files AS vf {join}
        WHERE vf.take_id = ?
        ORDER BY vf.camera_identity_id {lock}
        "#
    );
    sqlx::query_as::<_, VideoFileRow>(&query)
        .bind(take_id)
        .fetch_all(&mut *database)
        .await
        .map_err(classify_sqlx)
}

async fn insert_video_file(
    database: &mut sqlx::MySqlConnection,
    session_id: Uuid,
    video: &VideoFile,
) -> Result<(), RepositoryError> {
    let (state, object_key, hash, size, error) = video_state(video.state());
    sqlx::query(
        r#"
        INSERT INTO video_files (
            take_id, camera_identity_id, session_id, state, started_at, finished_at,
            object_key, hash, size, error
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        "#,
    )
    .bind(video.finished_take_id().into_uuid())
    .bind(video.camera_identity_id().into_uuid())
    .bind(session_id)
    .bind(state)
    .bind(naive(video.started_at()))
    .bind(naive(video.finished_at()))
    .bind(object_key)
    .bind(hash)
    .bind(size)
    .bind(error)
    .execute(&mut *database)
    .await
    .map_err(classify_sqlx)?;
    Ok(())
}

pub(crate) fn recording_state(
    state: &kinugasa_core::domain::RecordingCameraState,
) -> (&'static str, Option<&str>) {
    match state {
        kinugasa_core::domain::RecordingCameraState::Recording => ("recording", None),
        kinugasa_core::domain::RecordingCameraState::Errored(reason) => {
            ("errored", Some(reason.as_str()))
        }
    }
}

fn finished_state(state: &FinishedTakeState) -> (&'static str, Option<&str>) {
    match state {
        FinishedTakeState::Uploading => ("uploading", None),
        FinishedTakeState::Completed => ("completed", None),
        FinishedTakeState::Errored(reason) => ("errored", Some(reason.as_str())),
    }
}

pub(crate) type VideoStateValues<'a> = (
    &'static str,
    Option<&'a str>,
    Option<Vec<u8>>,
    Option<u64>,
    Option<&'a str>,
);

pub(crate) fn video_state(state: &VideoFileState) -> VideoStateValues<'_> {
    match state {
        VideoFileState::Uploading => ("uploading", None, None, None, None),
        VideoFileState::Completed(stored) => (
            "completed",
            Some(stored.object_key().as_str()),
            Some(stored.hash().as_bytes().to_vec()),
            Some(stored.size().bytes()),
            None,
        ),
        VideoFileState::Errored(reason) => ("errored", None, None, None, Some(reason.as_str())),
    }
}
