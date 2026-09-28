use std::sync::Arc;

use tokio::task::JoinSet;

use crate::{
    application::{UseCaseError, recording_state::persist_recording_failure, task::collect_tasks},
    domain::{CameraConnectionState, CameraInputState, ErrorReason, RecordingCameraState},
    ports::{
        CameraIngress, CameraRepository, MediaError, MediaEvent, MediaEventKind, MediaEventSource,
        ProvisionCameraRequest, RecordingRepository, TakeRepository, UnitOfWork, UnitOfWorkFactory,
    },
};

const RECORDING_INTERRUPTED: &str = "recording interrupted by process restart";

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct MediaRecoveryReport {
    pub interrupted_recordings: usize,
    pub provisioned_cameras: usize,
    pub completed_deletions: usize,
    pub deferred_cameras: usize,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CameraResourceOutcome {
    Provisioned,
    Deleted,
    Deferred,
}

pub struct MediaCoordinator<F, R, M> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    media: Arc<M>,
}

impl<F, R, M> MediaCoordinator<F, R, M> {
    #[must_use]
    pub fn new(unit_of_work_factory: Arc<F>, repository: Arc<R>, media: Arc<M>) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            media,
        }
    }
}

impl<F, R, M> MediaCoordinator<F, R, M>
where
    F: UnitOfWorkFactory + 'static,
    F::UnitOfWork: 'static,
    R: CameraRepository<F::UnitOfWork>
        + TakeRepository<F::UnitOfWork>
        + RecordingRepository<F::UnitOfWork>
        + 'static,
    M: CameraIngress + MediaEventSource + 'static,
{
    /// Performs one startup recovery pass. Recording is deliberately failed,
    /// while camera ingress is recreated to wait for RIST reconnection.
    pub async fn recover_after_restart(&self) -> Result<MediaRecoveryReport, UseCaseError> {
        let interrupted_reason = ErrorReason::new(RECORDING_INTERRUPTED)?;

        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let active_recordings = self
            .repository
            .list_active_recordings(&mut unit_of_work)
            .await?;
        for recording in &active_recordings {
            persist_recording_failure(
                &mut unit_of_work,
                self.repository.as_ref(),
                recording.ongoing_take_id(),
                recording.camera_identity_id(),
                &interrupted_reason,
            )
            .await?;
        }
        unit_of_work.commit().await?;
        let mut report = self.reconcile_camera_resources_inner(true).await?;
        report.interrupted_recordings = active_recordings.len();
        Ok(report)
    }

    /// Reconciles newly created and deleting camera resources. Unlike startup
    /// recovery, it leaves already provisioned waiting/connected cameras alone.
    pub async fn reconcile_camera_resources(&self) -> Result<MediaRecoveryReport, UseCaseError> {
        self.reconcile_camera_resources_inner(false).await
    }

    async fn reconcile_camera_resources_inner(
        &self,
        reprovision_all: bool,
    ) -> Result<MediaRecoveryReport, UseCaseError> {
        let mut report = MediaRecoveryReport::default();
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let resources = self
            .repository
            .list_camera_resources(&mut unit_of_work)
            .await?;
        unit_of_work.commit().await?;

        let mut tasks = JoinSet::new();
        for resource in resources {
            let camera_id = resource.camera.identity().id();
            if resource
                .camera
                .connection()
                .deletion_requested_at()
                .is_some()
            {
                let unit_of_work_factory = Arc::clone(&self.unit_of_work_factory);
                let repository = Arc::clone(&self.repository);
                let media = Arc::clone(&self.media);
                tasks.spawn(async move {
                    match media.revoke_camera(camera_id).await {
                        Ok(()) | Err(MediaError::NotFound) => {
                            let mut unit_of_work = unit_of_work_factory.begin().await?;
                            repository
                                .delete_camera_connection(&mut unit_of_work, camera_id)
                                .await?;
                            unit_of_work.commit().await?;
                            Ok(CameraResourceOutcome::Deleted)
                        }
                        Err(_) => Ok(CameraResourceOutcome::Deferred),
                    }
                });
                continue;
            }
            if !reprovision_all
                && !matches!(
                    resource.camera.connection().state(),
                    CameraConnectionState::Activating
                )
            {
                continue;
            }

            let request = ProvisionCameraRequest {
                session_id: resource.camera.identity().session_id(),
                session_name: resource.session_name,
                camera_identity_id: camera_id,
                camera_name: resource.camera.identity().name().clone(),
                virtual_port: resource.camera.connection().virtual_port(),
            };
            let unit_of_work_factory = Arc::clone(&self.unit_of_work_factory);
            let repository = Arc::clone(&self.repository);
            let media = Arc::clone(&self.media);
            tasks.spawn(async move {
                match media.provision_camera(&request).await {
                    Ok(access) => {
                        let state = CameraConnectionState::Waiting {
                            endpoint: access.endpoint,
                        };
                        let mut unit_of_work = unit_of_work_factory.begin().await?;
                        let camera = repository
                            .get_camera_by_id_for_update(&mut unit_of_work, camera_id)
                            .await?;
                        let (_, mut connection) = camera.into_parts();
                        connection.set_state(state);
                        connection.set_virtual_port(Some(access.virtual_port));
                        repository
                            .save_camera_connection(&mut unit_of_work, &connection)
                            .await?;
                        unit_of_work.commit().await?;
                        Ok(CameraResourceOutcome::Provisioned)
                    }
                    Err(_) => Ok(CameraResourceOutcome::Deferred),
                }
            });
        }

        for outcome in collect_tasks(tasks).await? {
            match outcome {
                CameraResourceOutcome::Provisioned => report.provisioned_cameras += 1,
                CameraResourceOutcome::Deleted => report.completed_deletions += 1,
                CameraResourceOutcome::Deferred => report.deferred_cameras += 1,
            }
        }
        Ok(report)
    }

    /// Applies one process-local media notification to the database. A camera
    /// input failure also terminally fails its active recording.
    pub async fn process_next_event(&self) -> Result<MediaEvent, UseCaseError> {
        let event = self.media.next_event().await?;
        let MediaEventKind::CameraInputChanged {
            camera_identity_id,
            state,
        } = &event.kind;

        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let camera = self
            .repository
            .get_camera_by_id_for_update(&mut unit_of_work, *camera_identity_id)
            .await?;
        let (_, mut connection) = camera.into_parts();
        connection.apply_input_state(state.clone())?;
        self.repository
            .save_camera_connection(&mut unit_of_work, &connection)
            .await?;
        let recording_error = match state {
            CameraInputState::Waiting => Some(ErrorReason::new("camera input disconnected")?),
            CameraInputState::Errored(reason) => Some(reason.clone()),
            CameraInputState::Connected => None,
        };
        if let Some(reason) = recording_error
            && let Some(recording) = self
                .repository
                .get_ongoing_recording_for_camera(&mut unit_of_work, *camera_identity_id)
                .await?
            && matches!(recording.state(), RecordingCameraState::Recording)
        {
            persist_recording_failure(
                &mut unit_of_work,
                self.repository.as_ref(),
                recording.ongoing_take_id(),
                recording.camera_identity_id(),
                &reason,
            )
            .await?;
        }
        unit_of_work.commit().await?;
        Ok(event)
    }
}
