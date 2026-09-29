use std::{path::PathBuf, time::Duration};

use kinugasa_core::domain::GatewayInstance;
use url::Url;

use crate::MediaBuildError;

#[derive(Debug, Clone)]
pub struct MediaConfig {
    pub recording_root: PathBuf,
    pub preview_endpoint: Url,
    pub gateway_instance: GatewayInstance,
    pub ingress_queue_capacity: usize,
    pub preview_queue_capacity: usize,
    pub statistics_stale_after: Duration,
}

impl MediaConfig {
    pub fn validate(&self) -> Result<(), MediaBuildError> {
        if self.recording_root.as_os_str().is_empty() {
            return Err(MediaBuildError::InvalidConfiguration(
                "recording_root must not be empty".into(),
            ));
        }
        if self.preview_endpoint.scheme() != "https" {
            return Err(MediaBuildError::InvalidConfiguration(
                "preview_endpoint must use https:// for WebTransport".into(),
            ));
        }
        if self.preview_endpoint.host_str().is_none() {
            return Err(MediaBuildError::InvalidConfiguration(
                "preview_endpoint must include a host".into(),
            ));
        }
        if self.ingress_queue_capacity == 0 {
            return Err(MediaBuildError::InvalidConfiguration(
                "ingress_queue_capacity must be positive".into(),
            ));
        }
        if self.preview_queue_capacity == 0 {
            return Err(MediaBuildError::InvalidConfiguration(
                "preview_queue_capacity must be positive".into(),
            ));
        }
        if self.statistics_stale_after.is_zero() {
            return Err(MediaBuildError::InvalidConfiguration(
                "statistics_stale_after must be positive".into(),
            ));
        }
        Ok(())
    }
}
