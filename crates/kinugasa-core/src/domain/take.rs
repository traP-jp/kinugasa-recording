use std::collections::HashSet;

use chrono::{DateTime, Utc};

use super::{
    CameraIdentityId, ErrorReason, SessionId, StoredObject, TakeId, TakeName, ValidationError,
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RecordingCameraState {
    Recording,
    Errored(ErrorReason),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RecordingCamera {
    ongoing_take_id: TakeId,
    camera_identity_id: CameraIdentityId,
    state: RecordingCameraState,
    started_at: DateTime<Utc>,
}

impl RecordingCamera {
    #[must_use]
    pub const fn new(
        ongoing_take_id: TakeId,
        camera_identity_id: CameraIdentityId,
        state: RecordingCameraState,
        started_at: DateTime<Utc>,
    ) -> Self {
        Self {
            ongoing_take_id,
            camera_identity_id,
            state,
            started_at,
        }
    }

    #[must_use]
    pub const fn ongoing_take_id(&self) -> TakeId {
        self.ongoing_take_id
    }

    #[must_use]
    pub const fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_identity_id
    }

    #[must_use]
    pub const fn state(&self) -> &RecordingCameraState {
        &self.state
    }

    #[must_use]
    pub const fn started_at(&self) -> DateTime<Utc> {
        self.started_at
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OngoingTake {
    id: TakeId,
    session_id: SessionId,
    name: TakeName,
    started_at: DateTime<Utc>,
    cameras: Vec<RecordingCamera>,
}

impl OngoingTake {
    pub fn new(
        id: TakeId,
        session_id: SessionId,
        name: TakeName,
        started_at: DateTime<Utc>,
        cameras: Vec<RecordingCamera>,
    ) -> Result<Self, ValidationError> {
        if cameras.is_empty() {
            return Err(ValidationError::new(
                "cameras",
                "must contain at least one camera",
            ));
        }
        let mut camera_ids = HashSet::with_capacity(cameras.len());
        for camera in &cameras {
            if camera.ongoing_take_id() != id {
                return Err(ValidationError::new(
                    "cameras.ongoing_take_id",
                    "must match the ongoing take",
                ));
            }
            if !camera_ids.insert(camera.camera_identity_id()) {
                return Err(ValidationError::new(
                    "cameras.camera_identity_id",
                    "must be unique within the take",
                ));
            }
        }
        Ok(Self {
            id,
            session_id,
            name,
            started_at,
            cameras,
        })
    }

    #[must_use]
    pub const fn id(&self) -> TakeId {
        self.id
    }

    #[must_use]
    pub const fn session_id(&self) -> SessionId {
        self.session_id
    }

    #[must_use]
    pub const fn name(&self) -> &TakeName {
        &self.name
    }

    #[must_use]
    pub const fn started_at(&self) -> DateTime<Utc> {
        self.started_at
    }

    #[must_use]
    pub fn cameras(&self) -> &[RecordingCamera] {
        &self.cameras
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FinishedTakeState {
    Uploading,
    Completed,
    Errored(ErrorReason),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FinishedTake {
    id: TakeId,
    session_id: SessionId,
    name: TakeName,
    state: FinishedTakeState,
    started_at: DateTime<Utc>,
    finished_at: DateTime<Utc>,
}

impl FinishedTake {
    pub fn new(
        id: TakeId,
        session_id: SessionId,
        name: TakeName,
        state: FinishedTakeState,
        started_at: DateTime<Utc>,
        finished_at: DateTime<Utc>,
    ) -> Result<Self, ValidationError> {
        if finished_at < started_at {
            return Err(ValidationError::new(
                "finished_at",
                "must not precede started_at",
            ));
        }
        Ok(Self {
            id,
            session_id,
            name,
            state,
            started_at,
            finished_at,
        })
    }

    #[must_use]
    pub const fn id(&self) -> TakeId {
        self.id
    }

    #[must_use]
    pub const fn session_id(&self) -> SessionId {
        self.session_id
    }

    #[must_use]
    pub const fn name(&self) -> &TakeName {
        &self.name
    }

    #[must_use]
    pub const fn state(&self) -> &FinishedTakeState {
        &self.state
    }

    #[must_use]
    pub const fn started_at(&self) -> DateTime<Utc> {
        self.started_at
    }

    #[must_use]
    pub const fn finished_at(&self) -> DateTime<Utc> {
        self.finished_at
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VideoFileState {
    Uploading,
    Completed(StoredObject),
    Errored(ErrorReason),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VideoFile {
    finished_take_id: TakeId,
    camera_identity_id: CameraIdentityId,
    state: VideoFileState,
    started_at: DateTime<Utc>,
    finished_at: DateTime<Utc>,
}

impl VideoFile {
    pub fn new(
        finished_take_id: TakeId,
        camera_identity_id: CameraIdentityId,
        state: VideoFileState,
        started_at: DateTime<Utc>,
        finished_at: DateTime<Utc>,
    ) -> Result<Self, ValidationError> {
        if finished_at < started_at {
            return Err(ValidationError::new(
                "finished_at",
                "must not precede started_at",
            ));
        }
        Ok(Self {
            finished_take_id,
            camera_identity_id,
            state,
            started_at,
            finished_at,
        })
    }

    #[must_use]
    pub const fn finished_take_id(&self) -> TakeId {
        self.finished_take_id
    }

    #[must_use]
    pub const fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_identity_id
    }

    #[must_use]
    pub const fn state(&self) -> &VideoFileState {
        &self.state
    }

    #[must_use]
    pub const fn started_at(&self) -> DateTime<Utc> {
        self.started_at
    }

    #[must_use]
    pub const fn finished_at(&self) -> DateTime<Utc> {
        self.finished_at
    }
}

/// The detailed form validates the cross-row invariants from the v2 schema.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FinishedTakeDetail {
    take: FinishedTake,
    video_files: Vec<VideoFile>,
}

impl FinishedTakeDetail {
    pub fn new(take: FinishedTake, video_files: Vec<VideoFile>) -> Result<Self, ValidationError> {
        let mut camera_ids = HashSet::with_capacity(video_files.len());
        let mut uploading = 0;
        let mut errored = 0;
        for file in &video_files {
            if file.finished_take_id() != take.id() {
                return Err(ValidationError::new(
                    "video_files.finished_take_id",
                    "must match the finished take",
                ));
            }
            if !camera_ids.insert(file.camera_identity_id()) {
                return Err(ValidationError::new(
                    "video_files.camera_identity_id",
                    "must be unique within the take",
                ));
            }
            match file.state() {
                VideoFileState::Uploading => uploading += 1,
                VideoFileState::Errored(_) => errored += 1,
                VideoFileState::Completed(_) => {}
            }
        }

        match take.state() {
            FinishedTakeState::Uploading if uploading == 0 => {
                return Err(ValidationError::new(
                    "video_files",
                    "an uploading take must contain an uploading file",
                ));
            }
            FinishedTakeState::Completed
                if video_files.is_empty() || uploading != 0 || errored != 0 =>
            {
                return Err(ValidationError::new(
                    "video_files",
                    "a completed take must contain only completed files",
                ));
            }
            FinishedTakeState::Errored(_) if uploading != 0 => {
                return Err(ValidationError::new(
                    "video_files",
                    "an errored take cannot contain uploading files",
                ));
            }
            _ => {}
        }

        Ok(Self { take, video_files })
    }

    #[must_use]
    pub const fn take(&self) -> &FinishedTake {
        &self.take
    }

    #[must_use]
    pub fn video_files(&self) -> &[VideoFile] {
        &self.video_files
    }

    #[must_use]
    pub fn into_parts(self) -> (FinishedTake, Vec<VideoFile>) {
        (self.take, self.video_files)
    }
}

#[cfg(test)]
mod tests {
    use chrono::TimeZone;

    use super::*;
    use crate::domain::{ContentHash, FileSize, ObjectKey};

    #[test]
    fn completed_video_file_carries_all_object_metadata() {
        let now = Utc.timestamp_opt(1_700_000_000, 0).unwrap();
        let take_id = TakeId::new_v7();
        let stored = StoredObject::new(
            ObjectKey::new("recording/video.ts").unwrap(),
            ContentHash::from_bytes([7; 32]),
            FileSize::from_bytes(42),
        );
        let file = VideoFile::new(
            take_id,
            CameraIdentityId::new_v7(),
            VideoFileState::Completed(stored),
            now,
            now,
        )
        .unwrap();
        assert!(matches!(file.state(), VideoFileState::Completed(_)));
    }
}
