use chrono::{DateTime, Utc};

use crate::{
    application::UseCaseError,
    domain::{
        CameraIdentityId, ErrorReason, FinalizedRecording, FinishedTake, FinishedTakeState,
        OngoingTake, RecordingCamera, RecordingCameraState, TakeId, UploadOutcome, UploadResult,
        VideoFile, VideoFileState,
    },
    ports::{RecordingRepository, TakeRepository, TakeSnapshot, UnitOfWork},
};

const UPLOAD_FAILURE: &str = "one or more video uploads failed";

pub(super) async fn persist_recording_started<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    take_id: TakeId,
    camera_id: CameraIdentityId,
    started_at: DateTime<Utc>,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let _take = repository
        .get_take_for_update(unit_of_work, take_id)
        .await?;
    let recording = repository
        .get_recording_camera_for_update(unit_of_work, take_id, camera_id)
        .await?
        .ok_or_else(UseCaseError::conflict)?;
    if !matches!(recording.state(), RecordingCameraState::Recording) {
        return Err(UseCaseError::conflict());
    }
    repository
        .save_recording_camera(
            unit_of_work,
            &RecordingCamera::new(
                take_id,
                camera_id,
                RecordingCameraState::Recording,
                started_at,
            ),
        )
        .await?;
    Ok(())
}

pub(super) async fn persist_recording_failure<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    take_id: TakeId,
    camera_id: CameraIdentityId,
    reason: &ErrorReason,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let take = repository
        .get_take_for_update(unit_of_work, take_id)
        .await?;
    let recording = repository
        .get_recording_camera_for_update(unit_of_work, take_id, camera_id)
        .await?;
    match recording {
        Some(recording) => match recording.state() {
            RecordingCameraState::Recording => {
                repository
                    .save_recording_camera(
                        unit_of_work,
                        &RecordingCamera::new(
                            take_id,
                            camera_id,
                            RecordingCameraState::Errored(reason.clone()),
                            recording.started_at(),
                        ),
                    )
                    .await?;
            }
            RecordingCameraState::Errored(current) if current == reason => {}
            RecordingCameraState::Errored(_) => return Err(UseCaseError::conflict()),
        },
        None if matches!(take, TakeSnapshot::Ongoing) => {
            return Err(UseCaseError::conflict());
        }
        None => {}
    }

    if let TakeSnapshot::Finished(finished) = take {
        fail_video_file(unit_of_work, repository, finished, camera_id, reason, true).await?;
    }
    Ok(())
}

pub(super) async fn finish_ongoing_take<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    ongoing: &OngoingTake,
    finished_at: DateTime<Utc>,
) -> Result<FinishedTake, UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U>,
{
    let mut video_files = Vec::with_capacity(ongoing.cameras().len());
    let mut has_uploading = false;
    for recording in ongoing.cameras() {
        let state = match recording.state() {
            RecordingCameraState::Recording => {
                has_uploading = true;
                VideoFileState::Uploading
            }
            RecordingCameraState::Errored(reason) => VideoFileState::Errored(reason.clone()),
        };
        video_files.push(VideoFile::new(
            ongoing.id(),
            recording.camera_identity_id(),
            state,
            recording.started_at(),
            finished_at.max(recording.started_at()),
        )?);
    }
    let state = if has_uploading {
        FinishedTakeState::Uploading
    } else {
        FinishedTakeState::Errored(ErrorReason::new(UPLOAD_FAILURE)?)
    };
    let finished = FinishedTake::new(
        ongoing.id(),
        ongoing.session_id(),
        ongoing.name().clone(),
        state,
        ongoing.started_at(),
        finished_at,
    )?;
    repository
        .replace_ongoing_with_finished(unit_of_work, &finished, &video_files)
        .await?;
    if !has_uploading {
        repository
            .delete_recording_cameras(unit_of_work, ongoing.id())
            .await?;
    }
    Ok(finished)
}

pub(super) async fn stage_finalized_recording<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    recording: &FinalizedRecording,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let take = repository
        .get_take_for_update(unit_of_work, recording.take_id())
        .await?;
    let TakeSnapshot::Finished(finished) = take else {
        return Err(UseCaseError::conflict());
    };
    if finished.session_id() != recording.session_id() {
        return Err(UseCaseError::conflict());
    }

    let existing = repository
        .get_finalized_recording_for_update(
            unit_of_work,
            recording.take_id(),
            recording.camera_identity_id(),
        )
        .await?;
    if let Some(existing) = existing {
        if !same_finalized_recording(&existing, recording) {
            return Err(UseCaseError::conflict());
        }
    } else {
        let source = repository
            .get_recording_camera_for_update(
                unit_of_work,
                recording.take_id(),
                recording.camera_identity_id(),
            )
            .await?
            .ok_or_else(UseCaseError::conflict)?;
        if !matches!(source.state(), RecordingCameraState::Recording) {
            return Err(UseCaseError::conflict());
        }
        repository
            .insert_finalized_recording(unit_of_work, recording)
            .await?;
    }

    let video = repository
        .get_video_file_for_update(
            unit_of_work,
            recording.take_id(),
            recording.camera_identity_id(),
        )
        .await?
        .ok_or_else(UseCaseError::conflict)?;
    if matches!(video.state(), VideoFileState::Uploading) {
        repository
            .save_video_file(
                unit_of_work,
                &VideoFile::new(
                    video.finished_take_id(),
                    video.camera_identity_id(),
                    VideoFileState::Uploading,
                    recording.started_at(),
                    recording.finished_at(),
                )?,
            )
            .await?;
    }
    Ok(())
}

pub(super) async fn persist_upload_result<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    result: &UploadResult,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let take = repository
        .get_take_for_update(unit_of_work, result.take_id())
        .await?;
    let TakeSnapshot::Finished(finished) = take else {
        return Err(UseCaseError::conflict());
    };
    if finished.session_id() != result.session_id() {
        return Err(UseCaseError::conflict());
    }
    let finalized = repository
        .get_finalized_recording_for_update(
            unit_of_work,
            result.take_id(),
            result.camera_identity_id(),
        )
        .await?
        .ok_or_else(UseCaseError::conflict)?;
    if finalized.session_id() != result.session_id() {
        return Err(UseCaseError::conflict());
    }
    let video = repository
        .get_video_file_for_update(unit_of_work, result.take_id(), result.camera_identity_id())
        .await?
        .ok_or_else(UseCaseError::conflict)?;
    let wanted = match result.outcome() {
        UploadOutcome::Completed(stored) => VideoFileState::Completed(stored.clone()),
        UploadOutcome::Errored(reason) => VideoFileState::Errored(reason.clone()),
    };
    if !matches!(video.state(), VideoFileState::Uploading) {
        return if video.state() == &wanted {
            Ok(())
        } else {
            Err(UseCaseError::conflict())
        };
    }
    repository
        .save_video_file(
            unit_of_work,
            &VideoFile::new(
                video.finished_take_id(),
                video.camera_identity_id(),
                wanted,
                video.started_at(),
                video.finished_at(),
            )?,
        )
        .await?;
    converge_finished_take(unit_of_work, repository, finished).await?;
    Ok(())
}

pub(super) async fn fail_upload<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    take_id: TakeId,
    camera_id: CameraIdentityId,
    reason: &ErrorReason,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let take = repository
        .get_take_for_update(unit_of_work, take_id)
        .await?;
    let TakeSnapshot::Finished(finished) = take else {
        return Err(UseCaseError::conflict());
    };
    fail_video_file(unit_of_work, repository, finished, camera_id, reason, false).await
}

async fn fail_video_file<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    finished: FinishedTake,
    camera_id: CameraIdentityId,
    reason: &ErrorReason,
    require_matching_error: bool,
) -> Result<(), UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let video = repository
        .get_video_file_for_update(unit_of_work, finished.id(), camera_id)
        .await?
        .ok_or_else(UseCaseError::conflict)?;
    match video.state() {
        VideoFileState::Uploading => {
            repository
                .save_video_file(
                    unit_of_work,
                    &VideoFile::new(
                        video.finished_take_id(),
                        video.camera_identity_id(),
                        VideoFileState::Errored(reason.clone()),
                        video.started_at(),
                        video.finished_at(),
                    )?,
                )
                .await?;
            converge_finished_take(unit_of_work, repository, finished).await?;
        }
        VideoFileState::Errored(current) if !require_matching_error || current == reason => {}
        VideoFileState::Completed(_) if !require_matching_error => {}
        VideoFileState::Completed(_) | VideoFileState::Errored(_) => {
            return Err(UseCaseError::conflict());
        }
    }
    Ok(())
}

async fn converge_finished_take<U, R>(
    unit_of_work: &mut U,
    repository: &R,
    take: FinishedTake,
) -> Result<FinishedTake, UseCaseError>
where
    U: UnitOfWork,
    R: TakeRepository<U> + RecordingRepository<U>,
{
    let video_files = repository
        .list_video_files_for_take_for_update(unit_of_work, take.id())
        .await?;
    if video_files.is_empty()
        || video_files
            .iter()
            .any(|video| matches!(video.state(), VideoFileState::Uploading))
    {
        return Ok(take);
    }
    let state = if video_files
        .iter()
        .any(|video| matches!(video.state(), VideoFileState::Errored(_)))
    {
        FinishedTakeState::Errored(ErrorReason::new(UPLOAD_FAILURE)?)
    } else {
        FinishedTakeState::Completed
    };
    let converged = FinishedTake::new(
        take.id(),
        take.session_id(),
        take.name().clone(),
        state,
        take.started_at(),
        take.finished_at(),
    )?;
    repository
        .save_finished_take(unit_of_work, &converged)
        .await?;
    repository
        .delete_recording_cameras(unit_of_work, take.id())
        .await?;
    Ok(converged)
}

fn same_finalized_recording(left: &FinalizedRecording, right: &FinalizedRecording) -> bool {
    left.take_id() == right.take_id()
        && left.camera_identity_id() == right.camera_identity_id()
        && left.session_id() == right.session_id()
        && left.started_at().timestamp_micros() == right.started_at().timestamp_micros()
        && left.finished_at().timestamp_micros() == right.finished_at().timestamp_micros()
        && left.relative_path() == right.relative_path()
        && left.media_type() == right.media_type()
}
