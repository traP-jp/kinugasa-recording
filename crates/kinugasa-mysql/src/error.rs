use kinugasa_core::ports::RepositoryError;
pub(crate) fn classify_sqlx(error: sqlx::Error) -> RepositoryError {
    if let Some(database) = error.as_database_error() {
        if database.is_unique_violation() || database.is_foreign_key_violation() {
            return RepositoryError::Conflict;
        }
        if let Some(mysql) = database.try_downcast_ref::<sqlx::mysql::MySqlDatabaseError>()
            && matches!(
                mysql.number(),
                1040 | 1042 | 1043 | 1047 | 1158..=1161 | 1205 | 1213
            )
        {
            return RepositoryError::Unavailable(Box::new(error));
        }
    }
    match error {
        error @ (sqlx::Error::Io(_)
        | sqlx::Error::Tls(_)
        | sqlx::Error::PoolTimedOut
        | sqlx::Error::PoolClosed
        | sqlx::Error::WorkerCrashed) => RepositoryError::Unavailable(Box::new(error)),
        error @ (sqlx::Error::ColumnIndexOutOfBounds { .. }
        | sqlx::Error::ColumnNotFound(_)
        | sqlx::Error::ColumnDecode { .. }
        | sqlx::Error::Decode(_)
        | sqlx::Error::TypeNotFound { .. }) => RepositoryError::CorruptData(Box::new(error)),
        error => RepositoryError::Unexpected(Box::new(error)),
    }
}

pub(crate) fn corrupt(error: impl std::error::Error + Send + Sync + 'static) -> RepositoryError {
    RepositoryError::CorruptData(Box::new(error))
}
