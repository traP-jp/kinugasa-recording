use thiserror::Error;

/// A value that could not be admitted into the domain model.
#[derive(Debug, Clone, PartialEq, Eq, Error)]
#[error("{field}: {reason}")]
pub struct ValidationError {
    field: &'static str,
    reason: String,
}

impl ValidationError {
    #[must_use]
    pub fn new(field: &'static str, reason: impl Into<String>) -> Self {
        Self {
            field,
            reason: reason.into(),
        }
    }

    #[must_use]
    pub const fn field(&self) -> &'static str {
        self.field
    }

    #[must_use]
    pub fn reason(&self) -> &str {
        &self.reason
    }
}
