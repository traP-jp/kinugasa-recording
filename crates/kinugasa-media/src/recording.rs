use std::{
    collections::BTreeSet,
    ffi::OsString,
    path::{Path, PathBuf},
};

use chrono::{DateTime, Utc};
use kinugasa_core::{
    domain::{FinalizedRecording, MediaType},
    ports::StartRecordingRequest,
};
use tokio::{
    fs::{self, File, OpenOptions},
    io::AsyncWriteExt,
};
use transmux::{Sample, Segmenter, TrackSpec};

const MP4_MEDIA_TYPE: &str = "video/mp4";
const MOVIE_TIMESCALE: u32 = 90_000;
const TARGET_SEGMENT_DURATION_SECONDS: f64 = 2.0;

pub(crate) struct RecordingFile {
    request: StartRecordingRequest,
    started_at: DateTime<Utc>,
    file: File,
    segmenter: Segmenter,
    track_ids: BTreeSet<u32>,
    partial_path: PathBuf,
    final_path: PathBuf,
}

impl RecordingFile {
    pub(crate) fn take_id(&self) -> kinugasa_core::domain::TakeId {
        self.request.take_id
    }

    pub(crate) async fn create(
        root: &Path,
        request: StartRecordingRequest,
        started_at: DateTime<Utc>,
        tracks: Vec<TrackSpec>,
    ) -> Result<Self, std::io::Error> {
        let track_ids = tracks.iter().map(|track| track.track_id).collect();
        let segmenter = Segmenter::new(tracks, MOVIE_TIMESCALE, TARGET_SEGMENT_DURATION_SECONDS)
            .map_err(transmux_error)?;
        let init_segment = segmenter.init_segment().map_err(transmux_error)?;
        let final_path = root.join(request.relative_path.as_str());
        let parent = final_path.parent().ok_or_else(|| {
            std::io::Error::new(std::io::ErrorKind::InvalidInput, "recording has no parent")
        })?;
        prepare_parent(root, parent).await?;
        let canonical_parent = fs::canonicalize(parent).await?;
        if !canonical_parent.starts_with(root) {
            return Err(std::io::Error::new(
                std::io::ErrorKind::PermissionDenied,
                "recording path escapes recording root through a symlink",
            ));
        }
        if fs::try_exists(&final_path).await? {
            return Err(std::io::Error::new(
                std::io::ErrorKind::AlreadyExists,
                "final recording already exists",
            ));
        }
        let mut partial_name = final_path
            .file_name()
            .map(OsString::from)
            .ok_or_else(|| std::io::Error::new(std::io::ErrorKind::InvalidInput, "invalid path"))?;
        partial_name.push(".partial");
        let partial_path = final_path.with_file_name(partial_name);
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&partial_path)
            .await?;
        file.write_all(&init_segment).await?;
        Ok(Self {
            request,
            started_at,
            file,
            segmenter,
            track_ids,
            partial_path,
            final_path,
        })
    }

    pub(crate) async fn write_sample(
        &mut self,
        track_id: u32,
        sample: Sample,
    ) -> Result<(), std::io::Error> {
        if !self.track_ids.contains(&track_id) {
            return Ok(());
        }
        self.segmenter
            .push(track_id, sample)
            .map_err(transmux_error)?;
        self.write_ready_segments().await
    }

    pub(crate) async fn finish(mut self) -> Result<FinalizedRecording, std::io::Error> {
        self.segmenter.flush().map_err(transmux_error)?;
        self.write_ready_segments().await?;
        self.file.flush().await?;
        self.file.sync_all().await?;
        drop(self.file);
        // hard_link has create-new semantics for the destination, unlike
        // rename(2), which would silently replace an existing final recording.
        // The link becomes visible atomically and both paths are on the same
        // recording filesystem.
        fs::hard_link(&self.partial_path, &self.final_path).await?;
        fs::remove_file(&self.partial_path).await?;
        sync_parent(
            self.final_path
                .parent()
                .expect("validated recording path has a parent")
                .to_owned(),
        )
        .await?;
        FinalizedRecording::new(
            self.request.take_id,
            self.request.camera_identity_id,
            self.request.session_id,
            self.started_at,
            Utc::now(),
            self.request.relative_path,
            MediaType::new(MP4_MEDIA_TYPE).expect("MP4 media type is non-empty"),
        )
        .map_err(|error| std::io::Error::other(error.to_string()))
    }

    pub(crate) async fn abort(self) -> Result<(), std::io::Error> {
        drop(self.file);
        match fs::remove_file(self.partial_path).await {
            Ok(()) => Ok(()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(error),
        }
    }

    async fn write_ready_segments(&mut self) -> Result<(), std::io::Error> {
        for segment in self.segmenter.take_ready() {
            self.file.write_all(&segment).await?;
        }
        Ok(())
    }
}

fn transmux_error(error: transmux::Error) -> std::io::Error {
    std::io::Error::new(std::io::ErrorKind::InvalidData, error)
}

async fn prepare_parent(root: &Path, parent: &Path) -> Result<(), std::io::Error> {
    let relative = parent.strip_prefix(root).map_err(|_| {
        std::io::Error::new(
            std::io::ErrorKind::PermissionDenied,
            "recording parent is outside recording root",
        )
    })?;
    let mut current = root.to_owned();
    for component in relative.components() {
        let std::path::Component::Normal(component) = component else {
            return Err(std::io::Error::new(
                std::io::ErrorKind::PermissionDenied,
                "recording parent is not normalized",
            ));
        };
        current.push(component);
        match fs::symlink_metadata(&current).await {
            Ok(metadata) if metadata.file_type().is_symlink() => {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::PermissionDenied,
                    "recording path contains a symlink",
                ));
            }
            Ok(metadata) if metadata.is_dir() => {}
            Ok(_) => {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::NotADirectory,
                    "recording path component is not a directory",
                ));
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                match fs::create_dir(&current).await {
                    Ok(()) => {}
                    Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
                        let metadata = fs::symlink_metadata(&current).await?;
                        if metadata.file_type().is_symlink() || !metadata.is_dir() {
                            return Err(std::io::Error::new(
                                std::io::ErrorKind::PermissionDenied,
                                "concurrently created recording path component is not a directory",
                            ));
                        }
                    }
                    Err(error) => return Err(error),
                }
            }
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

async fn sync_parent(parent: PathBuf) -> Result<(), std::io::Error> {
    tokio::task::spawn_blocking(move || std::fs::File::open(parent)?.sync_all())
        .await
        .map_err(|error| std::io::Error::other(format!("directory sync task failed: {error}")))?
}

#[cfg(test)]
mod tests {
    use kinugasa_core::{
        domain::{CameraIdentityId, RelativePath, SessionId, TakeId},
        ports::StartRecordingRequest,
    };

    use super::*;
    use transmux::{
        AVCConfigurationBox, AVCDecoderConfigurationRecord, AvcPps, AvcSps, CodecConfig,
    };

    fn request(path: &str) -> StartRecordingRequest {
        StartRecordingRequest {
            take_id: TakeId::new_v7(),
            session_id: SessionId::new_v7(),
            camera_identity_id: CameraIdentityId::new_v7(),
            relative_path: RelativePath::new(path).unwrap(),
        }
    }

    fn video_tracks() -> Vec<TrackSpec> {
        let record = AVCDecoderConfigurationRecord {
            configuration_version: 1,
            profile_indication: 66,
            profile_compatibility: 0xc0,
            level_indication: 10,
            length_size_minus_one: 3,
            sps: vec![AvcSps(vec![
                0x67, 0x42, 0xc0, 0x0a, 0xdd, 0xec, 0x04, 0x40, 0x00, 0x00, 0x03, 0x00, 0x40, 0x00,
                0x00, 0x0f, 0x03, 0xc4, 0x89, 0xe0,
            ])],
            pps: vec![AvcPps(vec![0x68, 0xce, 0x0f, 0x2c, 0x80])],
            chroma_format: None,
            bit_depth_luma_minus8: None,
            bit_depth_chroma_minus8: None,
            sps_ext: vec![],
        };
        vec![TrackSpec::new(
            1,
            90_000,
            CodecConfig::Avc {
                config: AVCConfigurationBox::new(record),
                width: 16,
                height: 16,
            },
        )]
    }

    #[tokio::test]
    async fn finalization_never_replaces_an_existing_recording() {
        let root = tempfile::tempdir().unwrap();
        let root = tokio::fs::canonicalize(root.path()).await.unwrap();
        let first = RecordingFile::create(
            &root,
            request("session/take/camera/video.mp4"),
            Utc::now(),
            video_tracks(),
        )
        .await
        .unwrap();
        first.finish().await.unwrap();
        let final_path = root.join("session/take/camera/video.mp4");
        let first_bytes = tokio::fs::read(&final_path).await.unwrap();

        let error = RecordingFile::create(
            &root,
            request("session/take/camera/video.mp4"),
            Utc::now(),
            video_tracks(),
        )
        .await
        .err()
        .expect("existing final file must be rejected");
        assert_eq!(error.kind(), std::io::ErrorKind::AlreadyExists);
        assert_eq!(tokio::fs::read(final_path).await.unwrap(), first_bytes);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn parent_symlink_cannot_escape_recording_root() {
        use std::os::unix::fs::symlink;

        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        symlink(outside.path(), root.path().join("escape")).unwrap();
        let root = tokio::fs::canonicalize(root.path()).await.unwrap();
        let error = RecordingFile::create(
            &root,
            request("escape/take/camera/video.mp4"),
            Utc::now(),
            video_tracks(),
        )
        .await
        .err()
        .expect("symlink must be rejected");
        assert_eq!(error.kind(), std::io::ErrorKind::PermissionDenied);
        assert!(!outside.path().join("take").exists());
    }
}
