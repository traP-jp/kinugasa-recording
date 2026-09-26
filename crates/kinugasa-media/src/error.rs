use thiserror::Error;

#[derive(Debug, Error)]
pub enum MediaBuildError {
    #[error("invalid media configuration: {0}")]
    InvalidConfiguration(String),
    #[error("failed to prepare recording root")]
    RecordingRoot(#[source] std::io::Error),
    #[error("failed to initialize RIST transport")]
    Rist(#[from] crate::RistError),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Error)]
pub enum IngressError {
    #[error("camera is not provisioned")]
    CameraNotFound,
    #[error("camera media queue is full")]
    QueueFull,
    #[error("camera media task has stopped")]
    Unavailable,
}
