use std::sync::Arc;

use crate::{
    application::UseCaseError,
    domain::{Session, SessionName, SessionState},
    ports::{
        Clock, IdGenerator, Page, PageRequest, SessionDetail, SessionRepository, UnitOfWork,
        UnitOfWorkFactory,
    },
};

pub struct SessionUseCases<F, R, C, I> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    clock: Arc<C>,
    ids: Arc<I>,
}

impl<F, R, C, I> SessionUseCases<F, R, C, I> {
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

impl<F, R, C, I> SessionUseCases<F, R, C, I>
where
    F: UnitOfWorkFactory,
    R: SessionRepository<F::UnitOfWork>,
    C: Clock,
    I: IdGenerator,
{
    pub async fn create_session(&self, name: String) -> Result<Session, UseCaseError> {
        let session = Session::new(
            self.ids.session_id(),
            SessionName::new(name)?,
            SessionState::Active,
            self.clock.now(),
        );
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        self.repository
            .create_session(&mut unit_of_work, &session)
            .await?;
        unit_of_work.commit().await?;
        Ok(session)
    }

    pub async fn list_sessions(
        &self,
        page: u32,
        page_size: u32,
    ) -> Result<Page<Session>, UseCaseError> {
        let request = PageRequest::new(page, page_size)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .list_sessions(&mut unit_of_work, request)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }

    pub async fn get_session(&self, name: String) -> Result<SessionDetail, UseCaseError> {
        let name = SessionName::new(name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .get_session(&mut unit_of_work, &name)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }
}

#[cfg(test)]
mod tests {
    use std::{
        str::FromStr,
        sync::{
            Arc, Mutex,
            atomic::{AtomicBool, Ordering},
        },
    };

    use async_trait::async_trait;
    use chrono::{DateTime, TimeZone, Utc};

    use super::*;
    use crate::{
        domain::{CameraIdentityId, SessionId, TakeId},
        ports::{RepositoryError, UnitOfWork},
    };

    struct TestUnitOfWork {
        committed: Arc<AtomicBool>,
    }

    #[async_trait]
    impl UnitOfWork for TestUnitOfWork {
        async fn commit(self) -> Result<(), RepositoryError> {
            self.committed.store(true, Ordering::SeqCst);
            Ok(())
        }

        async fn rollback(self) -> Result<(), RepositoryError> {
            Ok(())
        }
    }

    struct TestUnitOfWorkFactory {
        committed: Arc<AtomicBool>,
    }

    #[async_trait]
    impl UnitOfWorkFactory for TestUnitOfWorkFactory {
        type UnitOfWork = TestUnitOfWork;

        async fn begin(&self) -> Result<Self::UnitOfWork, RepositoryError> {
            Ok(TestUnitOfWork {
                committed: Arc::clone(&self.committed),
            })
        }
    }

    #[derive(Default)]
    struct TestSessionRepository {
        created: Mutex<Option<Session>>,
    }

    #[async_trait]
    impl SessionRepository<TestUnitOfWork> for TestSessionRepository {
        async fn create_session(
            &self,
            _unit_of_work: &mut TestUnitOfWork,
            session: &Session,
        ) -> Result<(), RepositoryError> {
            *self.created.lock().unwrap() = Some(session.clone());
            Ok(())
        }

        async fn list_sessions(
            &self,
            _unit_of_work: &mut TestUnitOfWork,
            _page: PageRequest,
        ) -> Result<Page<Session>, RepositoryError> {
            Ok(Page {
                items: Vec::new(),
                total: 0,
            })
        }

        async fn get_session(
            &self,
            _unit_of_work: &mut TestUnitOfWork,
            _name: &SessionName,
        ) -> Result<SessionDetail, RepositoryError> {
            Err(RepositoryError::NotFound)
        }
    }

    struct TestClock(DateTime<Utc>);

    impl Clock for TestClock {
        fn now(&self) -> DateTime<Utc> {
            self.0
        }
    }

    struct TestIds(SessionId);

    impl IdGenerator for TestIds {
        fn session_id(&self) -> SessionId {
            self.0
        }

        fn camera_identity_id(&self) -> CameraIdentityId {
            unreachable!()
        }

        fn take_id(&self) -> TakeId {
            unreachable!()
        }
    }

    #[test]
    fn create_session_persists_and_commits_one_unit_of_work() {
        let committed = Arc::new(AtomicBool::new(false));
        let factory = Arc::new(TestUnitOfWorkFactory {
            committed: Arc::clone(&committed),
        });
        let repository = Arc::new(TestSessionRepository::default());
        let now = Utc.timestamp_opt(1_700_000_000, 0).unwrap();
        let id = SessionId::from_str("019c240d-a6de-7de0-a826-0f26e8803fc0").unwrap();
        let service = SessionUseCases::new(
            factory,
            Arc::clone(&repository),
            Arc::new(TestClock(now)),
            Arc::new(TestIds(id)),
        );

        let created = tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(service.create_session("studio-a".to_owned()))
            .unwrap();

        assert_eq!(created.id(), id);
        assert_eq!(created.created_at(), now);
        assert_eq!(repository.created.lock().unwrap().as_ref(), Some(&created));
        assert!(committed.load(Ordering::SeqCst));
    }
}
