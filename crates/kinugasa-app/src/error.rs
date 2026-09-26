use thiserror::Error;

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("{0} is required")]
    Missing(&'static str),
    #[error("invalid {name}: {message}")]
    Invalid { name: &'static str, message: String },
}

impl ConfigError {
    pub(crate) fn invalid(name: &'static str, error: impl std::fmt::Display) -> Self {
        Self::Invalid {
            name,
            message: error.to_string(),
        }
    }
}

#[derive(Debug, Error)]
pub enum AppError {
    #[error("invalid application configuration: {0}")]
    InvalidConfiguration(String),
    #[error("failed to connect to MySQL: {0}")]
    DatabaseConnect(#[source] sqlx::Error),
    #[error("failed to migrate MySQL: {0}")]
    DatabaseMigration(#[source] sqlx::migrate::MigrateError),
    #[error("failed to start the media server: {0}")]
    MediaBuild(#[source] kinugasa_media::MediaBuildError),
    #[error("startup recovery failed: {0}")]
    StartupRecovery(#[source] kinugasa_core::application::UseCaseError),
    #[error("media event processing failed: {0}")]
    MediaEvents(#[source] kinugasa_core::application::UseCaseError),
    #[error("a backend task panicked or was cancelled: {0}")]
    Task(#[source] tokio::task::JoinError),
    #[error("the media server is still referenced after backend tasks stopped")]
    OutstandingMediaReferences,
    #[error("failed to shut down the media server: {0}")]
    MediaShutdown(#[source] kinugasa_media::MediaShutdownError),
}
