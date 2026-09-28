use std::collections::BTreeMap;

use chrono::{DateTime, NaiveDateTime, Utc};
use kinugasa_core::{
    domain::{
        Camera, CameraConnection, CameraConnectionState, CameraIdentity, CameraIdentityId,
        CameraName, ContentHash, ErrorReason, FileSize, FinalizedRecording, FinishedTake,
        FinishedTakeDetail, FinishedTakeState, MediaProcessId, MediaType, ObjectKey, OngoingTake,
        RecordingCamera, RecordingCameraState, RelativePath, Session, SessionId, SessionName,
        SessionState, StoredObject, TakeId, TakeName, VideoFile, VideoFileState,
    },
    ports::{NamedFinishedTakeDetail, RepositoryError},
};
use sqlx::FromRow;
use url::Url;
use uuid::Uuid;

use crate::error::corrupt;

#[derive(Debug, FromRow)]
pub(crate) struct SessionRow {
    pub id: Uuid,
    pub name: String,
    pub state: String,
    pub created_at: NaiveDateTime,
    pub ongoing_take_name: Option<String>,
}

impl SessionRow {
    pub fn into_detail(self) -> Result<kinugasa_core::ports::SessionDetail, RepositoryError> {
        Ok(kinugasa_core::ports::SessionDetail {
            session: Session::new(
                SessionId::from_uuid(self.id).map_err(corrupt)?,
                SessionName::new(self.name).map_err(corrupt)?,
                parse_session_state(&self.state)?,
                utc(self.created_at),
            ),
            ongoing_take_name: self
                .ongoing_take_name
                .map(TakeName::new)
                .transpose()
                .map_err(corrupt)?,
        })
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct CameraRow {
    pub id: Uuid,
    pub session_id: Uuid,
    pub name: String,
    pub created_at: NaiveDateTime,
    pub url: Option<String>,
    pub virtual_port: Option<u16>,
    pub status: String,
    pub error: Option<String>,
    pub media_process_id: Option<Uuid>,
    pub deletion_requested_at: Option<NaiveDateTime>,
    pub session_name: Option<String>,
}

impl CameraRow {
    pub fn into_camera(self) -> Result<Camera, RepositoryError> {
        let id = CameraIdentityId::from_uuid(self.id).map_err(corrupt)?;
        let identity = CameraIdentity::new(
            id,
            SessionId::from_uuid(self.session_id).map_err(corrupt)?,
            CameraName::new(self.name).map_err(corrupt)?,
            utc(self.created_at),
        );
        let mut connection = CameraConnection::new(
            id,
            parse_camera_connection_state(&self.status, self.url, self.error)?,
        );
        connection.set_virtual_port(self.virtual_port);
        connection.set_media_process_id(
            self.media_process_id
                .map(MediaProcessId::from_uuid)
                .transpose()
                .map_err(corrupt)?,
        );
        if let Some(requested_at) = self.deletion_requested_at {
            connection.request_deletion(utc(requested_at));
        }
        Camera::new(identity, connection).map_err(corrupt)
    }

    pub fn session_name(&self) -> Result<SessionName, RepositoryError> {
        SessionName::new(
            self.session_name
                .clone()
                .ok_or_else(|| invalid("camera resource has no session name"))?,
        )
        .map_err(corrupt)
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct RecordingCameraRow {
    pub take_id: Uuid,
    pub camera_identity_id: Uuid,
    pub state: String,
    pub started_at: NaiveDateTime,
    pub error: Option<String>,
}

impl RecordingCameraRow {
    pub fn into_domain(self) -> Result<RecordingCamera, RepositoryError> {
        Ok(RecordingCamera::new(
            TakeId::from_uuid(self.take_id).map_err(corrupt)?,
            CameraIdentityId::from_uuid(self.camera_identity_id).map_err(corrupt)?,
            match self.state.as_str() {
                "recording" => RecordingCameraState::Recording,
                "errored" => RecordingCameraState::Errored(parse_required_error(self.error)?),
                state => return Err(invalid(format!("unknown recording camera state {state:?}"))),
            },
            utc(self.started_at),
        ))
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct OngoingTakeRow {
    pub id: Uuid,
    pub session_id: Uuid,
    pub name: String,
    pub started_at: NaiveDateTime,
}

impl OngoingTakeRow {
    pub fn into_domain(
        self,
        cameras: Vec<RecordingCamera>,
    ) -> Result<OngoingTake, RepositoryError> {
        OngoingTake::new(
            TakeId::from_uuid(self.id).map_err(corrupt)?,
            SessionId::from_uuid(self.session_id).map_err(corrupt)?,
            TakeName::new(self.name).map_err(corrupt)?,
            utc(self.started_at),
            cameras,
        )
        .map_err(corrupt)
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct FinishedTakeRow {
    pub id: Uuid,
    pub session_id: Uuid,
    pub name: String,
    pub state: String,
    pub started_at: NaiveDateTime,
    pub finished_at: NaiveDateTime,
    pub error: Option<String>,
}

impl FinishedTakeRow {
    pub fn into_domain(self) -> Result<FinishedTake, RepositoryError> {
        FinishedTake::new(
            TakeId::from_uuid(self.id).map_err(corrupt)?,
            SessionId::from_uuid(self.session_id).map_err(corrupt)?,
            TakeName::new(self.name).map_err(corrupt)?,
            match self.state.as_str() {
                "uploading" => FinishedTakeState::Uploading,
                "completed" => FinishedTakeState::Completed,
                "errored" => FinishedTakeState::Errored(parse_required_error(self.error)?),
                state => return Err(invalid(format!("unknown finished take state {state:?}"))),
            },
            utc(self.started_at),
            utc(self.finished_at),
        )
        .map_err(corrupt)
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct VideoFileRow {
    pub take_id: Uuid,
    pub camera_identity_id: Uuid,
    pub state: String,
    pub started_at: NaiveDateTime,
    pub finished_at: NaiveDateTime,
    pub object_key: Option<String>,
    pub hash: Option<Vec<u8>>,
    pub size: Option<u64>,
    pub error: Option<String>,
    pub camera_name: Option<String>,
}

impl VideoFileRow {
    pub fn into_domain(self) -> Result<(VideoFile, Option<CameraName>), RepositoryError> {
        let state = match self.state.as_str() {
            "uploading" => VideoFileState::Uploading,
            "completed" => VideoFileState::Completed(StoredObject::new(
                ObjectKey::new(required(
                    self.object_key,
                    "completed video has no object key",
                )?)
                .map_err(corrupt)?,
                ContentHash::try_from_slice(&required(self.hash, "completed video has no hash")?)
                    .map_err(corrupt)?,
                FileSize::from_bytes(required(self.size, "completed video has no size")?),
            )),
            "errored" => VideoFileState::Errored(parse_required_error(self.error)?),
            state => return Err(invalid(format!("unknown video file state {state:?}"))),
        };
        let file = VideoFile::new(
            TakeId::from_uuid(self.take_id).map_err(corrupt)?,
            CameraIdentityId::from_uuid(self.camera_identity_id).map_err(corrupt)?,
            state,
            utc(self.started_at),
            utc(self.finished_at),
        )
        .map_err(corrupt)?;
        let camera_name = self
            .camera_name
            .map(CameraName::new)
            .transpose()
            .map_err(corrupt)?;
        Ok((file, camera_name))
    }
}

#[derive(Debug, FromRow)]
pub(crate) struct FinalizedRecordingRow {
    pub take_id: Uuid,
    pub camera_identity_id: Uuid,
    pub session_id: Uuid,
    pub started_at: NaiveDateTime,
    pub finished_at: NaiveDateTime,
    pub relative_path: String,
    pub media_type: String,
}

impl FinalizedRecordingRow {
    pub fn into_domain(self) -> Result<FinalizedRecording, RepositoryError> {
        FinalizedRecording::new(
            TakeId::from_uuid(self.take_id).map_err(corrupt)?,
            CameraIdentityId::from_uuid(self.camera_identity_id).map_err(corrupt)?,
            SessionId::from_uuid(self.session_id).map_err(corrupt)?,
            utc(self.started_at),
            utc(self.finished_at),
            RelativePath::new(self.relative_path).map_err(corrupt)?,
            MediaType::new(self.media_type).map_err(corrupt)?,
        )
        .map_err(corrupt)
    }
}

pub(crate) fn finished_detail(
    take: FinishedTake,
    rows: Vec<VideoFileRow>,
) -> Result<NamedFinishedTakeDetail, RepositoryError> {
    let mut files = Vec::with_capacity(rows.len());
    let mut names = BTreeMap::new();
    for row in rows {
        let (file, name) = row.into_domain()?;
        names.insert(
            file.camera_identity_id(),
            name.ok_or_else(|| invalid("finished video has no camera name"))?,
        );
        files.push(file);
    }
    let detail = FinishedTakeDetail::new(take, files).map_err(corrupt)?;
    NamedFinishedTakeDetail::new(detail, names).map_err(corrupt)
}

pub(crate) const fn naive(value: DateTime<Utc>) -> NaiveDateTime {
    value.naive_utc()
}

pub(crate) const fn utc(value: NaiveDateTime) -> DateTime<Utc> {
    value.and_utc()
}

fn parse_session_state(value: &str) -> Result<SessionState, RepositoryError> {
    match value {
        "active" => Ok(SessionState::Active),
        "inactive" => Ok(SessionState::Inactive),
        value => Err(invalid(format!("unknown session state {value:?}"))),
    }
}

fn parse_camera_connection_state(
    status: &str,
    endpoint: Option<String>,
    error: Option<String>,
) -> Result<CameraConnectionState, RepositoryError> {
    if status == "activating" {
        if endpoint.is_some() || error.is_some() {
            return Err(invalid("activating camera contains endpoint or error"));
        }
        return Ok(CameraConnectionState::Activating);
    }
    let endpoint =
        Url::parse(&required(endpoint, "provisioned camera has no endpoint")?).map_err(corrupt)?;
    match status {
        "waiting" if error.is_none() => Ok(CameraConnectionState::Waiting { endpoint }),
        "connected" if error.is_none() => Ok(CameraConnectionState::Connected { endpoint }),
        "error" => Ok(CameraConnectionState::Errored {
            endpoint,
            reason: parse_required_error(error)?,
        }),
        status => Err(invalid(format!(
            "invalid camera connection state {status:?}"
        ))),
    }
}

fn parse_required_error(value: Option<String>) -> Result<ErrorReason, RepositoryError> {
    ErrorReason::new(required(value, "errored row has no error reason")?).map_err(corrupt)
}

fn required<T>(value: Option<T>, message: &'static str) -> Result<T, RepositoryError> {
    value.ok_or_else(|| invalid(message))
}

pub(crate) fn invalid(message: impl Into<String>) -> RepositoryError {
    corrupt(StoredDataError(message.into()))
}

#[derive(Debug)]
struct StoredDataError(String);

impl std::fmt::Display for StoredDataError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for StoredDataError {}
