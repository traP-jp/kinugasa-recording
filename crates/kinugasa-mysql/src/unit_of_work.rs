use async_trait::async_trait;
use kinugasa_core::ports::{RepositoryError, UnitOfWork, UnitOfWorkFactory};
use sqlx::{MySql, MySqlConnection, Transaction};

use crate::{error::classify_sqlx, store::MySqlRepository};

pub struct MySqlUnitOfWork {
    transaction: Option<Transaction<'static, MySql>>,
}

impl MySqlUnitOfWork {
    pub(crate) fn connection(&mut self) -> Result<&mut MySqlConnection, RepositoryError> {
        self.transaction
            .as_deref_mut()
            .ok_or_else(|| RepositoryError::Unexpected(Box::new(UnitOfWorkAlreadyCompleted)))
    }
}

#[derive(Debug)]
struct UnitOfWorkAlreadyCompleted;

impl std::fmt::Display for UnitOfWorkAlreadyCompleted {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("MySQL unit of work was already completed")
    }
}

impl std::error::Error for UnitOfWorkAlreadyCompleted {}

#[async_trait]
impl UnitOfWork for MySqlUnitOfWork {
    async fn commit(mut self) -> Result<(), RepositoryError> {
        let transaction = self
            .transaction
            .take()
            .ok_or_else(|| RepositoryError::Unexpected(Box::new(UnitOfWorkAlreadyCompleted)))?;
        transaction.commit().await.map_err(classify_sqlx)
    }

    async fn rollback(mut self) -> Result<(), RepositoryError> {
        let transaction = self
            .transaction
            .take()
            .ok_or_else(|| RepositoryError::Unexpected(Box::new(UnitOfWorkAlreadyCompleted)))?;
        transaction.rollback().await.map_err(classify_sqlx)
    }
}

#[async_trait]
impl UnitOfWorkFactory for MySqlRepository {
    type UnitOfWork = MySqlUnitOfWork;

    async fn begin(&self) -> Result<Self::UnitOfWork, RepositoryError> {
        let transaction = self.pool.begin().await.map_err(classify_sqlx)?;
        Ok(MySqlUnitOfWork {
            transaction: Some(transaction),
        })
    }
}
