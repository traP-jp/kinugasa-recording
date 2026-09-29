use std::{num::NonZeroU32, sync::Arc};

use async_trait::async_trait;
use chrono::{DateTime, Duration, Utc};
use kinugasa_core::{
    application::{CameraUseCases, RecordingLayout, TakeUseCases, UploadCoordinator, UseCaseError},
    domain::{
        Camera, CameraConnection, CameraConnectionState, CameraIdentity, CameraIdentityId,
        CameraName, ContentHash, ErrorReason, FileSize, FinalizedRecording, FinishedTake,
        FinishedTakeState, MediaType, ObjectKey, OngoingTake, RecordingCamera,
        RecordingCameraState, RelativePath, Session, SessionId, SessionName, SessionState,
        StoredObject, TakeId, TakeName, VideoFile, VideoFileState,
    },
    ports::{
        CameraRepository, Clock, IdGenerator, LockfileRepository, MediaError, ObjectStorage,
        ObjectStorageError, PageRequest, RecordingRepository, RecordingService, RecordingStarted,
        RepositoryError, SessionRepository, StartRecordingRequest, TakeRepository, UnitOfWork,
        UnitOfWorkFactory,
    },
};
use kinugasa_mysql::MySqlRepository;
use url::Url;

struct FixedClock(DateTime<Utc>);

impl Clock for FixedClock {
    fn now(&self) -> DateTime<Utc> {
        self.0
    }
}

struct FixedIds {
    session_id: SessionId,
    camera_id: CameraIdentityId,
    take_id: TakeId,
}

impl IdGenerator for FixedIds {
    fn session_id(&self) -> SessionId {
        self.session_id
    }

    fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_id
    }

    fn take_id(&self) -> TakeId {
        self.take_id
    }
}

struct TestMedia {
    session_id: SessionId,
    started_at: DateTime<Utc>,
    finished_at: DateTime<Utc>,
}

#[async_trait]
impl RecordingService for TestMedia {
    async fn start_recording(
        &self,
        request: &StartRecordingRequest,
    ) -> Result<RecordingStarted, MediaError> {
        Ok(RecordingStarted {
            take_id: request.take_id,
            camera_identity_id: request.camera_identity_id,
            started_at: self.started_at,
        })
    }

    async fn finish_recording(
        &self,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<FinalizedRecording, MediaError> {
        Ok(FinalizedRecording::new(
            take_id,
            camera_id,
            self.session_id,
            self.started_at,
            self.finished_at,
            RelativePath::new("recording/session/take/camera/video.ts").unwrap(),
            MediaType::new("video/mp2t").unwrap(),
        )
        .unwrap())
    }

    async fn abort_recording(
        &self,
        _take_id: TakeId,
        _camera_id: CameraIdentityId,
    ) -> Result<(), MediaError> {
        Ok(())
    }
}

struct TestStorage(StoredObject);

#[async_trait]
impl ObjectStorage for TestStorage {
    async fn upload(
        &self,
        _recording: &FinalizedRecording,
    ) -> Result<StoredObject, ObjectStorageError> {
        Ok(self.0.clone())
    }
}

struct Seed {
    now: DateTime<Utc>,
    session_id: SessionId,
    camera_id: CameraIdentityId,
    take_id: TakeId,
    session_name: SessionName,
    camera_name: CameraName,
    take_name: TakeName,
    camera: Camera,
}

async fn seed(repository: &MySqlRepository) -> Seed {
    let now = DateTime::from_timestamp_micros(Utc::now().timestamp_micros()).unwrap();
    let session_id = SessionId::new_v7();
    let camera_id = CameraIdentityId::new_v7();
    let take_id = TakeId::new_v7();
    let session_id_text = session_id.to_string();
    let session_name =
        SessionName::new(format!("s-{}", session_id_text.rsplit('-').next().unwrap())).unwrap();
    let camera_name = CameraName::new("camera-a").unwrap();
    let take_name = TakeName::new("take-a").unwrap();
    let mut connection = CameraConnection::new(
        camera_id,
        CameraConnectionState::Connected {
            endpoint: Url::parse("rist://127.0.0.1:9000?virt-dst-port=12345&buffer=5000").unwrap(),
        },
    );
    connection.set_virtual_port(Some(12_345));
    let camera = Camera::new(
        CameraIdentity::new(camera_id, session_id, camera_name.clone(), now),
        connection,
    )
    .unwrap();
    let take = OngoingTake::new(
        take_id,
        session_id,
        take_name.clone(),
        now,
        vec![RecordingCamera::new(
            take_id,
            camera_id,
            RecordingCameraState::Recording,
            now,
        )],
    )
    .unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    repository
        .create_session(
            &mut unit_of_work,
            &Session::new(session_id, session_name.clone(), SessionState::Active, now),
        )
        .await
        .unwrap();
    repository
        .create_camera(&mut unit_of_work, &camera)
        .await
        .unwrap();
    repository
        .insert_ongoing_take(&mut unit_of_work, &take)
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    Seed {
        now,
        session_id,
        camera_id,
        take_id,
        session_name,
        camera_name,
        take_name,
        camera,
    }
}

#[tokio::test]
#[ignore = "requires KINUGASA_MYSQL_TEST_URL to point to a disposable MySQL database"]
async fn repository_lifecycle() {
    let database_url = std::env::var("KINUGASA_MYSQL_TEST_URL").unwrap();
    let repository = MySqlRepository::connect(&database_url, 4).await.unwrap();
    repository.migrate().await.unwrap();
    let seed = seed(&repository).await;

    let started_at = seed.now + Duration::seconds(1);
    let finished_at = seed.now + Duration::seconds(10);
    let mut unit_of_work = repository.begin().await.unwrap();
    assert!(
        repository
            .list_sessions(&mut unit_of_work, PageRequest::new(1, 100).unwrap())
            .await
            .unwrap()
            .items
            .iter()
            .any(|session| session.id() == seed.session_id)
    );
    assert_eq!(
        repository
            .get_camera(&mut unit_of_work, &seed.session_name, &seed.camera_name)
            .await
            .unwrap(),
        seed.camera
    );
    assert_eq!(
        repository
            .get_ongoing_take_for_update(&mut unit_of_work, &seed.session_name)
            .await
            .unwrap()
            .unwrap()
            .id(),
        seed.take_id
    );
    repository
        .save_recording_camera(
            &mut unit_of_work,
            &RecordingCamera::new(
                seed.take_id,
                seed.camera_id,
                RecordingCameraState::Recording,
                started_at,
            ),
        )
        .await
        .unwrap();
    let uploading_take = FinishedTake::new(
        seed.take_id,
        seed.session_id,
        seed.take_name.clone(),
        FinishedTakeState::Uploading,
        seed.now,
        finished_at,
    )
    .unwrap();
    let uploading_video = VideoFile::new(
        seed.take_id,
        seed.camera_id,
        VideoFileState::Uploading,
        started_at,
        finished_at,
    )
    .unwrap();
    repository
        .replace_ongoing_with_finished(
            &mut unit_of_work,
            &uploading_take,
            std::slice::from_ref(&uploading_video),
        )
        .await
        .unwrap();
    let finalized = FinalizedRecording::new(
        seed.take_id,
        seed.camera_id,
        seed.session_id,
        started_at,
        finished_at,
        RelativePath::new("session/take/camera.ts").unwrap(),
        MediaType::new("video/mp2t").unwrap(),
    )
    .unwrap();
    repository
        .insert_finalized_recording(&mut unit_of_work, &finalized)
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    assert_eq!(
        repository
            .list_pending_uploads(&mut unit_of_work, NonZeroU32::new(10).unwrap())
            .await
            .unwrap()
            .len(),
        1
    );
    let stored = StoredObject::new(
        ObjectKey::new("recordings/camera.ts").unwrap(),
        ContentHash::from_bytes([42; 32]),
        FileSize::from_bytes(1234),
    );
    let completed_video = VideoFile::new(
        seed.take_id,
        seed.camera_id,
        VideoFileState::Completed(stored.clone()),
        started_at,
        finished_at,
    )
    .unwrap();
    repository
        .save_video_file(&mut unit_of_work, &completed_video)
        .await
        .unwrap();
    repository
        .save_finished_take(
            &mut unit_of_work,
            &FinishedTake::new(
                seed.take_id,
                seed.session_id,
                seed.take_name.clone(),
                FinishedTakeState::Completed,
                seed.now,
                finished_at,
            )
            .unwrap(),
        )
        .await
        .unwrap();
    repository
        .delete_recording_cameras(&mut unit_of_work, seed.take_id)
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    let detail = repository
        .get_finished_take(&mut unit_of_work, &seed.session_name, &seed.take_name)
        .await
        .unwrap();
    assert!(matches!(
        detail.detail().take().state(),
        FinishedTakeState::Completed
    ));
    assert_eq!(
        repository
            .list_lockfile_objects(&mut unit_of_work, &seed.session_name)
            .await
            .unwrap()[0]
            .stored,
        stored
    );
    assert_eq!(
        repository
            .get_finalized_recording_for_update(&mut unit_of_work, seed.take_id, seed.camera_id,)
            .await
            .unwrap(),
        Some(finalized)
    );
    let camera = repository
        .get_camera_for_update(&mut unit_of_work, &seed.session_name, &seed.camera_name)
        .await
        .unwrap();
    let (_, mut connection) = camera.into_parts();
    connection.request_deletion(finished_at + Duration::seconds(1));
    repository
        .save_camera_connection(&mut unit_of_work, &connection)
        .await
        .unwrap();
    repository
        .delete_camera_connection(&mut unit_of_work, seed.camera_id)
        .await
        .unwrap();
    assert!(matches!(
        repository
            .get_camera(&mut unit_of_work, &seed.session_name, &seed.camera_name)
            .await,
        Err(RepositoryError::NotFound)
    ));
    unit_of_work.commit().await.unwrap();
}

#[tokio::test]
#[ignore = "requires KINUGASA_MYSQL_TEST_URL to point to a disposable MySQL database"]
async fn persists_application_decided_recording_failure() {
    let database_url = std::env::var("KINUGASA_MYSQL_TEST_URL").unwrap();
    let repository = MySqlRepository::connect(&database_url, 4).await.unwrap();
    repository.migrate().await.unwrap();
    let seed = seed(&repository).await;
    let interrupted = ErrorReason::new("recording interrupted by process restart").unwrap();
    let finished_at = seed.now + Duration::seconds(1);
    let finished = FinishedTake::new(
        seed.take_id,
        seed.session_id,
        seed.take_name.clone(),
        FinishedTakeState::Errored(ErrorReason::new("one or more video uploads failed").unwrap()),
        seed.now,
        finished_at,
    )
    .unwrap();
    let video = VideoFile::new(
        seed.take_id,
        seed.camera_id,
        VideoFileState::Errored(interrupted.clone()),
        seed.now,
        finished_at,
    )
    .unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    repository
        .save_recording_camera(
            &mut unit_of_work,
            &RecordingCamera::new(
                seed.take_id,
                seed.camera_id,
                RecordingCameraState::Errored(interrupted.clone()),
                seed.now,
            ),
        )
        .await
        .unwrap();
    repository
        .replace_ongoing_with_finished(&mut unit_of_work, &finished, std::slice::from_ref(&video))
        .await
        .unwrap();
    repository
        .delete_recording_cameras(&mut unit_of_work, seed.take_id)
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    assert_eq!(
        repository
            .get_video_file_for_update(&mut unit_of_work, seed.take_id, seed.camera_id)
            .await
            .unwrap(),
        Some(video)
    );
    let detail = repository
        .get_finished_take(&mut unit_of_work, &seed.session_name, &seed.take_name)
        .await
        .unwrap();
    assert!(matches!(
        detail.detail().video_files()[0].state(),
        VideoFileState::Errored(reason) if reason == &interrupted
    ));
    unit_of_work.commit().await.unwrap();
}

#[tokio::test]
#[ignore = "requires KINUGASA_MYSQL_TEST_URL to point to a disposable MySQL database"]
async fn take_and_upload_use_cases_own_the_state_transitions() {
    let database_url = std::env::var("KINUGASA_MYSQL_TEST_URL").unwrap();
    let repository = Arc::new(MySqlRepository::connect(&database_url, 8).await.unwrap());
    repository.migrate().await.unwrap();

    let now = DateTime::from_timestamp_micros(Utc::now().timestamp_micros()).unwrap();
    let session_id = SessionId::new_v7();
    let camera_id = CameraIdentityId::new_v7();
    let take_id = TakeId::new_v7();
    let session_id_text = session_id.to_string();
    let session_name =
        SessionName::new(format!("s-{}", session_id_text.rsplit('-').next().unwrap())).unwrap();
    let camera_name = CameraName::new("camera-a").unwrap();
    let mut unit_of_work = repository.begin().await.unwrap();
    repository
        .create_session(
            &mut unit_of_work,
            &Session::new(session_id, session_name.clone(), SessionState::Active, now),
        )
        .await
        .unwrap();
    repository
        .create_camera(
            &mut unit_of_work,
            &Camera::new(
                CameraIdentity::new(camera_id, session_id, camera_name.clone(), now),
                CameraConnection::new(
                    camera_id,
                    CameraConnectionState::Connected {
                        endpoint: Url::parse("rist://127.0.0.1:9000").unwrap(),
                    },
                ),
            )
            .unwrap(),
        )
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    let clock = Arc::new(FixedClock(now));
    let take_use_cases = TakeUseCases::new(
        Arc::clone(&repository),
        Arc::clone(&repository),
        Arc::new(TestMedia {
            session_id,
            started_at: now,
            finished_at: now + Duration::seconds(10),
        }),
        Arc::clone(&clock),
        Arc::new(FixedIds {
            session_id,
            camera_id,
            take_id,
        }),
        RecordingLayout::new("video.ts").unwrap(),
    );
    take_use_cases
        .start_take(
            session_name.as_str().to_owned(),
            "take-a".to_owned(),
            vec![camera_name.as_str().to_owned()],
        )
        .await
        .unwrap();
    let finished = take_use_cases
        .finish_take(session_name.as_str().to_owned())
        .await
        .unwrap();
    assert!(matches!(finished.state(), FinishedTakeState::Uploading));

    let stored = StoredObject::new(
        ObjectKey::new("recordings/use-case.ts").unwrap(),
        ContentHash::from_bytes([7; 32]),
        FileSize::from_bytes(777),
    );
    let uploader = UploadCoordinator::new(
        Arc::clone(&repository),
        Arc::clone(&repository),
        Arc::new(TestStorage(stored.clone())),
        clock,
    );
    let report = uploader
        .reconcile(NonZeroU32::new(10).unwrap())
        .await
        .unwrap();
    assert_eq!(report.completed, 1);

    let mut unit_of_work = repository.begin().await.unwrap();
    let detail = repository
        .get_finished_take(
            &mut unit_of_work,
            &session_name,
            &TakeName::new("take-a").unwrap(),
        )
        .await
        .unwrap();
    assert!(matches!(
        detail.detail().take().state(),
        FinishedTakeState::Completed
    ));
    assert_eq!(
        detail.detail().video_files()[0].state(),
        &VideoFileState::Completed(stored)
    );
    unit_of_work.commit().await.unwrap();
}

#[tokio::test]
#[ignore = "requires KINUGASA_MYSQL_TEST_URL to point to a disposable MySQL database"]
async fn camera_use_case_owns_force_deletion_policy() {
    let database_url = std::env::var("KINUGASA_MYSQL_TEST_URL").unwrap();
    let repository = Arc::new(MySqlRepository::connect(&database_url, 4).await.unwrap());
    repository.migrate().await.unwrap();
    let seed = seed(&repository).await;
    let finished_at = seed.now + Duration::seconds(1);
    let uploading_take = FinishedTake::new(
        seed.take_id,
        seed.session_id,
        seed.take_name.clone(),
        FinishedTakeState::Uploading,
        seed.now,
        finished_at,
    )
    .unwrap();
    let uploading_video = VideoFile::new(
        seed.take_id,
        seed.camera_id,
        VideoFileState::Uploading,
        seed.now,
        finished_at,
    )
    .unwrap();
    let mut unit_of_work = repository.begin().await.unwrap();
    repository
        .replace_ongoing_with_finished(
            &mut unit_of_work,
            &uploading_take,
            std::slice::from_ref(&uploading_video),
        )
        .await
        .unwrap();
    unit_of_work.commit().await.unwrap();

    let use_cases = CameraUseCases::new(
        Arc::clone(&repository),
        Arc::clone(&repository),
        Arc::new(FixedClock(finished_at)),
        Arc::new(FixedIds {
            session_id: seed.session_id,
            camera_id: seed.camera_id,
            take_id: seed.take_id,
        }),
    );
    let error = use_cases
        .delete_camera(
            seed.session_name.as_str().to_owned(),
            seed.camera_name.as_str().to_owned(),
            false,
        )
        .await
        .unwrap_err();
    assert!(matches!(
        error,
        UseCaseError::Repository(RepositoryError::Conflict)
    ));
    use_cases
        .delete_camera(
            seed.session_name.as_str().to_owned(),
            seed.camera_name.as_str().to_owned(),
            true,
        )
        .await
        .unwrap();

    let mut unit_of_work = repository.begin().await.unwrap();
    let detail = repository
        .get_finished_take(&mut unit_of_work, &seed.session_name, &seed.take_name)
        .await
        .unwrap();
    assert!(matches!(
        detail.detail().take().state(),
        FinishedTakeState::Errored(_)
    ));
    assert!(matches!(
        detail.detail().video_files()[0].state(),
        VideoFileState::Errored(reason)
            if reason.as_str() == "upload aborted by forced camera deletion"
    ));
    assert!(
        repository
            .list_camera_resources(&mut unit_of_work)
            .await
            .unwrap()
            .iter()
            .any(|resource| {
                resource.camera.identity().id() == seed.camera_id
                    && resource
                        .camera
                        .connection()
                        .deletion_requested_at()
                        .is_some()
            })
    );
    unit_of_work.commit().await.unwrap();
}
