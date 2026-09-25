use std::{sync::Arc, time::Duration};

use crate::{
    application::{UseCaseError, UseCaseError::Repository},
    domain::{SessionName, SessionState},
    ports::{
        PreviewAccess, PreviewAccessRequest, PreviewService as PreviewPort, RepositoryError,
        SessionRepository, UnitOfWork, UnitOfWorkFactory,
    },
};

pub struct PreviewUseCases<F, R, M> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    media: Arc<M>,
    token_lifetime: Duration,
}

impl<F, R, M> PreviewUseCases<F, R, M> {
    pub fn new(
        unit_of_work_factory: Arc<F>,
        repository: Arc<R>,
        media: Arc<M>,
        token_lifetime: Duration,
    ) -> Result<Self, crate::domain::ValidationError> {
        if token_lifetime.is_zero() {
            return Err(crate::domain::ValidationError::new(
                "preview_token_lifetime",
                "must be positive",
            ));
        }
        Ok(Self {
            unit_of_work_factory,
            repository,
            media,
            token_lifetime,
        })
    }
}

impl<F, R, M> PreviewUseCases<F, R, M>
where
    F: UnitOfWorkFactory,
    R: SessionRepository<F::UnitOfWork>,
    M: PreviewPort,
{
    pub async fn create_preview_access(
        &self,
        session_name: String,
    ) -> Result<PreviewAccess, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let session = self
            .repository
            .get_session(&mut unit_of_work, &session_name)
            .await?
            .session;
        if session.state() != SessionState::Active {
            return Err(Repository(RepositoryError::Conflict));
        }
        unit_of_work.commit().await?;
        Ok(self
            .media
            .issue_preview_access(&PreviewAccessRequest {
                session_id: session.id(),
                valid_for: self.token_lifetime,
            })
            .await?)
    }
}
