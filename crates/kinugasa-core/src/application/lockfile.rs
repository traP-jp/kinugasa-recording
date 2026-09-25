use std::{collections::BTreeMap, sync::Arc};

use crate::{
    application::{LockfileConfig, UseCaseError},
    domain::{ContentHash, FileSize, ObjectKey, SessionName},
    ports::{LockfileRepository, UnitOfWork, UnitOfWorkFactory},
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LockfileEntry {
    pub key: ObjectKey,
    pub sha256: ContentHash,
    pub size: FileSize,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Lockfile {
    pub schema_version: String,
    pub bucket: String,
    pub objects: BTreeMap<String, LockfileEntry>,
}

pub struct LockfileUseCases<F, R> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    config: LockfileConfig,
}

impl<F, R> LockfileUseCases<F, R> {
    #[must_use]
    pub fn new(unit_of_work_factory: Arc<F>, repository: Arc<R>, config: LockfileConfig) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            config,
        }
    }
}

impl<F, R> LockfileUseCases<F, R>
where
    F: UnitOfWorkFactory,
    R: LockfileRepository<F::UnitOfWork>,
{
    pub async fn get_lockfile(&self, session_name: String) -> Result<Lockfile, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let stored = self
            .repository
            .list_lockfile_objects(&mut unit_of_work, &session_name)
            .await?;
        unit_of_work.commit().await?;
        let objects = stored
            .into_iter()
            .map(|object| {
                (
                    object.logical_path.into_inner(),
                    LockfileEntry {
                        key: object.stored.object_key().clone(),
                        sha256: object.stored.hash(),
                        size: object.stored.size(),
                    },
                )
            })
            .collect();
        Ok(Lockfile {
            schema_version: self.config.schema_version().to_owned(),
            bucket: self.config.bucket().to_owned(),
            objects,
        })
    }
}
