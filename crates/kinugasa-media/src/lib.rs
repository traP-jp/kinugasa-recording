//! Process-local media server implementation for kinugasa recording.
//!
//! The crate owns the volatile camera and recording state. Database recovery
//! remains in `kinugasa-core`: after a restart cameras are provisioned again,
//! while recordings from the previous process incarnation are failed rather
//! than resumed.

mod camera;
mod config;
mod error;
mod moq;
mod recording;
mod rist;
mod service;

pub use config::MediaConfig;
pub use error::{IngressError, MediaBuildError, MediaShutdownError};
pub use moq::{MoqConfig, MoqError, MoqTlsIdentity};
pub use rist::{RistConfig, RistError, RistServer};
pub use service::{
    IngressPacket, MediaIngress, MediaService, PreviewPacket, PreviewReceiveError,
    PreviewSubscription, TransportStatistics,
};
