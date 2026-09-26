use std::collections::BTreeMap;

use axum::{
    Json, Router,
    extract::{Path, Query, State, rejection::JsonRejection},
    http::{StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64_STANDARD};
use chrono::{DateTime, SecondsFormat, Utc};
use kinugasa_core::{
    application::{Lockfile, OngoingTakeView, UseCaseError},
    domain::{
        Camera, CameraConnectionStatus, FinishedTake, FinishedTakeState, RecordingCameraState,
        Session, SessionState, VideoFile, VideoFileState,
    },
    ports::{
        MediaError, NamedFinishedTakeDetail, ObjectStorageError, RepositoryError,
        RistStatisticsSnapshot, SessionDetail,
    },
};
use serde::{Deserialize, Serialize};
use tokio::sync::watch;
use tracing::error;

use crate::{AppError, Services};

pub fn router(services: Services) -> Router {
    Router::new()
        .route("/healthz", get(health))
        .route("/readyz", get(health))
        .route("/api/sessions", get(list_sessions).post(create_session))
        .route("/api/sessions/{session_name}", get(get_session))
        .route(
            "/api/sessions/{session_name}/cameras",
            get(list_cameras).post(create_camera),
        )
        .route(
            "/api/sessions/{session_name}/cameras/{camera_name}",
            axum::routing::delete(delete_camera),
        )
        .route(
            "/api/sessions/{session_name}/cameras/{camera_name}/connection",
            get(get_camera_connection),
        )
        .route(
            "/api/sessions/{session_name}/rist-statistics",
            get(list_rist_statistics),
        )
        .route(
            "/api/sessions/{session_name}/ongoing-take",
            get(get_ongoing_take),
        )
        .route(
            "/api/sessions/{session_name}/ongoing-take/start",
            post(start_take),
        )
        .route(
            "/api/sessions/{session_name}/ongoing-take/finish",
            post(finish_take),
        )
        .route(
            "/api/sessions/{session_name}/takes",
            get(list_finished_takes),
        )
        .route(
            "/api/sessions/{session_name}/takes/{take_name}",
            get(get_finished_take),
        )
        .route(
            "/api/sessions/{session_name}/preview-access",
            post(create_preview_access),
        )
        .route("/api/sessions/{session_name}/lockfile", get(get_lockfile))
        .with_state(services)
}

pub(crate) async fn serve(
    listener: tokio::net::TcpListener,
    services: Services,
    mut stop: watch::Receiver<bool>,
) -> Result<(), AppError> {
    axum::serve(listener, router(services))
        .with_graceful_shutdown(
            async move { while !*stop.borrow() && stop.changed().await.is_ok() {} },
        )
        .await
        .map_err(AppError::HttpServe)
}

#[derive(Debug)]
struct ApiError {
    status: StatusCode,
    message: String,
}

impl ApiError {
    fn bad_request(error: impl std::fmt::Display) -> Self {
        Self {
            status: StatusCode::BAD_REQUEST,
            message: error.to_string(),
        }
    }

    fn internal(error: impl std::fmt::Display) -> Self {
        error!(%error, "console API request failed");
        Self {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            message: "internal server error".into(),
        }
    }
}

impl From<UseCaseError> for ApiError {
    fn from(error: UseCaseError) -> Self {
        let status = match &error {
            UseCaseError::InvalidArgument(_) => StatusCode::BAD_REQUEST,
            UseCaseError::Repository(RepositoryError::NotFound) => StatusCode::NOT_FOUND,
            UseCaseError::Repository(RepositoryError::Conflict) => StatusCode::CONFLICT,
            UseCaseError::Repository(RepositoryError::Unavailable(_)) => {
                StatusCode::SERVICE_UNAVAILABLE
            }
            UseCaseError::Media(MediaError::NotFound) => StatusCode::NOT_FOUND,
            UseCaseError::Media(MediaError::Conflict) => StatusCode::CONFLICT,
            UseCaseError::Media(MediaError::Timeout | MediaError::Unavailable(_)) => {
                StatusCode::SERVICE_UNAVAILABLE
            }
            UseCaseError::ObjectStorage(ObjectStorageError::Conflict) => StatusCode::CONFLICT,
            UseCaseError::ObjectStorage(ObjectStorageError::Unavailable(_)) => {
                StatusCode::SERVICE_UNAVAILABLE
            }
            _ => StatusCode::INTERNAL_SERVER_ERROR,
        };
        if status.is_server_error() {
            error!(%error, "console API use case failed");
        }
        let message = if status == StatusCode::INTERNAL_SERVER_ERROR {
            "internal server error".into()
        } else {
            error.to_string()
        };
        Self { status, message }
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(ErrorResponse {
                error: self.message,
            }),
        )
            .into_response()
    }
}

#[derive(Serialize)]
struct ErrorResponse {
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CreateResourceRequest {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct StartTakeRequest {
    name: String,
    camera_names: Vec<String>,
}

#[derive(Deserialize)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
struct PageQuery {
    page: u32,
    page_size: u32,
}

impl Default for PageQuery {
    fn default() -> Self {
        Self {
            page: 1,
            page_size: 20,
        }
    }
}

#[derive(Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
struct DeleteQuery {
    force: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PaginationResponse {
    page: u32,
    page_size: u32,
    total: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SessionResponse {
    id: String,
    name: String,
    state: &'static str,
    created_at: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SessionDetailResponse {
    id: String,
    name: String,
    state: &'static str,
    ongoing_take_name: Option<String>,
    created_at: String,
}

#[derive(Serialize)]
struct PageResponse<T> {
    items: Vec<T>,
    pagination: PaginationResponse,
}

#[derive(Serialize)]
struct CameraResponse {
    name: String,
    url: Option<String>,
    status: &'static str,
    error: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RistStatisticsResponse {
    camera_name: String,
    gateway_instance: String,
    flow_id: u32,
    interval_start: String,
    interval_end: String,
    output_packets: u64,
    lost_packets: u64,
    recovered_packets: u64,
    discontinuities: u64,
    stale: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct OngoingTakeResponse {
    id: String,
    name: String,
    started_at: String,
    cameras: Vec<RecordingCameraResponse>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RecordingCameraResponse {
    name: String,
    state: &'static str,
    started_at: String,
    error: Option<String>,
}

#[derive(Serialize)]
#[serde(
    tag = "type",
    rename_all = "lowercase",
    rename_all_fields = "camelCase"
)]
enum OngoingTakeResultResponse {
    Absent,
    Present { ongoing_take: OngoingTakeResponse },
}

async fn health() -> StatusCode {
    StatusCode::NO_CONTENT
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct FinishedTakeResponse {
    id: String,
    name: String,
    state: &'static str,
    started_at: String,
    finished_at: String,
    error: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct FinishedTakeDetailResponse {
    id: String,
    name: String,
    state: &'static str,
    started_at: String,
    finished_at: String,
    error: Option<String>,
    video_files: Vec<VideoFileResponse>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct VideoFileResponse {
    camera_name: String,
    state: &'static str,
    started_at: String,
    finished_at: String,
    object_key: Option<String>,
    hash: Option<String>,
    error: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PreviewAccessResponse {
    url: String,
    access_token: String,
    expires_at: String,
    server_certificate_hashes: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct LockfileResponse {
    schema_version: String,
    bucket: String,
    objects: BTreeMap<String, LockfileEntryResponse>,
}

#[derive(Serialize)]
struct LockfileEntryResponse {
    key: String,
    sha256: String,
    size: u64,
}

async fn list_sessions(
    State(services): State<Services>,
    query: Result<Query<PageQuery>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<PageResponse<SessionResponse>>, ApiError> {
    let Query(query) = query.map_err(ApiError::bad_request)?;
    let page = services
        .sessions
        .list_sessions(query.page, query.page_size)
        .await?;
    Ok(Json(PageResponse {
        items: page.items.iter().map(session_response).collect(),
        pagination: pagination(query.page, query.page_size, page.total),
    }))
}

async fn create_session(
    State(services): State<Services>,
    body: Result<Json<CreateResourceRequest>, JsonRejection>,
) -> Result<impl IntoResponse, ApiError> {
    let request = json_body(body)?;
    let session = services.sessions.create_session(request.name).await?;
    Ok((StatusCode::CREATED, Json(session_response(&session))))
}

async fn get_session(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Json<SessionDetailResponse>, ApiError> {
    let detail = services.sessions.get_session(session_name).await?;
    Ok(Json(session_detail_response(&detail)))
}

async fn list_cameras(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Json<Vec<CameraResponse>>, ApiError> {
    let cameras = services.cameras.list_cameras(session_name).await?;
    Ok(Json(cameras.iter().map(camera_response).collect()))
}

async fn create_camera(
    State(services): State<Services>,
    Path(session_name): Path<String>,
    body: Result<Json<CreateResourceRequest>, JsonRejection>,
) -> Result<impl IntoResponse, ApiError> {
    let request = json_body(body)?;
    let camera = services
        .cameras
        .create_camera(session_name, request.name)
        .await?;
    Ok((StatusCode::CREATED, Json(camera_response(&camera))))
}

async fn get_camera_connection(
    State(services): State<Services>,
    Path((session_name, camera_name)): Path<(String, String)>,
) -> Result<Json<CameraResponse>, ApiError> {
    let camera = services
        .cameras
        .get_camera(session_name, camera_name)
        .await?;
    Ok(Json(camera_response(&camera)))
}

async fn delete_camera(
    State(services): State<Services>,
    Path((session_name, camera_name)): Path<(String, String)>,
    query: Result<Query<DeleteQuery>, axum::extract::rejection::QueryRejection>,
) -> Result<StatusCode, ApiError> {
    let Query(query) = query.map_err(ApiError::bad_request)?;
    services
        .cameras
        .delete_camera(session_name, camera_name, query.force)
        .await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn list_rist_statistics(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Json<Vec<RistStatisticsResponse>>, ApiError> {
    let statistics = services
        .statistics
        .list_rist_statistics(session_name)
        .await?;
    Ok(Json(statistics.iter().map(statistics_response).collect()))
}

async fn get_ongoing_take(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Json<OngoingTakeResultResponse>, ApiError> {
    let response = match services.takes.get_ongoing_take(session_name).await? {
        Some(take) => OngoingTakeResultResponse::Present {
            ongoing_take: ongoing_take_response(&take)?,
        },
        None => OngoingTakeResultResponse::Absent,
    };
    Ok(Json(response))
}

async fn start_take(
    State(services): State<Services>,
    Path(session_name): Path<String>,
    body: Result<Json<StartTakeRequest>, JsonRejection>,
) -> Result<impl IntoResponse, ApiError> {
    let request = json_body(body)?;
    let take = services
        .takes
        .start_take(session_name, request.name, request.camera_names)
        .await?;
    Ok((StatusCode::CREATED, Json(ongoing_take_response(&take)?)))
}

async fn finish_take(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<impl IntoResponse, ApiError> {
    let take = services.takes.finish_take(session_name).await?;
    Ok((StatusCode::ACCEPTED, Json(finished_take_response(&take))))
}

async fn list_finished_takes(
    State(services): State<Services>,
    Path(session_name): Path<String>,
    query: Result<Query<PageQuery>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<PageResponse<FinishedTakeResponse>>, ApiError> {
    let Query(query) = query.map_err(ApiError::bad_request)?;
    let page = services
        .takes
        .list_finished_takes(session_name, query.page, query.page_size)
        .await?;
    Ok(Json(PageResponse {
        items: page.items.iter().map(finished_take_response).collect(),
        pagination: pagination(query.page, query.page_size, page.total),
    }))
}

async fn get_finished_take(
    State(services): State<Services>,
    Path((session_name, take_name)): Path<(String, String)>,
) -> Result<Json<FinishedTakeDetailResponse>, ApiError> {
    let detail = services
        .takes
        .get_finished_take(session_name, take_name)
        .await?;
    Ok(Json(finished_take_detail_response(&detail)?))
}

async fn create_preview_access(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Json<PreviewAccessResponse>, ApiError> {
    let access = services
        .previews
        .create_preview_access(session_name)
        .await?;
    Ok(Json(PreviewAccessResponse {
        url: access.endpoint.to_string(),
        access_token: access.access_token.expose_secret().to_owned(),
        expires_at: timestamp(access.expires_at),
        server_certificate_hashes: access
            .server_certificate_hashes
            .into_iter()
            .map(|hash| hash.to_hex())
            .collect(),
    }))
}

async fn get_lockfile(
    State(services): State<Services>,
    Path(session_name): Path<String>,
) -> Result<Response, ApiError> {
    let lockfile = services.lockfiles.get_lockfile(session_name).await?;
    let response = lockfile_response(lockfile);
    let body = serde_json::to_string(&response).map_err(ApiError::internal)?;
    Ok(([(header::CONTENT_TYPE, "text/plain; charset=utf-8")], body).into_response())
}

fn json_body<T>(body: Result<Json<T>, JsonRejection>) -> Result<T, ApiError> {
    body.map(|Json(value)| value).map_err(ApiError::bad_request)
}

fn pagination(page: u32, page_size: u32, total: u64) -> PaginationResponse {
    PaginationResponse {
        page,
        page_size,
        total,
    }
}

fn timestamp(value: DateTime<Utc>) -> String {
    value.to_rfc3339_opts(SecondsFormat::AutoSi, true)
}

fn session_response(session: &Session) -> SessionResponse {
    SessionResponse {
        id: session.id().to_string(),
        name: session.name().to_string(),
        state: match session.state() {
            SessionState::Active => "active",
            SessionState::Inactive => "inactive",
        },
        created_at: timestamp(session.created_at()),
    }
}

fn session_detail_response(detail: &SessionDetail) -> SessionDetailResponse {
    let session = session_response(&detail.session);
    SessionDetailResponse {
        id: session.id,
        name: session.name,
        state: session.state,
        ongoing_take_name: detail.ongoing_take_name.as_ref().map(ToString::to_string),
        created_at: session.created_at,
    }
}

fn camera_response(camera: &Camera) -> CameraResponse {
    let state = camera.connection().state();
    CameraResponse {
        name: camera.identity().name().to_string(),
        url: state.endpoint().map(ToString::to_string),
        status: match state.status() {
            CameraConnectionStatus::Activating => "activating",
            CameraConnectionStatus::Waiting => "waiting",
            CameraConnectionStatus::Connected => "connected",
            CameraConnectionStatus::Errored => "error",
        },
        error: state.error_reason().map(ToString::to_string),
    }
}

fn statistics_response(value: &RistStatisticsSnapshot) -> RistStatisticsResponse {
    let statistics = &value.statistics;
    RistStatisticsResponse {
        camera_name: statistics.camera_name.to_string(),
        gateway_instance: statistics.gateway_instance.to_string(),
        flow_id: statistics.flow_id,
        interval_start: timestamp(statistics.interval_start),
        interval_end: timestamp(statistics.interval_end),
        output_packets: statistics.output_packets,
        lost_packets: statistics.lost_packets,
        recovered_packets: statistics.recovered_packets,
        discontinuities: statistics.discontinuities,
        stale: value.stale,
    }
}

fn ongoing_take_response(view: &OngoingTakeView) -> Result<OngoingTakeResponse, ApiError> {
    let cameras = view
        .take
        .cameras()
        .iter()
        .map(|camera| {
            let name = view
                .camera_names
                .get(&camera.camera_identity_id())
                .ok_or_else(|| ApiError::internal("ongoing take has no camera name"))?;
            let (state, error) = match camera.state() {
                RecordingCameraState::Recording => ("recording", None),
                RecordingCameraState::Errored(reason) => ("errored", Some(reason.to_string())),
            };
            Ok(RecordingCameraResponse {
                name: name.to_string(),
                state,
                started_at: timestamp(camera.started_at()),
                error,
            })
        })
        .collect::<Result<Vec<_>, ApiError>>()?;
    Ok(OngoingTakeResponse {
        id: view.take.id().to_string(),
        name: view.take.name().to_string(),
        started_at: timestamp(view.take.started_at()),
        cameras,
    })
}

fn finished_take_response(take: &FinishedTake) -> FinishedTakeResponse {
    let (state, error) = finished_state(take.state());
    FinishedTakeResponse {
        id: take.id().to_string(),
        name: take.name().to_string(),
        state,
        started_at: timestamp(take.started_at()),
        finished_at: timestamp(take.finished_at()),
        error,
    }
}

fn finished_take_detail_response(
    named: &NamedFinishedTakeDetail,
) -> Result<FinishedTakeDetailResponse, ApiError> {
    let detail = named.detail();
    let take = finished_take_response(detail.take());
    let video_files = detail
        .video_files()
        .iter()
        .map(|file| {
            let camera_name = named
                .camera_names()
                .get(&file.camera_identity_id())
                .ok_or_else(|| ApiError::internal("video file has no camera name"))?;
            Ok(video_file_response(file, camera_name.to_string()))
        })
        .collect::<Result<Vec<_>, ApiError>>()?;
    Ok(FinishedTakeDetailResponse {
        id: take.id,
        name: take.name,
        state: take.state,
        started_at: take.started_at,
        finished_at: take.finished_at,
        error: take.error,
        video_files,
    })
}

fn finished_state(state: &FinishedTakeState) -> (&'static str, Option<String>) {
    match state {
        FinishedTakeState::Uploading => ("uploading", None),
        FinishedTakeState::Completed => ("completed", None),
        FinishedTakeState::Errored(reason) => ("errored", Some(reason.to_string())),
    }
}

fn video_file_response(file: &VideoFile, camera_name: String) -> VideoFileResponse {
    let (state, object_key, hash, error) = match file.state() {
        VideoFileState::Uploading => ("uploading", None, None, None),
        VideoFileState::Completed(stored) => (
            "completed",
            Some(stored.object_key().to_string()),
            Some(BASE64_STANDARD.encode(stored.hash().as_bytes())),
            None,
        ),
        VideoFileState::Errored(reason) => ("errored", None, None, Some(reason.to_string())),
    };
    VideoFileResponse {
        camera_name,
        state,
        started_at: timestamp(file.started_at()),
        finished_at: timestamp(file.finished_at()),
        object_key,
        hash,
        error,
    }
}

fn lockfile_response(lockfile: Lockfile) -> LockfileResponse {
    LockfileResponse {
        schema_version: lockfile.schema_version,
        bucket: lockfile.bucket,
        objects: lockfile
            .objects
            .into_iter()
            .map(|(path, entry)| {
                (
                    path,
                    LockfileEntryResponse {
                        key: entry.key.to_string(),
                        sha256: entry.sha256.to_hex(),
                        size: entry.size.bytes(),
                    },
                )
            })
            .collect(),
    }
}
