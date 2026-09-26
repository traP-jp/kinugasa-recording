//! Production composition root for kinugasa recording.
//!
//! Protocol adapters can depend on [`Services`] without knowing how the
//! repository, media server, object storage, clock, or ID generator are
//! constructed. [`Backend`] owns their process lifecycle.

#![forbid(unsafe_code)]

mod api;
mod backend;
mod clock;
mod config;
mod error;
mod services;

pub use api::router as api_router;
pub use backend::Backend;
pub use clock::SystemClock;
pub use config::{AppConfig, DatabaseConfig, RuntimeConfig};
pub use error::{AppError, ConfigError};
pub use services::{
    CameraService, LockfileService, PreviewService, Services, SessionService, StatisticsService,
    TakeService,
};
