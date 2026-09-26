use std::{error::Error, time::Duration};

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use thiserror::Error;
use url::Url;

use crate::domain::{
    AccessToken, CameraIdentityId, CameraInputState, CameraName, FinalizedRecording,
    GatewayInstance, RelativePath, SessionId, SessionName, TakeId, ValidationError,
};

type BoxError = Box<dyn Error + Send + Sync + 'static>;

#[derive(Debug, Error)]
pub enum MediaError {
    #[error("media operation conflicts with current state")]
    Conflict,
    #[error("media resource was not found")]
    NotFound,
    #[error("media operation timed out")]
    Timeout,
    #[error("media service is unavailable")]
    Unavailable(#[source] BoxError),
    #[error("unexpected media failure")]
    Unexpected(#[source] BoxError),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProvisionCameraRequest {
    pub session_id: SessionId,
    pub session_name: SessionName,
    pub camera_identity_id: CameraIdentityId,
    pub camera_name: CameraName,
}

/// Publishing information returned to the camera-facing API. The transport is
/// intentionally represented by a URL so the core is not coupled to a QUIC or
/// MoQ implementation crate.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CameraPublishAccess {
    pub endpoint: Url,
    pub access_token: Option<AccessToken>,
    pub expires_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PreviewAccessRequest {
    pub session_id: SessionId,
    pub valid_for: Duration,
}

/// SHA-256 fingerprint used by browser WebTransport clients to pin a
/// self-signed server certificate.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct ServerCertificateHash([u8; Self::LENGTH]);

impl ServerCertificateHash {
    pub const LENGTH: usize = 32;

    pub fn from_hex(value: &str) -> Result<Self, ValidationError> {
        if value.len() != Self::LENGTH * 2 {
            return Err(ValidationError::new(
                "server_certificate_hash",
                "must contain exactly 64 hexadecimal characters",
            ));
        }
        let mut bytes = [0; Self::LENGTH];
        for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
            let high = decode_hex_digit(pair[0]).ok_or_else(|| {
                ValidationError::new(
                    "server_certificate_hash",
                    "must contain only hexadecimal characters",
                )
            })?;
            let low = decode_hex_digit(pair[1]).ok_or_else(|| {
                ValidationError::new(
                    "server_certificate_hash",
                    "must contain only hexadecimal characters",
                )
            })?;
            bytes[index] = (high << 4) | low;
        }
        Ok(Self(bytes))
    }

    #[must_use]
    pub const fn as_bytes(&self) -> &[u8; Self::LENGTH] {
        &self.0
    }

    #[must_use]
    pub fn to_hex(self) -> String {
        const HEX: &[u8; 16] = b"0123456789abcdef";
        let mut output = String::with_capacity(Self::LENGTH * 2);
        for byte in self.0 {
            output.push(char::from(HEX[usize::from(byte >> 4)]));
            output.push(char::from(HEX[usize::from(byte & 0x0f)]));
        }
        output
    }
}

const fn decode_hex_digit(value: u8) -> Option<u8> {
    match value {
        b'0'..=b'9' => Some(value - b'0'),
        b'a'..=b'f' => Some(value - b'a' + 10),
        b'A'..=b'F' => Some(value - b'A' + 10),
        _ => None,
    }
}

/// Short-lived subscriber credentials consumed by the web console.
///
/// `endpoint` is the session-scoped HTTPS WebTransport endpoint. Browser
/// clients append `access_token` unchanged as the `jwt` query parameter when
/// establishing the Media over QUIC connection. Keeping the secret separate
/// preserves redaction in logs and keeps it out of otherwise routinely logged
/// endpoint URLs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PreviewAccess {
    pub endpoint: Url,
    pub access_token: AccessToken,
    pub expires_at: DateTime<Utc>,
    /// Empty for a certificate trusted through the browser's normal trust
    /// store. Populated for an ephemeral self-signed development identity.
    pub server_certificate_hashes: Vec<ServerCertificateHash>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StartRecordingRequest {
    pub take_id: TakeId,
    pub session_id: SessionId,
    pub camera_identity_id: CameraIdentityId,
    pub relative_path: RelativePath,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RecordingStarted {
    pub take_id: TakeId,
    pub camera_identity_id: CameraIdentityId,
    /// Time of the first decodable random-access point actually admitted to
    /// the recording, not the command receipt time.
    pub started_at: DateTime<Utc>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MediaEventKind {
    CameraInputChanged {
        camera_identity_id: CameraIdentityId,
        state: CameraInputState,
    },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MediaEvent {
    pub occurred_at: DateTime<Utc>,
    pub kind: MediaEventKind,
}

/// Transport telemetry retained for the existing RIST statistics API.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RistStatistics {
    pub session_name: SessionName,
    pub camera_name: CameraName,
    pub gateway_instance: GatewayInstance,
    pub flow_id: u32,
    pub interval_start: DateTime<Utc>,
    pub interval_end: DateTime<Utc>,
    pub output_packets: u64,
    pub lost_packets: u64,
    pub recovered_packets: u64,
    pub discontinuities: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RistStatisticsSnapshot {
    pub statistics: RistStatistics,
    pub stale: bool,
}

/// Provisions camera publishers. Implementations may share one physical
/// listener and credential among cameras in the same session while using
/// transport-level stream identifiers to multiplex them. Different sessions
/// must remain isolated from one another.
///
/// Implementations may use MoQ directly or hide a different camera-side
/// ingest protocol behind the returned endpoint.
/// Provision and revoke operations must be idempotent because database-driven
/// camera reconciliation may repeat them after an interrupted attempt.
#[async_trait]
pub trait CameraIngress: Send + Sync {
    async fn provision_camera(
        &self,
        request: &ProvisionCameraRequest,
    ) -> Result<CameraPublishAccess, MediaError>;

    async fn revoke_camera(&self, camera_id: CameraIdentityId) -> Result<(), MediaError>;
}

/// Issues access to the integrated SFU. It does not expose rooms, tracks, or
/// vendor-specific LiveKit concepts to the application layer.
#[async_trait]
pub trait PreviewService: Send + Sync {
    async fn issue_preview_access(
        &self,
        request: &PreviewAccessRequest,
    ) -> Result<PreviewAccess, MediaError>;
}

/// Controls the recorder embedded in the media server.
///
/// An active recording belongs to the current process incarnation. It must be
/// classified as interrupted after a process restart and must never be resumed
/// by reconciling it from database state.
#[async_trait]
pub trait RecordingService: Send + Sync {
    async fn start_recording(
        &self,
        request: &StartRecordingRequest,
    ) -> Result<RecordingStarted, MediaError>;

    async fn finish_recording(
        &self,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<FinalizedRecording, MediaError>;

    async fn abort_recording(
        &self,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<(), MediaError>;
}

/// Ordered, process-local source of input state changes. These notifications
/// reduce connection-state propagation latency; they are not a durable log and
/// must not be used to recover or resume recording state.
#[async_trait]
pub trait MediaEventSource: Send + Sync {
    async fn next_event(&self) -> Result<MediaEvent, MediaError>;
}

/// Read side for the existing RIST telemetry endpoint. Freshness is evaluated
/// by the implementation against its receipt time, not the camera clock.
#[async_trait]
pub trait RistStatisticsSource: Send + Sync {
    async fn list_rist_statistics(
        &self,
        session_name: &SessionName,
    ) -> Result<Vec<RistStatisticsSnapshot>, MediaError>;
}

/// Convenience bound for a component that provides the entire integrated
/// media-server surface.
pub trait MediaServer:
    CameraIngress + PreviewService + RecordingService + MediaEventSource + RistStatisticsSource
{
}

impl<T> MediaServer for T where
    T: CameraIngress + PreviewService + RecordingService + MediaEventSource + RistStatisticsSource
{
}

#[cfg(test)]
mod tests {
    use super::ServerCertificateHash;

    #[test]
    fn server_certificate_hash_round_trips_hex() {
        let value = "0123456789abcdef0123456789ABCDEF0123456789abcdef0123456789ABCDEF";
        let hash = ServerCertificateHash::from_hex(value).unwrap();
        assert_eq!(
            hash.to_hex(),
            "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
        );
    }

    #[test]
    fn server_certificate_hash_rejects_invalid_hex() {
        assert!(ServerCertificateHash::from_hex("00").is_err());
        assert!(
            ServerCertificateHash::from_hex(
                "gg23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
            )
            .is_err()
        );
    }
}
