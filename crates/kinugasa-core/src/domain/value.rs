use std::{fmt, str::FromStr, sync::LazyLock};

use regex::Regex;
use thiserror::Error;
use uuid::{Uuid, Variant};

use super::ValidationError;

static RESOURCE_NAME: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$")
        .expect("the resource-name regular expression is valid")
});

#[derive(Debug, Clone, PartialEq, Eq, Error)]
pub enum ParseIdError {
    #[error("invalid UUID: {0}")]
    InvalidUuid(#[from] uuid::Error),
    #[error("UUID must use canonical lowercase RFC 4122 form with a version from 1 through 8")]
    InvalidFormat,
}

macro_rules! define_id {
    ($name:ident) => {
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
        pub struct $name(Uuid);

        impl $name {
            pub fn from_uuid(value: Uuid) -> Result<Self, ParseIdError> {
                let version = value.get_version_num();
                if value.get_variant() != Variant::RFC4122 || !(1..=8).contains(&version) {
                    return Err(ParseIdError::InvalidFormat);
                }
                Ok(Self(value))
            }

            #[must_use]
            pub fn new_v7() -> Self {
                Self(Uuid::now_v7())
            }

            #[must_use]
            pub const fn as_uuid(&self) -> &Uuid {
                &self.0
            }

            #[must_use]
            pub const fn into_uuid(self) -> Uuid {
                self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                self.0.fmt(formatter)
            }
        }

        impl FromStr for $name {
            type Err = ParseIdError;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                let parsed = Uuid::parse_str(value)?;
                if parsed.hyphenated().to_string() != value {
                    return Err(ParseIdError::InvalidFormat);
                }
                Self::from_uuid(parsed)
            }
        }
    };
}

define_id!(SessionId);
define_id!(CameraIdentityId);
define_id!(TakeId);
define_id!(MediaProcessId);

macro_rules! define_resource_name {
    ($name:ident, $field:literal) => {
        #[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
        pub struct $name(String);

        impl $name {
            pub fn new(value: impl Into<String>) -> Result<Self, ValidationError> {
                let value = value.into();
                if !RESOURCE_NAME.is_match(&value) {
                    return Err(ValidationError::new(
                        $field,
                        "must match ^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$",
                    ));
                }
                Ok(Self(value))
            }

            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }

            #[must_use]
            pub fn into_inner(self) -> String {
                self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(&self.0)
            }
        }

        impl AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.as_str()
            }
        }

        impl FromStr for $name {
            type Err = ValidationError;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                Self::new(value)
            }
        }

        impl TryFrom<String> for $name {
            type Error = ValidationError;

            fn try_from(value: String) -> Result<Self, Self::Error> {
                Self::new(value)
            }
        }
    };
}

define_resource_name!(SessionName, "session_name");
define_resource_name!(CameraName, "camera_name");
define_resource_name!(TakeName, "take_name");

macro_rules! define_non_empty_string {
    ($name:ident, $field:literal) => {
        #[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
        pub struct $name(String);

        impl $name {
            pub fn new(value: impl Into<String>) -> Result<Self, ValidationError> {
                let value = value.into();
                if value.trim().is_empty() {
                    return Err(ValidationError::new($field, "must not be empty"));
                }
                Ok(Self(value))
            }

            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }

            #[must_use]
            pub fn into_inner(self) -> String {
                self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(&self.0)
            }
        }

        impl AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.as_str()
            }
        }

        impl FromStr for $name {
            type Err = ValidationError;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                Self::new(value)
            }
        }
    };
}

define_non_empty_string!(ErrorReason, "error_reason");
define_non_empty_string!(ObjectKey, "object_key");
define_non_empty_string!(MediaType, "media_type");
define_non_empty_string!(GatewayInstance, "gateway_instance");

/// A normalized, slash-separated path relative to the recording root.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct RelativePath(String);

impl RelativePath {
    pub fn new(value: impl Into<String>) -> Result<Self, ValidationError> {
        let value = value.into();
        let invalid = value.is_empty()
            || value.starts_with('/')
            || value.ends_with('/')
            || value.contains('\\')
            || value
                .split('/')
                .any(|part| part.is_empty() || part == "." || part == "..");
        if invalid {
            return Err(ValidationError::new(
                "relative_path",
                "must be a normalized, non-empty slash-separated relative path",
            ));
        }
        Ok(Self(value))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    #[must_use]
    pub fn into_inner(self) -> String {
        self.0
    }
}

impl fmt::Display for RelativePath {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl FromStr for RelativePath {
    type Err = ValidationError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::new(value)
    }
}

/// A credential whose debug representation never contains the secret value.
#[derive(Clone, PartialEq, Eq)]
pub struct AccessToken(String);

impl AccessToken {
    pub fn new(value: impl Into<String>) -> Result<Self, ValidationError> {
        let value = value.into();
        if value.trim().is_empty() {
            return Err(ValidationError::new("access_token", "must not be empty"));
        }
        Ok(Self(value))
    }

    #[must_use]
    pub fn expose_secret(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for AccessToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("AccessToken([REDACTED])")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct ContentHash([u8; Self::LENGTH]);

impl ContentHash {
    pub const LENGTH: usize = 32;

    #[must_use]
    pub const fn from_bytes(value: [u8; Self::LENGTH]) -> Self {
        Self(value)
    }

    pub fn try_from_slice(value: &[u8]) -> Result<Self, ValidationError> {
        let value: [u8; Self::LENGTH] = value
            .try_into()
            .map_err(|_| ValidationError::new("content_hash", "must contain exactly 32 bytes"))?;
        Ok(Self(value))
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

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct FileSize(u64);

impl FileSize {
    #[must_use]
    pub const fn from_bytes(value: u64) -> Self {
        Self(value)
    }

    #[must_use]
    pub const fn bytes(self) -> u64 {
        self.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resource_names_preserve_v2_rules() {
        assert!(SessionName::new("studio-a").is_ok());
        assert!(SessionName::new("Invalid").is_err());
        assert!(SessionName::new("a-".to_owned() + &"b".repeat(31)).is_err());
    }

    #[test]
    fn relative_paths_reject_traversal_and_non_normalized_paths() {
        assert!(RelativePath::new("recording/session/take/video.ts").is_ok());
        for invalid in ["", "/video.ts", "a//b", "a/./b", "a/../b", "a\\b", "a/"] {
            assert!(RelativePath::new(invalid).is_err(), "accepted {invalid:?}");
        }
    }

    #[test]
    fn access_token_is_redacted() {
        let token = AccessToken::new("do-not-log").unwrap();
        assert_eq!(format!("{token:?}"), "AccessToken([REDACTED])");
    }

    #[test]
    fn identifiers_preserve_v2_uuid_rules() {
        assert!(SessionId::from_str("019c240d-a6de-7de0-a826-0f26e8803fc0").is_ok());
        assert!(SessionId::from_str("019C240D-A6DE-7DE0-A826-0F26E8803FC0").is_err());
        assert!(SessionId::from_str("00000000-0000-0000-0000-000000000000").is_err());
        assert!(SessionId::from_str("019c240da6de7de0a8260f26e8803fc0").is_err());
    }
}
