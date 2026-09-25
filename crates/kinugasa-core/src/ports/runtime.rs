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
