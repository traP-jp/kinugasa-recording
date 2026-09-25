use std::{num::NonZeroU32, sync::Arc};

use tokio::task::JoinSet;

use crate::{
    application::{UseCaseError, task::collect_tasks},
    domain::{ErrorReason, UploadOutcome, UploadResult},
    ports::{
        Clock, ObjectStorage, ObjectStorageError, RecordingRepository, UnitOfWork,
        UnitOfWorkFactory,
    },
};

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct UploadReconciliationReport {
    pub completed: usize,
    pub errored: usize,
    pub deferred: usize,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum UploadTaskOutcome {
    Completed,
    Errored,
    Deferred,
}

pub struct UploadCoordinator<F, R, S, C> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    object_storage: Arc<S>,
    clock: Arc<C>,
}

impl<F, R, S, C> UploadCoordinator<F, R, S, C> {
    #[must_use]
    pub fn new(
        unit_of_work_factory: Arc<F>,
        repository: Arc<R>,
        object_storage: Arc<S>,
        clock: Arc<C>,
    ) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            object_storage,
            clock,
        }
    }
}

impl<F, R, S, C> UploadCoordinator<F, R, S, C>
where
    F: UnitOfWorkFactory + 'static,
    F::UnitOfWork: 'static,
    R: RecordingRepository<F::UnitOfWork> + 'static,
    S: ObjectStorage + 'static,
    C: Clock + 'static,
{
    pub async fn reconcile(
        &self,
        limit: NonZeroU32,
    ) -> Result<UploadReconciliationReport, UseCaseError> {
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let pending = self
            .repository
            .list_pending_uploads(&mut unit_of_work, limit)
            .await?;
        unit_of_work.commit().await?;

        let mut tasks = JoinSet::new();
        for upload in pending {
            let unit_of_work_factory = Arc::clone(&self.unit_of_work_factory);
            let repository = Arc::clone(&self.repository);
            let object_storage = Arc::clone(&self.object_storage);
            let clock = Arc::clone(&self.clock);
            tasks.spawn(async move {
                let (outcome, task_outcome) = match object_storage.upload(upload.recording()).await
                {
                    Ok(stored) => (
                        UploadOutcome::Completed(stored),
                        UploadTaskOutcome::Completed,
                    ),
                    Err(ObjectStorageError::Conflict) => (
                        UploadOutcome::Errored(ErrorReason::new(
                            "object already exists with different contents",
                        )?),
                        UploadTaskOutcome::Errored,
                    ),
                    Err(ObjectStorageError::Unavailable(_) | ObjectStorageError::Unexpected(_)) => {
                        return Ok(UploadTaskOutcome::Deferred);
                    }
                };
                let result = UploadResult::new(
                    upload.recording().take_id(),
                    upload.recording().camera_identity_id(),
                    upload.recording().session_id(),
                    outcome,
                    clock.now(),
                );
                let mut unit_of_work = unit_of_work_factory.begin().await?;
                repository
                    .apply_upload_result(&mut unit_of_work, &result)
                    .await?;
                unit_of_work.commit().await?;
                Ok(task_outcome)
            });
        }

        let mut report = UploadReconciliationReport::default();
        for outcome in collect_tasks(tasks).await? {
            match outcome {
                UploadTaskOutcome::Completed => report.completed += 1,
                UploadTaskOutcome::Errored => report.errored += 1,
                UploadTaskOutcome::Deferred => report.deferred += 1,
            }
        }
        Ok(report)
    }
}
