use chrono::{DateTime, Utc};
use url::Url;

use super::{
    CameraIdentityId, CameraName, ErrorReason, MediaProcessId, SessionId, ValidationError,
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CameraIdentity {
    id: CameraIdentityId,
    session_id: SessionId,
    name: CameraName,
    created_at: DateTime<Utc>,
}

impl CameraIdentity {
    #[must_use]
    pub const fn new(
        id: CameraIdentityId,
        session_id: SessionId,
        name: CameraName,
        created_at: DateTime<Utc>,
    ) -> Self {
        Self {
            id,
            session_id,
            name,
            created_at,
        }
    }

    #[must_use]
    pub const fn id(&self) -> CameraIdentityId {
        self.id
    }

    #[must_use]
    pub const fn session_id(&self) -> SessionId {
        self.session_id
    }

    #[must_use]
    pub const fn name(&self) -> &CameraName {
        &self.name
    }

    #[must_use]
    pub const fn created_at(&self) -> DateTime<Utc> {
        self.created_at
    }
}

/// The URL and error combinations are encoded in the variant, making the v2
/// invalid combinations unrepresentable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CameraConnectionState {
    Activating,
    Waiting { endpoint: Url },
    Connected { endpoint: Url },
    Errored { endpoint: Url, reason: ErrorReason },
}

impl CameraConnectionState {
    #[must_use]
    pub const fn status(&self) -> CameraConnectionStatus {
        match self {
            Self::Activating => CameraConnectionStatus::Activating,
            Self::Waiting { .. } => CameraConnectionStatus::Waiting,
            Self::Connected { .. } => CameraConnectionStatus::Connected,
            Self::Errored { .. } => CameraConnectionStatus::Errored,
        }
    }

    #[must_use]
    pub fn endpoint(&self) -> Option<&Url> {
        match self {
            Self::Activating => None,
            Self::Waiting { endpoint }
            | Self::Connected { endpoint }
            | Self::Errored { endpoint, .. } => Some(endpoint),
        }
    }

    #[must_use]
    pub fn error_reason(&self) -> Option<&ErrorReason> {
        match self {
            Self::Errored { reason, .. } => Some(reason),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CameraConnectionStatus {
    Activating,
    Waiting,
    Connected,
    Errored,
}

/// Runtime input observation applied without replacing the provisioned
/// publishing endpoint.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CameraInputState {
    Waiting,
    Connected,
    Errored(ErrorReason),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CameraConnection {
    camera_identity_id: CameraIdentityId,
    state: CameraConnectionState,
    virtual_port: Option<u16>,
    media_process_id: Option<MediaProcessId>,
    deletion_requested_at: Option<DateTime<Utc>>,
}

impl CameraConnection {
    #[must_use]
    pub const fn new(camera_identity_id: CameraIdentityId, state: CameraConnectionState) -> Self {
        Self {
            camera_identity_id,
            state,
            virtual_port: None,
            media_process_id: None,
            deletion_requested_at: None,
        }
    }

    #[must_use]
    pub const fn camera_identity_id(&self) -> CameraIdentityId {
        self.camera_identity_id
    }

    #[must_use]
    pub const fn state(&self) -> &CameraConnectionState {
        &self.state
    }

    #[must_use]
    pub const fn media_process_id(&self) -> Option<MediaProcessId> {
        self.media_process_id
    }

    #[must_use]
    pub const fn virtual_port(&self) -> Option<u16> {
        self.virtual_port
    }

    #[must_use]
    pub const fn deletion_requested_at(&self) -> Option<DateTime<Utc>> {
        self.deletion_requested_at
    }

    pub fn set_state(&mut self, state: CameraConnectionState) {
        self.state = state;
    }

    pub fn apply_input_state(&mut self, state: CameraInputState) -> Result<(), ValidationError> {
        let endpoint = self.state.endpoint().cloned().ok_or_else(|| {
            ValidationError::new(
                "camera_connection.state",
                "must be provisioned before applying input state",
            )
        })?;
        self.state = match state {
            CameraInputState::Waiting => CameraConnectionState::Waiting { endpoint },
            CameraInputState::Connected => CameraConnectionState::Connected { endpoint },
            CameraInputState::Errored(reason) => {
                CameraConnectionState::Errored { endpoint, reason }
            }
        };
        Ok(())
    }

    pub fn set_media_process_id(&mut self, id: Option<MediaProcessId>) {
        self.media_process_id = id;
    }

    pub fn set_virtual_port(&mut self, virtual_port: Option<u16>) {
        self.virtual_port = virtual_port;
    }

    pub fn request_deletion(&mut self, at: DateTime<Utc>) {
        self.deletion_requested_at = Some(at);
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Camera {
    identity: CameraIdentity,
    connection: CameraConnection,
}

impl Camera {
    pub fn new(
        identity: CameraIdentity,
        connection: CameraConnection,
    ) -> Result<Self, ValidationError> {
        if identity.id() != connection.camera_identity_id() {
            return Err(ValidationError::new(
                "camera.connection.camera_identity_id",
                "must match camera identity",
            ));
        }
        Ok(Self {
            identity,
            connection,
        })
    }

    #[must_use]
    pub const fn identity(&self) -> &CameraIdentity {
        &self.identity
    }

    #[must_use]
    pub const fn connection(&self) -> &CameraConnection {
        &self.connection
    }

    #[must_use]
    pub fn into_parts(self) -> (CameraIdentity, CameraConnection) {
        (self.identity, self.connection)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn input_observation_preserves_provisioned_endpoint() {
        let endpoint = Url::parse("rist://camera.example.test:9000").unwrap();
        let mut connection = CameraConnection::new(
            CameraIdentityId::new_v7(),
            CameraConnectionState::Waiting {
                endpoint: endpoint.clone(),
            },
        );

        connection
            .apply_input_state(CameraInputState::Connected)
            .unwrap();
        assert_eq!(connection.state().endpoint(), Some(&endpoint));

        connection
            .apply_input_state(CameraInputState::Errored(
                ErrorReason::new("input failed").unwrap(),
            ))
            .unwrap();
        assert_eq!(connection.state().endpoint(), Some(&endpoint));
        assert_eq!(
            connection.state().error_reason().map(ErrorReason::as_str),
            Some("input failed")
        );
    }

    #[test]
    fn activating_connection_rejects_input_observation() {
        let mut connection = CameraConnection::new(
            CameraIdentityId::new_v7(),
            CameraConnectionState::Activating,
        );
        assert!(
            connection
                .apply_input_state(CameraInputState::Waiting)
                .is_err()
        );
    }
}
