use std::sync::Arc;

use crate::{
    application::{UseCaseError, recording_state::fail_upload},
    domain::{
        Camera, CameraConnection, CameraConnectionState, CameraIdentity, CameraName, ErrorReason,
        SessionName, SessionState,
    },
    ports::{
        CameraRepository, Clock, IdGenerator, RecordingRepository, SessionRepository,
        TakeRepository, UnitOfWork, UnitOfWorkFactory,
    },
};

pub struct CameraUseCases<F, R, C, I> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    clock: Arc<C>,
    ids: Arc<I>,
}

impl<F, R, C, I> CameraUseCases<F, R, C, I> {
    #[must_use]
    pub fn new(
        unit_of_work_factory: Arc<F>,
        repository: Arc<R>,
        clock: Arc<C>,
        ids: Arc<I>,
    ) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            clock,
            ids,
        }
    }
}

impl<F, R, C, I> CameraUseCases<F, R, C, I>
where
    F: UnitOfWorkFactory,
    R: CameraRepository<F::UnitOfWork>
        + SessionRepository<F::UnitOfWork>
        + TakeRepository<F::UnitOfWork>
        + RecordingRepository<F::UnitOfWork>,
    C: Clock,
    I: IdGenerator,
{
    pub async fn create_camera(
        &self,
        session_name: String,
        camera_name: String,
    ) -> Result<Camera, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let camera_name = CameraName::new(camera_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let session = self
            .repository
            .get_session_for_update(&mut unit_of_work, &session_name)
            .await?
            .session;
        if session.state() != SessionState::Active {
            return Err(UseCaseError::conflict());
        }
        let identity = CameraIdentity::new(
            self.ids.camera_identity_id(),
            session.id(),
            camera_name,
            self.clock.now(),
        );
        let connection = CameraConnection::new(identity.id(), CameraConnectionState::Activating);
        let camera = Camera::new(identity, connection)?;
        self.repository
            .create_camera(&mut unit_of_work, &camera)
            .await?;
        unit_of_work.commit().await?;
        Ok(camera)
    }

    pub async fn list_cameras(&self, session_name: String) -> Result<Vec<Camera>, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .list_cameras(&mut unit_of_work, &session_name)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }

    pub async fn get_camera(
        &self,
        session_name: String,
        camera_name: String,
    ) -> Result<Camera, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let camera_name = CameraName::new(camera_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .get_camera(&mut unit_of_work, &session_name, &camera_name)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }

    pub async fn delete_camera(
        &self,
        session_name: String,
        camera_name: String,
        force: bool,
    ) -> Result<(), UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let camera_name = CameraName::new(camera_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let camera = self
            .repository
            .get_camera_for_update(&mut unit_of_work, &session_name, &camera_name)
            .await?;
        let camera_id = camera.identity().id();
        if self
            .repository
            .get_ongoing_recording_for_camera(&mut unit_of_work, camera_id)
            .await?
            .is_some()
        {
            return Err(UseCaseError::conflict());
        }
        let uploading = self
            .repository
            .list_uploading_video_files_for_camera(&mut unit_of_work, camera_id)
            .await?;
        if !uploading.is_empty() && !force {
            return Err(UseCaseError::conflict());
        }
        if force {
            let reason = ErrorReason::new("upload aborted by forced camera deletion")?;
            for video in uploading {
                fail_upload(
                    &mut unit_of_work,
                    self.repository.as_ref(),
                    video.finished_take_id(),
                    camera_id,
                    &reason,
                )
                .await?;
            }
        }
        let (_, mut connection) = camera.into_parts();
        connection.request_deletion(self.clock.now());
        self.repository
            .save_camera_connection(&mut unit_of_work, &connection)
            .await?;
        unit_of_work.commit().await?;
        Ok(())
    }
}
