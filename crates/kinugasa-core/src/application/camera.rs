use std::sync::Arc;

use crate::{
    application::UseCaseError,
    domain::{
        Camera, CameraConnection, CameraConnectionState, CameraIdentity, CameraName, SessionName,
        SessionState,
    },
    ports::{
        CameraDeletionRequest, CameraRepository, Clock, IdGenerator, SessionRepository, UnitOfWork,
        UnitOfWorkFactory,
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
    R: CameraRepository<F::UnitOfWork> + SessionRepository<F::UnitOfWork>,
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
            .get_session(&mut unit_of_work, &session_name)
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
        let request = CameraDeletionRequest {
            session_name: SessionName::new(session_name)?,
            camera_name: CameraName::new(camera_name)?,
            requested_at: self.clock.now(),
            force,
        };
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let _camera_id = self
            .repository
            .request_camera_deletion(&mut unit_of_work, &request)
            .await?;
        unit_of_work.commit().await?;
        Ok(())
    }
}
