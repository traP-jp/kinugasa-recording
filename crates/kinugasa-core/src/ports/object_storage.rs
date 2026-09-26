use std::error::Error;

use async_trait::async_trait;
use thiserror::Error;

use crate::domain::{FinalizedRecording, StoredObject};

type BoxError = Box<dyn Error + Send + Sync + 'static>;

#[derive(Debug, Error)]
pub enum ObjectStorageError {
    #[error("object already exists with different contents")]
    Conflict,
    #[error("object storage is unavailable")]
    Unavailable(#[source] BoxError),
    #[error("unexpected object-storage failure")]
    Unexpected(#[source] BoxError),
}

#[async_trait]
pub trait ObjectStorage: Send + Sync {
    /// Uploads a finalized local recording. Implementations must be idempotent
    /// for the same recording identity and contents.
    async fn upload(
        &self,
        recording: &FinalizedRecording,
    ) -> Result<StoredObject, ObjectStorageError>;
}
