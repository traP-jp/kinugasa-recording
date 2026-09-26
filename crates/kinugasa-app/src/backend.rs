use std::{future::Future, sync::Arc};

use kinugasa_core::{
    application::{
        CameraUseCases, LockfileUseCases, MediaCoordinator, PreviewUseCases, SessionUseCases,
        StatisticsUseCases, TakeUseCases, UploadCoordinator,
    },
    ports::UuidV7IdGenerator,
};
use kinugasa_media::MediaService;
use kinugasa_mysql::MySqlRepository;
use kinugasa_s3::S3ObjectStorage;
use tokio::{sync::watch, task::JoinSet, time::MissedTickBehavior};
use tracing::{error, info, warn};

use crate::{AppConfig, AppError, Services, SystemClock};

type MediaReconciler = MediaCoordinator<MySqlRepository, MySqlRepository, MediaService>;
type UploadReconciler =
    UploadCoordinator<MySqlRepository, MySqlRepository, S3ObjectStorage, SystemClock>;

pub struct Backend {
    services: Services,
    repository: Arc<MySqlRepository>,
    object_storage: Arc<S3ObjectStorage>,
    media: Arc<MediaService>,
    media_reconciler: Arc<MediaReconciler>,
    upload_reconciler: Arc<UploadReconciler>,
    runtime: crate::RuntimeConfig,
}

impl Backend {
    pub async fn build(config: AppConfig) -> Result<Self, AppError> {
        if config.preview_token_lifetime.is_zero() {
            return Err(AppError::InvalidConfiguration(
                "preview token lifetime must be positive".into(),
            ));
        }
        if config.runtime.camera_reconcile_interval.is_zero()
            || config.runtime.upload_reconcile_interval.is_zero()
        {
            return Err(AppError::InvalidConfiguration(
                "reconciliation intervals must be positive".into(),
            ));
        }
        let repository = Arc::new(
            MySqlRepository::connect(&config.database.url, config.database.max_connections)
                .await
                .map_err(AppError::DatabaseConnect)?,
        );
        if config.database.migrate {
            repository
                .migrate()
                .await
                .map_err(AppError::DatabaseMigration)?;
        }

        let object_storage = Arc::new(S3ObjectStorage::new(config.storage).await);
        let media = Arc::new(
            MediaService::new(config.media, config.rist, config.moq)
                .await
                .map_err(AppError::MediaBuild)?,
        );
        let clock = Arc::new(SystemClock);
        let ids = Arc::new(UuidV7IdGenerator::new());

        let sessions = Arc::new(SessionUseCases::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&clock),
            Arc::clone(&ids),
        ));
        let cameras = Arc::new(CameraUseCases::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&clock),
            Arc::clone(&ids),
        ));
        let takes = Arc::new(TakeUseCases::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&media),
            Arc::clone(&clock),
            Arc::clone(&ids),
            config.recording_layout,
        ));
        let previews = Arc::new(
            PreviewUseCases::new(
                Arc::clone(&repository),
                Arc::clone(&repository),
                Arc::clone(&media),
                config.preview_token_lifetime,
            )
            .map_err(|error| AppError::InvalidConfiguration(error.to_string()))?,
        );
        let statistics = Arc::new(StatisticsUseCases::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&media),
        ));
        let lockfiles = Arc::new(LockfileUseCases::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            config.lockfile,
        ));
        let services = Services {
            sessions,
            cameras,
            takes,
            previews,
            statistics,
            lockfiles,
        };
        let media_reconciler = Arc::new(MediaCoordinator::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&media),
        ));
        let upload_reconciler = Arc::new(UploadCoordinator::new(
            Arc::clone(&repository),
            Arc::clone(&repository),
            Arc::clone(&object_storage),
            clock,
        ));

        Ok(Self {
            services,
            repository,
            object_storage,
            media,
            media_reconciler,
            upload_reconciler,
            runtime: config.runtime,
        })
    }

    #[must_use]
    pub const fn services(&self) -> &Services {
        &self.services
    }

    /// Runs recovery and reconciliation until shutdown is requested.
    ///
    /// An inbound adapter may clone [`Services`] before calling this method,
    /// but it must drop that clone before its shutdown future resolves so the
    /// media service can be shut down deterministically.
    pub async fn run_until(
        self,
        shutdown: impl Future<Output = ()> + Send,
    ) -> Result<(), AppError> {
        let report = match self.media_reconciler.recover_after_restart().await {
            Ok(report) => report,
            Err(error) => {
                let error = AppError::StartupRecovery(error);
                if let Err(shutdown_error) = self.shutdown().await {
                    error!(%shutdown_error, "backend cleanup after startup failure failed");
                }
                return Err(error);
            }
        };
        info!(
            interrupted_recordings = report.interrupted_recordings,
            provisioned_cameras = report.provisioned_cameras,
            completed_deletions = report.completed_deletions,
            deferred_cameras = report.deferred_cameras,
            "startup recovery completed"
        );

        let (stop_sender, stop_receiver) = watch::channel(false);
        let mut tasks = JoinSet::new();
        tasks.spawn(run_media_events(
            Arc::clone(&self.media_reconciler),
            stop_receiver.clone(),
        ));
        tasks.spawn(run_camera_reconciliation(
            Arc::clone(&self.media_reconciler),
            self.runtime.camera_reconcile_interval,
            stop_receiver.clone(),
        ));
        tasks.spawn(run_upload_reconciliation(
            Arc::clone(&self.upload_reconciler),
            self.runtime.upload_reconcile_interval,
            self.runtime.upload_batch_size,
            stop_receiver,
        ));

        tokio::pin!(shutdown);
        let result = tokio::select! {
            () = &mut shutdown => Ok(()),
            task = tasks.join_next() => match task {
                Some(Ok(result)) => result,
                Some(Err(error)) => Err(AppError::Task(error)),
                None => Ok(()),
            },
        };

        let _ = stop_sender.send(true);
        while let Some(task) = tasks.join_next().await {
            match task {
                Ok(Ok(())) => {}
                Ok(Err(error)) => error!(%error, "backend task stopped during shutdown"),
                Err(error) => error!(%error, "backend task failed during shutdown"),
            }
        }

        let shutdown_result = self.shutdown().await;
        result.and(shutdown_result)
    }

    async fn shutdown(self) -> Result<(), AppError> {
        let Backend {
            services,
            repository,
            object_storage,
            media,
            media_reconciler,
            upload_reconciler,
            runtime: _,
        } = self;
        drop(services);
        drop(media_reconciler);
        drop(upload_reconciler);
        drop(object_storage);

        let media = Arc::try_unwrap(media).map_err(|_| AppError::OutstandingMediaReferences)?;
        let shutdown_result = media.shutdown().await.map_err(AppError::MediaShutdown);
        repository.pool().close().await;
        shutdown_result
    }
}

async fn run_media_events(
    coordinator: Arc<MediaReconciler>,
    mut stop: watch::Receiver<bool>,
) -> Result<(), AppError> {
    loop {
        tokio::select! {
            changed = stop.changed() => {
                if changed.is_err() || *stop.borrow() {
                    return Ok(());
                }
            }
            result = coordinator.process_next_event() => {
                result.map_err(AppError::MediaEvents)?;
            }
        }
    }
}

async fn run_camera_reconciliation(
    coordinator: Arc<MediaReconciler>,
    interval: std::time::Duration,
    mut stop: watch::Receiver<bool>,
) -> Result<(), AppError> {
    let mut ticker = tokio::time::interval_at(tokio::time::Instant::now() + interval, interval);
    ticker.set_missed_tick_behavior(MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            changed = stop.changed() => {
                if changed.is_err() || *stop.borrow() {
                    return Ok(());
                }
            }
            _ = ticker.tick() => match coordinator.reconcile_camera_resources().await {
                Ok(report) if report.provisioned_cameras != 0
                    || report.completed_deletions != 0
                    || report.deferred_cameras != 0 => info!(
                        provisioned_cameras = report.provisioned_cameras,
                        completed_deletions = report.completed_deletions,
                        deferred_cameras = report.deferred_cameras,
                        "camera resources reconciled"
                    ),
                Ok(_) => {}
                Err(error) => warn!(%error, "camera reconciliation failed; it will be retried"),
            }
        }
    }
}

async fn run_upload_reconciliation(
    coordinator: Arc<UploadReconciler>,
    interval: std::time::Duration,
    batch_size: std::num::NonZeroU32,
    mut stop: watch::Receiver<bool>,
) -> Result<(), AppError> {
    let mut ticker = tokio::time::interval_at(tokio::time::Instant::now(), interval);
    ticker.set_missed_tick_behavior(MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            changed = stop.changed() => {
                if changed.is_err() || *stop.borrow() {
                    return Ok(());
                }
            }
            _ = ticker.tick() => match coordinator.reconcile(batch_size).await {
                Ok(report) if report.completed != 0 || report.errored != 0 || report.deferred != 0 => info!(
                    completed = report.completed,
                    errored = report.errored,
                    deferred = report.deferred,
                    "recording uploads reconciled"
                ),
                Ok(_) => {}
                Err(error) => warn!(%error, "upload reconciliation failed; it will be retried"),
            }
        }
    }
}
