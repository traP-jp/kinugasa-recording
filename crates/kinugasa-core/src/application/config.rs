use crate::domain::{CameraName, RelativePath, SessionName, TakeName, ValidationError};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RecordingLayout {
    file_name: String,
}

impl RecordingLayout {
    pub fn new(file_name: impl Into<String>) -> Result<Self, ValidationError> {
        let file_name = file_name.into();
        if file_name.is_empty()
            || file_name == "."
            || file_name == ".."
            || file_name.contains('/')
            || file_name.contains('\\')
        {
            return Err(ValidationError::new(
                "recording_file_name",
                "must be a non-empty path component",
            ));
        }
        Ok(Self { file_name })
    }

    pub fn path(
        &self,
        session: &SessionName,
        take: &TakeName,
        camera: &CameraName,
    ) -> RelativePath {
        RelativePath::new(format!(
            "recording/{session}/{take}/{camera}/{}",
            self.file_name
        ))
        .expect("validated resource names and file name form a valid relative path")
    }

    #[must_use]
    pub fn file_name(&self) -> &str {
        &self.file_name
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LockfileConfig {
    bucket: String,
    schema_version: String,
}

impl LockfileConfig {
    pub fn new(
        bucket: impl Into<String>,
        schema_version: impl Into<String>,
    ) -> Result<Self, ValidationError> {
        let bucket = bucket.into();
        let schema_version = schema_version.into();
        if bucket.trim().is_empty() {
            return Err(ValidationError::new("bucket", "must not be empty"));
        }
        if schema_version.trim().is_empty() {
            return Err(ValidationError::new("schema_version", "must not be empty"));
        }
        Ok(Self {
            bucket,
            schema_version,
        })
    }

    #[must_use]
    pub fn bucket(&self) -> &str {
        &self.bucket
    }

    #[must_use]
    pub fn schema_version(&self) -> &str {
        &self.schema_version
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn recording_layout_builds_v2_compatible_paths() {
        let layout = RecordingLayout::new("video.ts").unwrap();
        let path = layout.path(
            &SessionName::new("session-1").unwrap(),
            &TakeName::new("take-1").unwrap(),
            &CameraName::new("camera-1").unwrap(),
        );
        assert_eq!(
            path.as_str(),
            "recording/session-1/take-1/camera-1/video.ts"
        );
    }
}
