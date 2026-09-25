use chrono::{DateTime, Utc};

use super::{SessionId, SessionName};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SessionState {
    Active,
    Inactive,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Session {
    id: SessionId,
    name: SessionName,
    state: SessionState,
    created_at: DateTime<Utc>,
}

impl Session {
    #[must_use]
    pub const fn new(
        id: SessionId,
        name: SessionName,
        state: SessionState,
        created_at: DateTime<Utc>,
    ) -> Self {
        Self {
            id,
            name,
            state,
            created_at,
        }
    }

    #[must_use]
    pub const fn id(&self) -> SessionId {
        self.id
    }

    #[must_use]
    pub const fn name(&self) -> &SessionName {
        &self.name
    }

    #[must_use]
    pub const fn state(&self) -> SessionState {
        self.state
    }

    #[must_use]
    pub const fn created_at(&self) -> DateTime<Utc> {
        self.created_at
    }

    pub fn deactivate(&mut self) {
        self.state = SessionState::Inactive;
    }
}
