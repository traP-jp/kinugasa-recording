use async_trait::async_trait;
use kinugasa_core::{
    domain::{
        Camera, CameraConnection, CameraConnectionState, CameraIdentityId, CameraName, SessionName,
    },
    ports::{CameraRepository, CameraResource, RepositoryError},
};

use crate::{
    MySqlRepository, MySqlUnitOfWork,
    error::classify_sqlx,
    model::{CameraRow, naive},
};

const CAMERA_COLUMNS: &str = r#"
    ci.id, ci.session_id, CAST(ci.name AS CHAR) AS name, ci.created_at, cc.url,
    CAST(cc.status AS CHAR) AS status, cc.error, cc.media_process_id,
    cc.deletion_requested_at
"#;

#[async_trait]
impl CameraRepository<MySqlUnitOfWork> for MySqlRepository {
    async fn create_camera(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        camera: &Camera,
    ) -> Result<(), RepositoryError> {
        let identity = camera.identity();
        let connection = camera.connection();
        let (url, status, error) = connection_values(connection.state());
        let database = unit_of_work.connection()?;

        sqlx::query(
            "INSERT INTO camera_identities (id, session_id, name, created_at) VALUES (?, ?, ?, ?)",
        )
        .bind(identity.id().into_uuid())
        .bind(identity.session_id().into_uuid())
        .bind(identity.name().as_str())
        .bind(naive(identity.created_at()))
        .execute(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        sqlx::query(
            r#"
            INSERT INTO camera_connections
                (camera_identity_id, url, status, error, media_process_id, deletion_requested_at)
            VALUES (?, ?, ?, ?, ?, ?)
            "#,
        )
        .bind(identity.id().into_uuid())
        .bind(url)
        .bind(status)
        .bind(error)
        .bind(connection.media_process_id().map(|id| id.into_uuid()))
        .bind(connection.deletion_requested_at().map(naive))
        .execute(&mut *database)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn list_cameras(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
    ) -> Result<Vec<Camera>, RepositoryError> {
        let database = unit_of_work.connection()?;
        let session_exists =
            sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM sessions WHERE name = ?)")
                .bind(session_name.as_str())
                .fetch_one(&mut *database)
                .await
                .map_err(classify_sqlx)?;
        if !session_exists {
            return Err(RepositoryError::NotFound);
        }
        let query = format!(
            r#"
            SELECT {CAMERA_COLUMNS}, NULL AS session_name
            FROM camera_identities AS ci
            JOIN sessions AS s ON s.id = ci.session_id
            JOIN camera_connections AS cc ON cc.camera_identity_id = ci.id
            WHERE s.name = ? AND cc.deletion_requested_at IS NULL
            ORDER BY ci.created_at ASC, ci.name ASC
            "#
        );
        sqlx::query_as::<_, CameraRow>(&query)
            .bind(session_name.as_str())
            .fetch_all(&mut *database)
            .await
            .map_err(classify_sqlx)?
            .into_iter()
            .map(CameraRow::into_camera)
            .collect()
    }

    async fn list_camera_resources(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
    ) -> Result<Vec<CameraResource>, RepositoryError> {
        let query = format!(
            r#"
            SELECT {CAMERA_COLUMNS}, CAST(s.name AS CHAR) AS session_name
            FROM camera_identities AS ci
            JOIN sessions AS s ON s.id = ci.session_id
            JOIN camera_connections AS cc ON cc.camera_identity_id = ci.id
            ORDER BY ci.id
            "#
        );
        sqlx::query_as::<_, CameraRow>(&query)
            .fetch_all(&mut *unit_of_work.connection()?)
            .await
            .map_err(classify_sqlx)?
            .into_iter()
            .map(|row| {
                let session_name = row.session_name()?;
                let camera = row.into_camera()?;
                Ok(CameraResource {
                    session_name,
                    camera,
                })
            })
            .collect()
    }

    async fn get_camera(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
        camera_name: &CameraName,
    ) -> Result<Camera, RepositoryError> {
        get_camera_by_name(unit_of_work, session_name, camera_name, false).await
    }

    async fn get_camera_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
        camera_name: &CameraName,
    ) -> Result<Camera, RepositoryError> {
        get_camera_by_name(unit_of_work, session_name, camera_name, true).await
    }

    async fn get_camera_by_id_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        camera_id: CameraIdentityId,
    ) -> Result<Camera, RepositoryError> {
        let query = format!(
            r#"
            SELECT {CAMERA_COLUMNS}, NULL AS session_name
            FROM camera_identities AS ci
            JOIN camera_connections AS cc ON cc.camera_identity_id = ci.id
            WHERE ci.id = ?
            FOR UPDATE
            "#
        );
        sqlx::query_as::<_, CameraRow>(&query)
            .bind(camera_id.into_uuid())
            .fetch_optional(&mut *unit_of_work.connection()?)
            .await
            .map_err(classify_sqlx)?
            .ok_or(RepositoryError::NotFound)?
            .into_camera()
    }

    async fn save_camera_connection(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        connection: &CameraConnection,
    ) -> Result<(), RepositoryError> {
        let (url, status, error) = connection_values(connection.state());
        sqlx::query(
            r#"
            UPDATE camera_connections
            SET url = ?, status = ?, error = ?, media_process_id = ?, deletion_requested_at = ?
            WHERE camera_identity_id = ?
            "#,
        )
        .bind(url)
        .bind(status)
        .bind(error)
        .bind(connection.media_process_id().map(|id| id.into_uuid()))
        .bind(connection.deletion_requested_at().map(naive))
        .bind(connection.camera_identity_id().into_uuid())
        .execute(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn delete_camera_connection(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        camera_id: CameraIdentityId,
    ) -> Result<(), RepositoryError> {
        let result = sqlx::query("DELETE FROM camera_connections WHERE camera_identity_id = ?")
            .bind(camera_id.into_uuid())
            .execute(&mut *unit_of_work.connection()?)
            .await
            .map_err(classify_sqlx)?;
        if result.rows_affected() == 0 {
            return Err(RepositoryError::NotFound);
        }
        Ok(())
    }
}

async fn get_camera_by_name(
    unit_of_work: &mut MySqlUnitOfWork,
    session_name: &SessionName,
    camera_name: &CameraName,
    for_update: bool,
) -> Result<Camera, RepositoryError> {
    let lock = if for_update { "FOR UPDATE" } else { "" };
    let query = format!(
        r#"
        SELECT {CAMERA_COLUMNS}, NULL AS session_name
        FROM camera_identities AS ci
        JOIN sessions AS s ON s.id = ci.session_id
        JOIN camera_connections AS cc ON cc.camera_identity_id = ci.id
        WHERE s.name = ? AND ci.name = ? AND cc.deletion_requested_at IS NULL
        {lock}
        "#
    );
    sqlx::query_as::<_, CameraRow>(&query)
        .bind(session_name.as_str())
        .bind(camera_name.as_str())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .ok_or(RepositoryError::NotFound)?
        .into_camera()
}

fn connection_values(state: &CameraConnectionState) -> (Option<&str>, &'static str, Option<&str>) {
    match state {
        CameraConnectionState::Activating => (None, "activating", None),
        CameraConnectionState::Waiting { endpoint } => (Some(endpoint.as_str()), "waiting", None),
        CameraConnectionState::Connected { endpoint } => {
            (Some(endpoint.as_str()), "connected", None)
        }
        CameraConnectionState::Errored { endpoint, reason } => {
            (Some(endpoint.as_str()), "error", Some(reason.as_str()))
        }
    }
}
