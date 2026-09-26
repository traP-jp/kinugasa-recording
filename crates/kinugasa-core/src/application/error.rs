use thiserror::Error;

use crate::{
    domain::ValidationError,
    ports::{MediaError, ObjectStorageError, RepositoryError},
};

#[derive(Debug, Error)]
pub enum UseCaseError {
    #[error("invalid argument: {0}")]
    InvalidArgument(#[from] ValidationError),
    #[error(transparent)]
    Repository(#[from] RepositoryError),
    #[error(transparent)]
    Media(#[from] MediaError),
    #[error(transparent)]
    ObjectStorage(#[from] ObjectStorageError),
    #[error("application task failed: {0}")]
    Task(String),
}

impl UseCaseError {
    #[must_use]
    pub fn conflict() -> Self {
        Self::Repository(RepositoryError::Conflict)
    }
}
