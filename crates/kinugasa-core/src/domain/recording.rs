use chrono::{DateTime, Utc};

use super::{
    CameraIdentityId, ContentHash, ErrorReason, FileSize, MediaType, ObjectKey, RelativePath,
    SessionId, TakeId, ValidationError,
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FinalizedRecording {
    take_id: TakeId,
    camera_identity_id: CameraIdentityId,
    session_id: SessionId,
    started_at: DateTime<Utc>,
    finished_at: DateTime<Utc>,
    relative_path: RelativePath,
    media_type: MediaType,
}

impl FinalizedRecording {
    pub fn new(
        take_id: TakeId,
        camera_identity_id: CameraIdentityId,
        session_id: SessionId,
        started_at: DateTime<Utc>,
        finished_at: DateTime<Utc>,
        relative_path: RelativePath,
        media_type: MediaType,
    ) -> Result<Self, ValidationError> {
        if finished_at < started_at {
            return Err(ValidationError::new(
                "finished_at",
                "must not precede started_at",
            ));
        }
        Ok(Self {
            take_id,
            camera_identity_id,
            session_id,
            started_at,
            finished_at,
            relative_path,
            media_type,
        })
    }

    #[must_use]
    pub const fn take_id(&self) -> TakeId {
        self.take_id
    }

    #[must_use]
    pub const fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_identity_id
    }

    #[must_use]
    pub const fn session_id(&self) -> SessionId {
        self.session_id
    }

    #[must_use]
    pub const fn started_at(&self) -> DateTime<Utc> {
        self.started_at
    }

    #[must_use]
    pub const fn finished_at(&self) -> DateTime<Utc> {
        self.finished_at
    }

    #[must_use]
    pub const fn relative_path(&self) -> &RelativePath {
        &self.relative_path
    }

    #[must_use]
    pub const fn media_type(&self) -> &MediaType {
        &self.media_type
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StoredObject {
    object_key: ObjectKey,
    hash: ContentHash,
    size: FileSize,
}

impl StoredObject {
    #[must_use]
    pub const fn new(object_key: ObjectKey, hash: ContentHash, size: FileSize) -> Self {
        Self {
            object_key,
            hash,
            size,
        }
    }

    #[must_use]
    pub const fn object_key(&self) -> &ObjectKey {
        &self.object_key
    }

    #[must_use]
    pub const fn hash(&self) -> ContentHash {
        self.hash
    }

    #[must_use]
    pub const fn size(&self) -> FileSize {
        self.size
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum UploadOutcome {
    Completed(StoredObject),
    Errored(ErrorReason),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UploadResult {
    take_id: TakeId,
    camera_identity_id: CameraIdentityId,
    session_id: SessionId,
    outcome: UploadOutcome,
    observed_at: DateTime<Utc>,
}

impl UploadResult {
    #[must_use]
    pub const fn new(
        take_id: TakeId,
        camera_identity_id: CameraIdentityId,
        session_id: SessionId,
        outcome: UploadOutcome,
        observed_at: DateTime<Utc>,
    ) -> Self {
        Self {
            take_id,
            camera_identity_id,
            session_id,
            outcome,
            observed_at,
        }
    }

    #[must_use]
    pub const fn take_id(&self) -> TakeId {
        self.take_id
    }

    #[must_use]
    pub const fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_identity_id
    }

    #[must_use]
    pub const fn session_id(&self) -> SessionId {
        self.session_id
    }

    #[must_use]
    pub const fn outcome(&self) -> &UploadOutcome {
        &self.outcome
    }

    #[must_use]
    pub const fn observed_at(&self) -> DateTime<Utc> {
        self.observed_at
    }
}
