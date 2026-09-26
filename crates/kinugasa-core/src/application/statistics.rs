use std::sync::Arc;

use crate::{
    application::UseCaseError,
    domain::SessionName,
    ports::{
        RistStatisticsSnapshot, RistStatisticsSource, SessionRepository, UnitOfWork,
        UnitOfWorkFactory,
    },
};

pub struct StatisticsUseCases<F, R, M> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    media: Arc<M>,
}

impl<F, R, M> StatisticsUseCases<F, R, M> {
    #[must_use]
    pub fn new(unit_of_work_factory: Arc<F>, repository: Arc<R>, media: Arc<M>) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            media,
        }
    }
}

impl<F, R, M> StatisticsUseCases<F, R, M>
where
    F: UnitOfWorkFactory,
    R: SessionRepository<F::UnitOfWork>,
    M: RistStatisticsSource,
{
    pub async fn list_rist_statistics(
        &self,
        session_name: String,
    ) -> Result<Vec<RistStatisticsSnapshot>, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        self.repository
            .get_session(&mut unit_of_work, &session_name)
            .await?;
        unit_of_work.commit().await?;
        Ok(self.media.list_rist_statistics(&session_name).await?)
    }
}
