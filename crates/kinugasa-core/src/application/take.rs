use std::{collections::BTreeMap, sync::Arc};

use tokio::task::JoinSet;

use crate::{
    application::{
        RecordingLayout, UseCaseError,
        recording_state::{
            finish_ongoing_take, persist_recording_failure, persist_recording_started,
            stage_finalized_recording,
        },
        task::collect_tasks,
    },
    domain::{
        CameraConnectionState, CameraIdentityId, CameraName, ErrorReason, FinishedTake,
        OngoingTake, RecordingCamera, RecordingCameraState, SessionName, SessionState, TakeName,
    },
    ports::{
        CameraRepository, Clock, IdGenerator, MediaError, Page, PageRequest, RecordingRepository,
        RecordingService, SessionRepository, StartRecordingRequest, TakeRepository, UnitOfWork,
        UnitOfWorkFactory,
    },
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OngoingTakeView {
    pub take: OngoingTake,
    pub camera_names: BTreeMap<CameraIdentityId, CameraName>,
}

pub struct TakeUseCases<F, R, M, C, I> {
    unit_of_work_factory: Arc<F>,
    repository: Arc<R>,
    media: Arc<M>,
    clock: Arc<C>,
    ids: Arc<I>,
    recording_layout: RecordingLayout,
}

impl<F, R, M, C, I> TakeUseCases<F, R, M, C, I> {
    #[must_use]
    pub fn new(
        unit_of_work_factory: Arc<F>,
        repository: Arc<R>,
        media: Arc<M>,
        clock: Arc<C>,
        ids: Arc<I>,
        recording_layout: RecordingLayout,
    ) -> Self {
        Self {
            unit_of_work_factory,
            repository,
            media,
            clock,
            ids,
            recording_layout,
        }
    }
}

impl<F, R, M, C, I> TakeUseCases<F, R, M, C, I>
where
    F: UnitOfWorkFactory + 'static,
    F::UnitOfWork: 'static,
    R: SessionRepository<F::UnitOfWork>
        + CameraRepository<F::UnitOfWork>
        + TakeRepository<F::UnitOfWork>
        + RecordingRepository<F::UnitOfWork>
        + 'static,
    M: RecordingService + 'static,
    C: Clock,
    I: IdGenerator,
{
    pub async fn start_take(
        &self,
        session_name: String,
        take_name: String,
        camera_names: Vec<String>,
    ) -> Result<OngoingTakeView, UseCaseError> {
        if camera_names.is_empty() {
            return Err(crate::domain::ValidationError::new(
                "camera_names",
                "must contain at least one camera",
            )
            .into());
        }
        let session_name = SessionName::new(session_name)?;
        let take_name = TakeName::new(take_name)?;
        let camera_names = camera_names
            .into_iter()
            .map(CameraName::new)
            .collect::<Result<Vec<_>, _>>()?;
        let mut sorted_names = camera_names.clone();
        sorted_names.sort();
        sorted_names.dedup();
        if sorted_names.len() != camera_names.len() {
            return Err(
                crate::domain::ValidationError::new("camera_names", "must be unique").into(),
            );
        }

        let take_id = self.ids.take_id();
        let requested_at = self.clock.now();
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let session = self
            .repository
            .get_session_for_update(&mut unit_of_work, &session_name)
            .await?
            .session;
        if session.state() != SessionState::Active {
            return Err(UseCaseError::conflict());
        }

        let mut cameras = Vec::with_capacity(camera_names.len());
        let mut names_by_id = BTreeMap::new();
        for camera_name in &camera_names {
            let camera = self
                .repository
                .get_camera_for_update(&mut unit_of_work, &session_name, camera_name)
                .await?;
            if !matches!(
                camera.connection().state(),
                CameraConnectionState::Connected { .. }
            ) {
                return Err(UseCaseError::conflict());
            }
            cameras.push(RecordingCamera::new(
                take_id,
                camera.identity().id(),
                RecordingCameraState::Recording,
                requested_at,
            ));
            names_by_id.insert(camera.identity().id(), camera_name.clone());
        }
        let take = OngoingTake::new(
            take_id,
            session.id(),
            take_name.clone(),
            requested_at,
            cameras,
        )?;
        self.repository
            .insert_ongoing_take(&mut unit_of_work, &take)
            .await?;
        unit_of_work.commit().await?;

        let mut tasks = JoinSet::new();
        for camera in take.cameras() {
            let camera_id = camera.camera_identity_id();
            let camera_name = names_by_id
                .get(&camera_id)
                .expect("camera name map was built with the take");
            let request = StartRecordingRequest {
                take_id,
                session_id: session.id(),
                camera_identity_id: camera_id,
                relative_path: self
                    .recording_layout
                    .path(&session_name, &take_name, camera_name),
            };
            let unit_of_work_factory = Arc::clone(&self.unit_of_work_factory);
            let repository = Arc::clone(&self.repository);
            let media = Arc::clone(&self.media);
            tasks.spawn(async move {
                match media.start_recording(&request).await {
                    Ok(started) => {
                        let mut unit_of_work = unit_of_work_factory.begin().await?;
                        persist_recording_started(
                            &mut unit_of_work,
                            repository.as_ref(),
                            take_id,
                            camera_id,
                            started.started_at,
                        )
                        .await?;
                        unit_of_work.commit().await?;
                    }
                    Err(error) => {
                        record_recording_failure(
                            unit_of_work_factory.as_ref(),
                            repository.as_ref(),
                            take_id,
                            camera_id,
                            &error,
                        )
                        .await?;
                    }
                }
                Ok(())
            });
        }
        collect_tasks(tasks).await?;

        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let take = self
            .repository
            .get_ongoing_take(&mut unit_of_work, &session_name)
            .await?
            .ok_or_else(UseCaseError::conflict)?;
        unit_of_work.commit().await?;
        Ok(OngoingTakeView {
            take,
            camera_names: names_by_id,
        })
    }

    pub async fn get_ongoing_take(
        &self,
        session_name: String,
    ) -> Result<Option<OngoingTakeView>, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let take = self
            .repository
            .get_ongoing_take(&mut unit_of_work, &session_name)
            .await?;
        let Some(take) = take else {
            unit_of_work.commit().await?;
            return Ok(None);
        };
        let cameras = self
            .repository
            .list_cameras(&mut unit_of_work, &session_name)
            .await?;
        unit_of_work.commit().await?;
        let camera_names = cameras
            .into_iter()
            .map(|camera| (camera.identity().id(), camera.identity().name().clone()))
            .collect();
        Ok(Some(OngoingTakeView { take, camera_names }))
    }

    pub async fn finish_take(&self, session_name: String) -> Result<FinishedTake, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let ongoing = self
            .repository
            .get_ongoing_take_for_update(&mut unit_of_work, &session_name)
            .await?
            .ok_or_else(UseCaseError::conflict)?;
        let finished = finish_ongoing_take(
            &mut unit_of_work,
            self.repository.as_ref(),
            &ongoing,
            self.clock.now(),
        )
        .await?;
        unit_of_work.commit().await?;

        let mut tasks = JoinSet::new();
        for camera in ongoing.cameras() {
            if matches!(camera.state(), RecordingCameraState::Errored(_)) {
                continue;
            }
            let take_id = ongoing.id();
            let camera_id = camera.camera_identity_id();
            let unit_of_work_factory = Arc::clone(&self.unit_of_work_factory);
            let repository = Arc::clone(&self.repository);
            let media = Arc::clone(&self.media);
            tasks.spawn(async move {
                match media.finish_recording(take_id, camera_id).await {
                    Ok(recording) => {
                        let mut unit_of_work = unit_of_work_factory.begin().await?;
                        stage_finalized_recording(
                            &mut unit_of_work,
                            repository.as_ref(),
                            &recording,
                        )
                        .await?;
                        unit_of_work.commit().await?;
                    }
                    Err(error) => {
                        record_recording_failure(
                            unit_of_work_factory.as_ref(),
                            repository.as_ref(),
                            take_id,
                            camera_id,
                            &error,
                        )
                        .await?;
                    }
                }
                Ok(())
            });
        }
        collect_tasks(tasks).await?;

        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let refreshed = self
            .repository
            .get_finished_take(&mut unit_of_work, &session_name, finished.name())
            .await?;
        unit_of_work.commit().await?;
        Ok(refreshed.detail().take().clone())
    }

    pub async fn list_finished_takes(
        &self,
        session_name: String,
        page: u32,
        page_size: u32,
    ) -> Result<Page<FinishedTake>, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let page = PageRequest::new(page, page_size)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .list_finished_takes(&mut unit_of_work, &session_name, page)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }

    pub async fn get_finished_take(
        &self,
        session_name: String,
        take_name: String,
    ) -> Result<crate::ports::NamedFinishedTakeDetail, UseCaseError> {
        let session_name = SessionName::new(session_name)?;
        let take_name = TakeName::new(take_name)?;
        let mut unit_of_work = self.unit_of_work_factory.begin().await?;
        let result = self
            .repository
            .get_finished_take(&mut unit_of_work, &session_name, &take_name)
            .await?;
        unit_of_work.commit().await?;
        Ok(result)
    }
}

async fn record_recording_failure<F, R>(
    unit_of_work_factory: &F,
    repository: &R,
    take_id: crate::domain::TakeId,
    camera_id: CameraIdentityId,
    error: &MediaError,
) -> Result<(), UseCaseError>
where
    F: UnitOfWorkFactory,
    R: TakeRepository<F::UnitOfWork> + RecordingRepository<F::UnitOfWork>,
{
    let reason = ErrorReason::new(error.to_string())?;
    let mut unit_of_work = unit_of_work_factory.begin().await?;
    persist_recording_failure(&mut unit_of_work, repository, take_id, camera_id, &reason).await?;
    unit_of_work.commit().await?;
    Ok(())
}
