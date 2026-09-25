use chrono::{DateTime, Utc};

use crate::domain::{CameraIdentityId, SessionId, TakeId};

pub trait Clock: Send + Sync {
    fn now(&self) -> DateTime<Utc>;
}

/// ID generation is a port so application tests do not depend on wall-clock or
/// random UUID generation.
pub trait IdGenerator: Send + Sync {
    fn session_id(&self) -> SessionId;
    fn camera_identity_id(&self) -> CameraIdentityId;
    fn take_id(&self) -> TakeId;
}

/// Production ID generator backed by time-ordered UUID version 7 values.
#[derive(Debug, Clone, Copy, Default)]
pub struct UuidV7IdGenerator;

impl UuidV7IdGenerator {
    #[must_use]
    pub const fn new() -> Self {
        Self
    }
}

impl IdGenerator for UuidV7IdGenerator {
    fn session_id(&self) -> SessionId {
        SessionId::new_v7()
    }

    fn camera_identity_id(&self) -> CameraIdentityId {
        CameraIdentityId::new_v7()
    }

    fn take_id(&self) -> TakeId {
        TakeId::new_v7()
    }
}

#[cfg(test)]
mod tests {
    use uuid::Variant;

    use super::*;

    #[test]
    fn uuid_v7_generator_returns_distinct_rfc_4122_v7_ids() {
        let generator = UuidV7IdGenerator::new();
        let first_session = generator.session_id();
        let second_session = generator.session_id();
        let camera = generator.camera_identity_id();
        let take = generator.take_id();

        assert_ne!(first_session, second_session);
        for uuid in [
            first_session.into_uuid(),
            camera.into_uuid(),
            take.into_uuid(),
        ] {
            assert_eq!(uuid.get_version_num(), 7);
            assert_eq!(uuid.get_variant(), Variant::RFC4122);
        }
    }
}
