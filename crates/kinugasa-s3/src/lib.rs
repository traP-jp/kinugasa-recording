//! S3-backed implementation of the kinugasa object-storage port.

#![forbid(unsafe_code)]

use std::{
    error::Error,
    path::{Path, PathBuf},
    sync::Arc,
};

use async_trait::async_trait;
use aws_config::{BehaviorVersion, Region};
use aws_sdk_s3::{Client, primitives::ByteStream};
use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64_STANDARD};
use kinugasa_core::{
    domain::{ContentHash, FileSize, FinalizedRecording, ObjectKey, StoredObject},
    ports::{ObjectStorage, ObjectStorageError},
};
use sha2::{Digest, Sha256};
use thiserror::Error;
use tokio::io::AsyncReadExt;
use url::Url;

const SHA256_METADATA_KEY: &str = "sha256";
const HASH_BUFFER_SIZE: usize = 1024 * 1024;

type BoxError = Box<dyn Error + Send + Sync + 'static>;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct S3Config {
    bucket: String,
    region: String,
    endpoint: Option<Url>,
    force_path_style: bool,
    recording_root: PathBuf,
}

impl S3Config {
    pub fn new(
        bucket: impl Into<String>,
        region: impl Into<String>,
        recording_root: impl Into<PathBuf>,
    ) -> Result<Self, S3ConfigError> {
        let bucket = bucket.into();
        let region = region.into();
        let recording_root = recording_root.into();
        if bucket.trim().is_empty() {
            return Err(S3ConfigError::EmptyBucket);
        }
        if region.trim().is_empty() {
            return Err(S3ConfigError::EmptyRegion);
        }
        if recording_root.as_os_str().is_empty() {
            return Err(S3ConfigError::EmptyRecordingRoot);
        }
        Ok(Self {
            bucket,
            region,
            endpoint: None,
            force_path_style: false,
            recording_root,
        })
    }

    #[must_use]
    pub fn with_endpoint(mut self, endpoint: Url) -> Self {
        self.endpoint = Some(endpoint);
        self
    }

    #[must_use]
    pub const fn with_force_path_style(mut self, force_path_style: bool) -> Self {
        self.force_path_style = force_path_style;
        self
    }

    #[must_use]
    pub fn bucket(&self) -> &str {
        &self.bucket
    }

    #[must_use]
    pub fn region(&self) -> &str {
        &self.region
    }

    #[must_use]
    pub const fn endpoint(&self) -> Option<&Url> {
        self.endpoint.as_ref()
    }

    #[must_use]
    pub const fn force_path_style(&self) -> bool {
        self.force_path_style
    }

    #[must_use]
    pub fn recording_root(&self) -> &Path {
        &self.recording_root
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Error)]
pub enum S3ConfigError {
    #[error("S3 bucket must not be empty")]
    EmptyBucket,
    #[error("S3 region must not be empty")]
    EmptyRegion,
    #[error("recording root must not be empty")]
    EmptyRecordingRoot,
}

pub struct S3ObjectStorage {
    client: Arc<dyn S3Client>,
    bucket: String,
    recording_root: PathBuf,
}

impl S3ObjectStorage {
    /// Creates an S3 adapter using the standard AWS credential provider chain.
    pub async fn new(config: S3Config) -> Self {
        let shared_config = aws_config::defaults(BehaviorVersion::latest())
            .region(Region::new(config.region.clone()))
            .load()
            .await;
        let mut s3_config = aws_sdk_s3::config::Builder::from(&shared_config)
            .force_path_style(config.force_path_style);
        if let Some(endpoint) = &config.endpoint {
            s3_config = s3_config.endpoint_url(endpoint.as_str());
        }
        let client = Client::from_conf(s3_config.build());
        Self {
            client: Arc::new(AwsS3Client { client }),
            bucket: config.bucket,
            recording_root: config.recording_root,
        }
    }

    #[cfg(test)]
    fn with_client(config: S3Config, client: Arc<dyn S3Client>) -> Self {
        Self {
            client,
            bucket: config.bucket,
            recording_root: config.recording_root,
        }
    }

    async fn prepare_upload(
        &self,
        recording: &FinalizedRecording,
    ) -> Result<PreparedUpload, ObjectStorageError> {
        let path = resolve_recording_path(&self.recording_root, recording.relative_path().as_str())
            .await
            .map_err(unexpected)?;
        let (hash, size) = hash_file(&path).await.map_err(unexpected)?;
        let hash_hex = hash.to_hex();
        let key = content_addressed_key(recording.relative_path().as_str(), &hash_hex)
            .map_err(unexpected)?;
        Ok(PreparedUpload {
            bucket: self.bucket.clone(),
            key,
            path,
            hash,
            hash_hex,
            size,
            media_type: recording.media_type().as_str().to_owned(),
        })
    }

    async fn inspect_existing(
        &self,
        upload: &PreparedUpload,
    ) -> Result<Option<RemoteObject>, ObjectStorageError> {
        self.client
            .head_object(&upload.bucket, upload.key.as_str())
            .await
            .map_err(map_client_error)
    }
}

#[async_trait]
impl ObjectStorage for S3ObjectStorage {
    async fn upload(
        &self,
        recording: &FinalizedRecording,
    ) -> Result<StoredObject, ObjectStorageError> {
        let upload = self.prepare_upload(recording).await?;
        match self.client.put_object(&upload).await {
            Ok(()) => Ok(upload.stored_object()),
            Err(ClientError::PreconditionFailed) => match self.inspect_existing(&upload).await? {
                Some(existing) if existing.matches(&upload) => Ok(upload.stored_object()),
                Some(_) => Err(ObjectStorageError::Conflict),
                None => Err(ObjectStorageError::Unavailable(Box::new(S3Failure(
                    "conditional S3 upload lost a concurrent create/delete race".to_owned(),
                )))),
            },
            Err(error) => Err(map_client_error(error)),
        }
    }
}

#[derive(Debug, Clone)]
struct PreparedUpload {
    bucket: String,
    key: ObjectKey,
    path: PathBuf,
    hash: ContentHash,
    hash_hex: String,
    size: FileSize,
    media_type: String,
}

impl PreparedUpload {
    fn stored_object(&self) -> StoredObject {
        StoredObject::new(self.key.clone(), self.hash, self.size)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct RemoteObject {
    size: Option<u64>,
    sha256: Option<String>,
}

impl RemoteObject {
    fn matches(&self, upload: &PreparedUpload) -> bool {
        self.size == Some(upload.size.bytes()) && self.sha256.as_deref() == Some(&upload.hash_hex)
    }
}

#[async_trait]
trait S3Client: Send + Sync {
    async fn head_object(
        &self,
        bucket: &str,
        key: &str,
    ) -> Result<Option<RemoteObject>, ClientError>;

    async fn put_object(&self, upload: &PreparedUpload) -> Result<(), ClientError>;
}

struct AwsS3Client {
    client: Client,
}

#[async_trait]
impl S3Client for AwsS3Client {
    async fn head_object(
        &self,
        bucket: &str,
        key: &str,
    ) -> Result<Option<RemoteObject>, ClientError> {
        match self
            .client
            .head_object()
            .bucket(bucket)
            .key(key)
            .send()
            .await
        {
            Ok(output) => Ok(Some(RemoteObject {
                size: output
                    .content_length()
                    .and_then(|size| u64::try_from(size).ok()),
                sha256: output
                    .metadata()
                    .and_then(|metadata| metadata.get(SHA256_METADATA_KEY))
                    .cloned(),
            })),
            Err(error) => {
                let status = error
                    .raw_response()
                    .map(|response| response.status().as_u16());
                if status == Some(404) {
                    Ok(None)
                } else {
                    Err(classify_sdk_error("head S3 object", status, error))
                }
            }
        }
    }

    async fn put_object(&self, upload: &PreparedUpload) -> Result<(), ClientError> {
        let content_length = i64::try_from(upload.size.bytes()).map_err(|error| {
            ClientError::Unexpected(Box::new(S3Failure(format!(
                "recording is too large for an S3 content length: {error}"
            ))))
        })?;
        let body = ByteStream::from_path(&upload.path).await.map_err(|error| {
            ClientError::Unexpected(Box::new(S3Failure(format!(
                "open recording for S3 upload: {error}"
            ))))
        })?;
        let result = self
            .client
            .put_object()
            .bucket(&upload.bucket)
            .key(upload.key.as_str())
            .body(body)
            .content_length(content_length)
            .content_type(&upload.media_type)
            .checksum_sha256(BASE64_STANDARD.encode(upload.hash.as_bytes()))
            .metadata(SHA256_METADATA_KEY, &upload.hash_hex)
            .if_none_match("*")
            .send()
            .await;
        match result {
            Ok(_) => Ok(()),
            Err(error) => {
                let status = error
                    .raw_response()
                    .map(|response| response.status().as_u16());
                if status == Some(412) {
                    Err(ClientError::PreconditionFailed)
                } else {
                    Err(classify_sdk_error("put S3 object", status, error))
                }
            }
        }
    }
}

#[derive(Debug, Error)]
enum ClientError {
    #[error("S3 conditional write precondition failed")]
    PreconditionFailed,
    #[error("S3 is unavailable")]
    Unavailable(#[source] BoxError),
    #[error("unexpected S3 failure")]
    Unexpected(#[source] BoxError),
}

#[derive(Debug, Error)]
#[error("{0}")]
struct S3Failure(String);

fn classify_sdk_error(
    operation: &'static str,
    status: Option<u16>,
    error: impl std::fmt::Display,
) -> ClientError {
    let failure: BoxError = Box::new(S3Failure(format!("{operation}: {error}")));
    if status.is_none()
        || matches!(status, Some(408 | 409 | 429))
        || status.is_some_and(|status| status >= 500)
    {
        ClientError::Unavailable(failure)
    } else {
        ClientError::Unexpected(failure)
    }
}

fn map_client_error(error: ClientError) -> ObjectStorageError {
    match error {
        ClientError::PreconditionFailed => ObjectStorageError::Conflict,
        ClientError::Unavailable(error) => ObjectStorageError::Unavailable(error),
        ClientError::Unexpected(error) => ObjectStorageError::Unexpected(error),
    }
}

fn unexpected(error: impl Error + Send + Sync + 'static) -> ObjectStorageError {
    ObjectStorageError::Unexpected(Box::new(error))
}

async fn resolve_recording_path(root: &Path, relative: &str) -> Result<PathBuf, S3Failure> {
    let canonical_root = tokio::fs::canonicalize(root)
        .await
        .map_err(|error| S3Failure(format!("canonicalize recording root: {error}")))?;
    let candidate = root.join(relative);
    let metadata = tokio::fs::symlink_metadata(&candidate)
        .await
        .map_err(|error| S3Failure(format!("inspect finalized recording: {error}")))?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(S3Failure(
            "finalized recording must be a regular non-symlink file".to_owned(),
        ));
    }
    let canonical_path = tokio::fs::canonicalize(candidate)
        .await
        .map_err(|error| S3Failure(format!("canonicalize finalized recording: {error}")))?;
    if !canonical_path.starts_with(&canonical_root) {
        return Err(S3Failure(
            "finalized recording resolves outside the recording root".to_owned(),
        ));
    }
    Ok(canonical_path)
}

async fn hash_file(path: &Path) -> Result<(ContentHash, FileSize), S3Failure> {
    let mut file = tokio::fs::File::open(path)
        .await
        .map_err(|error| S3Failure(format!("open finalized recording: {error}")))?;
    let mut hasher = Sha256::new();
    let mut size = 0_u64;
    let mut buffer = vec![0_u8; HASH_BUFFER_SIZE];
    loop {
        let read = file
            .read(&mut buffer)
            .await
            .map_err(|error| S3Failure(format!("hash finalized recording: {error}")))?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
        size = size
            .checked_add(read as u64)
            .ok_or_else(|| S3Failure("recording size overflow".to_owned()))?;
    }
    let hash = ContentHash::from_bytes(hasher.finalize().into());
    Ok((hash, FileSize::from_bytes(size)))
}

fn content_addressed_key(relative: &str, hash: &str) -> Result<ObjectKey, S3Failure> {
    let (directory, file_name) = relative
        .rsplit_once('/')
        .map_or(("", relative), |(directory, file_name)| {
            (directory, file_name)
        });
    let key = if directory.is_empty() {
        format!("{hash}-{file_name}")
    } else {
        format!("{directory}/{hash}-{file_name}")
    };
    ObjectKey::new(key).map_err(|error| S3Failure(format!("build S3 object key: {error}")))
}

#[cfg(test)]
mod tests {
    use std::{
        collections::VecDeque,
        sync::{Arc, Mutex},
    };

    use kinugasa_core::domain::{CameraIdentityId, MediaType, RelativePath, SessionId, TakeId};
    use uuid::Uuid;

    use super::*;

    struct TestDirectory(PathBuf);

    impl TestDirectory {
        fn new() -> Self {
            let path = std::env::temp_dir().join(format!("kinugasa-s3-{}", Uuid::now_v7()));
            std::fs::create_dir_all(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for TestDirectory {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    #[derive(Default)]
    struct StubClient {
        heads: Mutex<VecDeque<Result<Option<RemoteObject>, ClientError>>>,
        puts: Mutex<Vec<PreparedUpload>>,
        put_error: Mutex<Option<ClientError>>,
    }

    #[async_trait]
    impl S3Client for StubClient {
        async fn head_object(
            &self,
            _bucket: &str,
            _key: &str,
        ) -> Result<Option<RemoteObject>, ClientError> {
            self.heads.lock().unwrap().pop_front().unwrap_or(Ok(None))
        }

        async fn put_object(&self, upload: &PreparedUpload) -> Result<(), ClientError> {
            self.puts.lock().unwrap().push(upload.clone());
            match self.put_error.lock().unwrap().take() {
                Some(error) => Err(error),
                None => Ok(()),
            }
        }
    }

    fn recording(relative_path: &str) -> FinalizedRecording {
        let started_at = std::time::SystemTime::UNIX_EPOCH.into();
        FinalizedRecording::new(
            TakeId::new_v7(),
            CameraIdentityId::new_v7(),
            SessionId::new_v7(),
            started_at,
            started_at,
            RelativePath::new(relative_path).unwrap(),
            MediaType::new("video/mp4").unwrap(),
        )
        .unwrap()
    }

    fn test_store(root: &Path, client: Arc<StubClient>) -> S3ObjectStorage {
        S3ObjectStorage::with_client(
            S3Config::new("recordings", "ap-northeast-1", root).unwrap(),
            client,
        )
    }

    #[test]
    fn configuration_rejects_empty_required_values() {
        assert_eq!(
            S3Config::new("", "ap-northeast-1", "/recordings"),
            Err(S3ConfigError::EmptyBucket)
        );
        assert_eq!(
            S3Config::new("recordings", "", "/recordings"),
            Err(S3ConfigError::EmptyRegion)
        );
        assert_eq!(
            S3Config::new("recordings", "ap-northeast-1", ""),
            Err(S3ConfigError::EmptyRecordingRoot)
        );
    }

    #[test]
    fn uploads_with_v2_compatible_content_addressed_key() {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(async {
                let directory = TestDirectory::new();
                let relative = "recording/session/take/camera/video.mp4";
                let path = directory.0.join(relative);
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(&path, b"video").unwrap();
                let client = Arc::new(StubClient::default());
                let store = test_store(&directory.0, Arc::clone(&client));

                let stored = store.upload(&recording(relative)).await.unwrap();
                let hash = stored.hash().to_hex();

                assert_eq!(stored.size().bytes(), 5);
                assert_eq!(
                    stored.object_key().as_str(),
                    format!("recording/session/take/camera/{hash}-video.mp4")
                );
                let puts = client.puts.lock().unwrap();
                assert_eq!(puts.len(), 1);
                assert_eq!(puts[0].hash_hex, hash);
                assert_eq!(puts[0].media_type, "video/mp4");
            });
    }

    #[test]
    fn matching_existing_object_is_idempotent_after_conditional_write() {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(async {
                let directory = TestDirectory::new();
                let relative = "recording/session/take/camera/video.mp4";
                let path = directory.0.join(relative);
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(&path, b"video").unwrap();
                let hash = ContentHash::from_bytes(Sha256::digest(b"video").into()).to_hex();
                let client = Arc::new(StubClient::default());
                client
                    .heads
                    .lock()
                    .unwrap()
                    .push_back(Ok(Some(RemoteObject {
                        size: Some(5),
                        sha256: Some(hash),
                    })));
                *client.put_error.lock().unwrap() = Some(ClientError::PreconditionFailed);
                let store = test_store(&directory.0, Arc::clone(&client));

                store.upload(&recording(relative)).await.unwrap();

                assert_eq!(client.puts.lock().unwrap().len(), 1);
            });
    }

    #[test]
    fn mismatching_existing_object_is_a_conflict() {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(async {
                let directory = TestDirectory::new();
                let relative = "recording/session/take/camera/video.mp4";
                let path = directory.0.join(relative);
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(&path, b"video").unwrap();
                let client = Arc::new(StubClient::default());
                client
                    .heads
                    .lock()
                    .unwrap()
                    .push_back(Ok(Some(RemoteObject {
                        size: Some(4),
                        sha256: Some("wrong".to_owned()),
                    })));
                *client.put_error.lock().unwrap() = Some(ClientError::PreconditionFailed);
                let store = test_store(&directory.0, client);

                let error = store.upload(&recording(relative)).await.unwrap_err();

                assert!(matches!(error, ObjectStorageError::Conflict));
            });
    }

    #[test]
    fn missing_object_after_conditional_write_race_is_deferred() {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(async {
                let directory = TestDirectory::new();
                let relative = "recording/session/take/camera/video.mp4";
                let path = directory.0.join(relative);
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(&path, b"video").unwrap();
                let client = Arc::new(StubClient::default());
                client.heads.lock().unwrap().push_back(Ok(None));
                *client.put_error.lock().unwrap() = Some(ClientError::PreconditionFailed);
                let store = test_store(&directory.0, client);

                let error = store.upload(&recording(relative)).await.unwrap_err();

                assert!(matches!(error, ObjectStorageError::Unavailable(_)));
            });
    }
}
