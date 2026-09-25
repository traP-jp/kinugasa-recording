use async_trait::async_trait;
use kinugasa_core::{
    domain::{ContentHash, FileSize, ObjectKey, RelativePath, SessionName, StoredObject},
    ports::{LockfileObject, LockfileRepository, RepositoryError},
};
use sqlx::FromRow;
use uuid::Uuid;

use crate::{MySqlRepository, MySqlUnitOfWork, error::classify_sqlx};

#[async_trait]
impl LockfileRepository<MySqlUnitOfWork> for MySqlRepository {
    async fn list_lockfile_objects(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session_name: &SessionName,
    ) -> Result<Vec<LockfileObject>, RepositoryError> {
        let database = unit_of_work.connection()?;
        let session_id = sqlx::query_scalar::<_, Uuid>("SELECT id FROM sessions WHERE name = ?")
            .bind(session_name.as_str())
            .fetch_optional(&mut *database)
            .await
            .map_err(classify_sqlx)?
            .ok_or(RepositoryError::NotFound)?;
        sqlx::query_as::<_, LockfileRow>(
            r#"
            SELECT fr.relative_path, vf.object_key, vf.hash, vf.size
            FROM video_files AS vf
            JOIN finalized_recordings AS fr
              ON fr.take_id = vf.take_id
             AND fr.camera_identity_id = vf.camera_identity_id
             AND fr.session_id = vf.session_id
            WHERE vf.session_id = ? AND vf.state = 'completed'
            ORDER BY fr.relative_path
            "#,
        )
        .bind(session_id)
        .fetch_all(&mut *database)
        .await
        .map_err(classify_sqlx)?
        .into_iter()
        .map(LockfileRow::into_domain)
        .collect()
    }
}

#[derive(Debug, FromRow)]
struct LockfileRow {
    relative_path: String,
    object_key: String,
    hash: Vec<u8>,
    size: u64,
}

impl LockfileRow {
    fn into_domain(self) -> Result<LockfileObject, RepositoryError> {
        Ok(LockfileObject {
            logical_path: RelativePath::new(self.relative_path).map_err(crate::error::corrupt)?,
            stored: StoredObject::new(
                ObjectKey::new(self.object_key).map_err(crate::error::corrupt)?,
                ContentHash::try_from_slice(&self.hash).map_err(crate::error::corrupt)?,
                FileSize::from_bytes(self.size),
            ),
        })
    }
}
