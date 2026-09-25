use std::{collections::BTreeMap, error::Error, num::NonZeroU32};

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use thiserror::Error;

use crate::domain::{
    Camera, CameraConnectionState, CameraIdentityId, CameraName, ErrorReason, FinalizedRecording,
    FinishedTake, FinishedTakeDetail, OngoingTake, RecordingCamera, Session, SessionName, TakeName,
    UploadResult, VideoFile, VideoFileState,
};

type BoxError = Box<dyn Error + Send + Sync + 'static>;

/// Stable application-facing repository failures. SQL driver errors remain
/// adapter details and are attached only as sources.
#[derive(Debug, Error)]
pub enum RepositoryError {
    #[error("repository entity was not found")]
    NotFound,
    #[error("repository operation conflicts with current state")]
    Conflict,
    #[error("stored data violates the domain model")]
    CorruptData(#[source] BoxError),
    #[error("repository is unavailable")]
    Unavailable(#[source] BoxError),
    #[error("unexpected repository failure")]
    Unexpected(#[source] BoxError),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PageRequest {
    page: u32,
    page_size: u32,
}

impl PageRequest {
    pub const MAX_PAGE_SIZE: u32 = 100;

    pub fn new(page: u32, page_size: u32) -> Result<Self, crate::domain::ValidationError> {
        if page == 0 {
            return Err(crate::domain::ValidationError::new(
                "page",
                "must be at least 1",
            ));
        }
        if page_size == 0 || page_size > Self::MAX_PAGE_SIZE {
            return Err(crate::domain::ValidationError::new(
                "page_size",
                "must be between 1 and 100",
            ));
        }
        Ok(Self { page, page_size })
    }

    #[must_use]
    pub const fn page(self) -> u32 {
        self.page
    }

    #[must_use]
    pub const fn page_size(self) -> u32 {
        self.page_size
    }

    #[must_use]
    pub const fn offset(self) -> u64 {
        (self.page - 1) as u64 * self.page_size as u64
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Page<T> {
    pub items: Vec<T>,
    pub total: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionDetail {
    pub session: Session,
    pub ongoing_take_name: Option<TakeName>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NamedFinishedTakeDetail {
    detail: FinishedTakeDetail,
    camera_names: BTreeMap<CameraIdentityId, CameraName>,
}

impl NamedFinishedTakeDetail {
    pub fn new(
        detail: FinishedTakeDetail,
        camera_names: BTreeMap<CameraIdentityId, CameraName>,
    ) -> Result<Self, crate::domain::ValidationError> {
        if detail.video_files().len() != camera_names.len()
            || detail
                .video_files()
                .iter()
                .any(|file| !camera_names.contains_key(&file.camera_identity_id()))
        {
            return Err(crate::domain::ValidationError::new(
                "camera_names",
                "must contain exactly one name for every video file",
            ));
        }
        Ok(Self {
            detail,
            camera_names,
        })
    }

    #[must_use]
    pub const fn detail(&self) -> &FinishedTakeDetail {
        &self.detail
    }

    #[must_use]
    pub const fn camera_names(&self) -> &BTreeMap<CameraIdentityId, CameraName> {
        &self.camera_names
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CameraResource {
    pub session_name: SessionName,
    pub camera: Camera,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CameraDeletionRequest {
    pub session_name: SessionName,
    pub camera_name: CameraName,
    pub requested_at: DateTime<Utc>,
    pub force: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FinishTakeRequest {
    pub session_name: SessionName,
    pub finished_at: DateTime<Utc>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LockfileObject {
    pub logical_path: crate::domain::RelativePath,
    pub stored: crate::domain::StoredObject,
}

/// A durable upload intent reconstructed from a finalized recording and its
/// uploading VideoFile row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PendingUpload {
    recording: FinalizedRecording,
    video_file: VideoFile,
}

impl PendingUpload {
    pub fn new(
        recording: FinalizedRecording,
        video_file: VideoFile,
    ) -> Result<Self, crate::domain::ValidationError> {
        if !matches!(video_file.state(), VideoFileState::Uploading) {
            return Err(crate::domain::ValidationError::new(
                "video_file.state",
                "must be uploading",
            ));
        }
        if recording.take_id() != video_file.finished_take_id()
            || recording.camera_identity_id() != video_file.camera_identity_id()
        {
            return Err(crate::domain::ValidationError::new(
                "video_file",
                "must identify the finalized recording",
            ));
        }
        Ok(Self {
            recording,
            video_file,
        })
    }

    #[must_use]
    pub const fn recording(&self) -> &FinalizedRecording {
        &self.recording
    }

    #[must_use]
    pub const fn video_file(&self) -> &VideoFile {
        &self.video_file
    }
}

/// Transaction context carried by one application use case.
///
/// Commit and rollback consume the value, preventing accidental use after the
/// transaction has ended. Implementations should also roll back on drop.
#[async_trait]
pub trait UnitOfWork: Send + Sized {
    async fn commit(self) -> Result<(), RepositoryError>;

    async fn rollback(self) -> Result<(), RepositoryError>;
}

/// Issues database transaction contexts. The application layer owns the
/// begin/use/commit lifecycle.
#[async_trait]
pub trait UnitOfWorkFactory: Send + Sync {
    type UnitOfWork: UnitOfWork;

    async fn begin(&self) -> Result<Self::UnitOfWork, RepositoryError>;
}

/// Session persistence. Ordering is part of the port contract: newest first,
/// then name ascending, matching the v2 console API.
#[async_trait]
pub trait SessionRepository<U: UnitOfWork>: Send + Sync {
    async fn create_session(
        &self,
        unit_of_work: &mut U,
        session: &Session,
    ) -> Result<(), RepositoryError>;

    async fn list_sessions(
        &self,
        unit_of_work: &mut U,
        page: PageRequest,
    ) -> Result<Page<Session>, RepositoryError>;

    async fn get_session(
        &self,
        unit_of_work: &mut U,
        name: &SessionName,
    ) -> Result<SessionDetail, RepositoryError>;
}

/// CameraIdentity and CameraConnection are persisted atomically by this port.
#[async_trait]
pub trait CameraRepository<U: UnitOfWork>: Send + Sync {
    async fn create_camera(
        &self,
        unit_of_work: &mut U,
        camera: &Camera,
    ) -> Result<(), RepositoryError>;

    async fn list_cameras(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
    ) -> Result<Vec<Camera>, RepositoryError>;

    /// Includes connections with a pending deletion request. Used to restore
    /// media resources and finish interrupted deletion after process restart.
    async fn list_camera_resources(
        &self,
        unit_of_work: &mut U,
    ) -> Result<Vec<CameraResource>, RepositoryError>;

    async fn get_camera(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
        camera_name: &CameraName,
    ) -> Result<Camera, RepositoryError>;

    async fn request_camera_deletion(
        &self,
        unit_of_work: &mut U,
        request: &CameraDeletionRequest,
    ) -> Result<CameraIdentityId, RepositoryError>;

    async fn complete_camera_deletion(
        &self,
        unit_of_work: &mut U,
        camera_id: CameraIdentityId,
    ) -> Result<(), RepositoryError>;

    async fn set_camera_connection_state(
        &self,
        unit_of_work: &mut U,
        camera_id: CameraIdentityId,
        state: &CameraConnectionState,
    ) -> Result<(), RepositoryError>;
}

/// Take-level methods are intentionally coarse grained so aggregate invariants
/// are enforced while participating in the caller's unit of work.
#[async_trait]
pub trait TakeRepository<U: UnitOfWork>: Send + Sync {
    async fn create_ongoing_take(
        &self,
        unit_of_work: &mut U,
        take: &OngoingTake,
    ) -> Result<(), RepositoryError>;

    async fn get_ongoing_take(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
    ) -> Result<Option<OngoingTake>, RepositoryError>;

    async fn finish_take(
        &self,
        unit_of_work: &mut U,
        request: &FinishTakeRequest,
    ) -> Result<FinishedTake, RepositoryError>;

    async fn list_finished_takes(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
        page: PageRequest,
    ) -> Result<Page<FinishedTake>, RepositoryError>;

    async fn get_finished_take(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
        take_name: &TakeName,
    ) -> Result<NamedFinishedTakeDetail, RepositoryError>;
}

/// Applies media/recording outcomes idempotently and converges take state.
#[async_trait]
pub trait RecordingRepository<U: UnitOfWork>: Send + Sync {
    /// Lists recordings left active by the current database state. The startup
    /// use case marks every returned item errored; it must not ask media to
    /// resume them.
    async fn list_active_recordings(
        &self,
        unit_of_work: &mut U,
    ) -> Result<Vec<RecordingCamera>, RepositoryError>;

    async fn mark_recording_started(
        &self,
        unit_of_work: &mut U,
        take_id: crate::domain::TakeId,
        camera_id: CameraIdentityId,
        started_at: DateTime<Utc>,
    ) -> Result<(), RepositoryError>;

    async fn mark_recording_errored(
        &self,
        unit_of_work: &mut U,
        take_id: crate::domain::TakeId,
        camera_id: CameraIdentityId,
        reason: &ErrorReason,
    ) -> Result<(), RepositoryError>;

    async fn stage_finalized_recording(
        &self,
        unit_of_work: &mut U,
        recording: &FinalizedRecording,
    ) -> Result<(), RepositoryError>;

    async fn apply_upload_result(
        &self,
        unit_of_work: &mut U,
        result: &UploadResult,
    ) -> Result<(), RepositoryError>;

    /// Lists finalized files whose durable desired state is still Uploading.
    /// Temporary object-storage failures leave these rows in this set.
    async fn list_pending_uploads(
        &self,
        unit_of_work: &mut U,
        limit: NonZeroU32,
    ) -> Result<Vec<PendingUpload>, RepositoryError>;
}

#[async_trait]
pub trait LockfileRepository<U: UnitOfWork>: Send + Sync {
    async fn list_lockfile_objects(
        &self,
        unit_of_work: &mut U,
        session_name: &SessionName,
    ) -> Result<Vec<LockfileObject>, RepositoryError>;
}

/// Convenience bound for application services that need the complete store.
pub trait Repository<U: UnitOfWork>:
    SessionRepository<U>
    + CameraRepository<U>
    + TakeRepository<U>
    + RecordingRepository<U>
    + LockfileRepository<U>
{
}

impl<T, U> Repository<U> for T
where
    U: UnitOfWork,
    T: SessionRepository<U>
        + CameraRepository<U>
        + TakeRepository<U>
        + RecordingRepository<U>
        + LockfileRepository<U>,
{
}
