use thiserror::Error;

#[derive(Debug, Error)]
pub enum MediaBuildError {
    #[error("invalid media configuration: {0}")]
    InvalidConfiguration(String),
    #[error("failed to prepare recording root")]
    RecordingRoot(#[source] std::io::Error),
    #[error("failed to initialize RIST transport")]
    Rist(#[from] crate::RistError),
    #[error("failed to initialize Media over QUIC transport")]
    Moq(#[from] crate::MoqError),
}

#[derive(Debug, Error)]
pub enum MediaShutdownError {
    #[error("failed to shut down RIST transport")]
    Rist(#[source] crate::RistError),
    #[error("failed to shut down Media over QUIC transport")]
    Moq(#[source] crate::MoqError),
    #[error("failed to shut down RIST and Media over QUIC transports")]
    Both {
        rist: crate::RistError,
        moq: crate::MoqError,
    },
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
