use std::{
    collections::BTreeSet,
    net::{IpAddr, SocketAddr},
    num::NonZeroU32,
    path::PathBuf,
    time::Duration,
};

use kinugasa_core::{
    application::{LockfileConfig, RecordingLayout},
    domain::GatewayInstance,
};
use kinugasa_media::{MediaConfig, MoqConfig, MoqTlsIdentity, RistConfig};
use kinugasa_s3::S3Config;
use url::Url;

use crate::ConfigError;

const DEFAULT_DATABASE_CONNECTIONS: u32 = 16;
const DEFAULT_MOQ_LISTEN_ADDRESS: &str = "0.0.0.0:4443";
const DEFAULT_RECORDING_ROOT: &str = "/recordings";

#[derive(Clone)]
pub struct DatabaseConfig {
    pub url: String,
    pub max_connections: u32,
    pub migrate: bool,
}

impl std::fmt::Debug for DatabaseConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("DatabaseConfig")
            .field("url", &"[REDACTED]")
            .field("max_connections", &self.max_connections)
            .field("migrate", &self.migrate)
            .finish()
    }
}

#[derive(Debug, Clone)]
pub struct RuntimeConfig {
    pub camera_reconcile_interval: Duration,
    pub upload_reconcile_interval: Duration,
    pub upload_batch_size: NonZeroU32,
}

#[derive(Debug, Clone)]
pub struct AppConfig {
    pub database: DatabaseConfig,
    pub storage: S3Config,
    pub media: MediaConfig,
    pub rist: RistConfig,
    pub moq: MoqConfig,
    pub recording_layout: RecordingLayout,
    pub lockfile: LockfileConfig,
    pub preview_token_lifetime: Duration,
    pub runtime: RuntimeConfig,
}

impl AppConfig {
    pub fn from_env() -> Result<Self, ConfigError> {
        Self::from_lookup(|name| std::env::var(name).ok())
    }

    fn from_lookup(mut get: impl FnMut(&str) -> Option<String>) -> Result<Self, ConfigError> {
        let database_url = required(&mut get, "DATABASE_URL")?;
        let max_connections = parse_or(
            &mut get,
            "KINUGASA_DB_MAX_CONNECTIONS",
            DEFAULT_DATABASE_CONNECTIONS,
        )?;
        if max_connections == 0 {
            return Err(ConfigError::invalid(
                "KINUGASA_DB_MAX_CONNECTIONS",
                "must be positive",
            ));
        }
        let migrate = parse_or(&mut get, "KINUGASA_DB_MIGRATE", true)?;

        let recording_root = PathBuf::from(
            non_empty(get("KINUGASA_RECORDING_ROOT"))
                .unwrap_or_else(|| DEFAULT_RECORDING_ROOT.to_owned()),
        );
        let bucket = required(&mut get, "KINUGASA_S3_BUCKET")?;
        let region = required(&mut get, "KINUGASA_S3_REGION")?;
        let mut storage = S3Config::new(&bucket, region, &recording_root)
            .map_err(|error| ConfigError::invalid("KINUGASA_RECORDING_ROOT", error))?;
        if let Some(endpoint) = non_empty(get("KINUGASA_S3_ENDPOINT")) {
            storage = storage.with_endpoint(parse_url("KINUGASA_S3_ENDPOINT", endpoint)?);
        }
        storage =
            storage.with_force_path_style(parse_or(&mut get, "KINUGASA_S3_PATH_STYLE", false)?);

        let preview_endpoint = parse_url(
            "KINUGASA_PREVIEW_ENDPOINT",
            required(&mut get, "KINUGASA_PREVIEW_ENDPOINT")?,
        )?;
        let gateway_instance =
            GatewayInstance::new(required(&mut get, "KINUGASA_GATEWAY_INSTANCE")?)
                .map_err(|error| ConfigError::invalid("KINUGASA_GATEWAY_INSTANCE", error))?;

        let media = MediaConfig {
            recording_root: recording_root.clone(),
            preview_endpoint,
            gateway_instance,
            ingress_queue_capacity: parse_or(&mut get, "KINUGASA_INGRESS_QUEUE_CAPACITY", 4_096)?,
            preview_queue_capacity: parse_or(&mut get, "KINUGASA_PREVIEW_QUEUE_CAPACITY", 1_024)?,
            recording_start_timeout: duration_or(
                &mut get,
                "KINUGASA_RECORDING_START_TIMEOUT",
                Duration::from_secs(10),
            )?,
            statistics_stale_after: duration_or(
                &mut get,
                "KINUGASA_STATISTICS_STALE_AFTER",
                Duration::from_secs(15),
            )?,
        };

        let rist = RistConfig {
            listen_address: parse_or(
                &mut get,
                "KINUGASA_RIST_LISTEN_ADDRESS",
                IpAddr::from([0, 0, 0, 0]),
            )?,
            public_endpoint: parse_url(
                "KINUGASA_RIST_PUBLIC_ENDPOINT",
                required(&mut get, "KINUGASA_RIST_PUBLIC_ENDPOINT")?,
            )?,
            available_ports: parse_ports(required(&mut get, "KINUGASA_RIST_PORTS")?)?,
            recovery_buffer: duration_or(
                &mut get,
                "KINUGASA_RIST_RECOVERY_BUFFER",
                Duration::from_secs(5),
            )?,
            reorder_buffer: duration_or(
                &mut get,
                "KINUGASA_RIST_REORDER_BUFFER",
                Duration::from_millis(200),
            )?,
            statistics_interval: duration_or(
                &mut get,
                "KINUGASA_RIST_STATISTICS_INTERVAL",
                Duration::from_secs(5),
            )?,
        };

        let moq = MoqConfig {
            listen_address: parse_or(
                &mut get,
                "KINUGASA_MOQ_LISTEN_ADDRESS",
                DEFAULT_MOQ_LISTEN_ADDRESS.parse::<SocketAddr>().unwrap(),
            )?,
            tls: tls_identity(&mut get)?,
        };

        let recording_layout = RecordingLayout::new(
            get("KINUGASA_RECORDING_FILE_NAME").unwrap_or_else(|| "video.ts".to_owned()),
        )
        .map_err(|error| ConfigError::invalid("KINUGASA_RECORDING_FILE_NAME", error))?;
        let lockfile = LockfileConfig::new(
            bucket,
            get("KINUGASA_LOCKFILE_SCHEMA_VERSION").unwrap_or_else(|| "1.0".to_owned()),
        )
        .map_err(|error| ConfigError::invalid("KINUGASA_LOCKFILE_SCHEMA_VERSION", error))?;

        Ok(Self {
            database: DatabaseConfig {
                url: database_url,
                max_connections,
                migrate,
            },
            storage,
            media,
            rist,
            moq,
            recording_layout,
            lockfile,
            preview_token_lifetime: duration_or(
                &mut get,
                "KINUGASA_PREVIEW_TOKEN_TTL",
                Duration::from_secs(5 * 60),
            )?,
            runtime: RuntimeConfig {
                camera_reconcile_interval: duration_or(
                    &mut get,
                    "KINUGASA_CAMERA_RECONCILE_INTERVAL",
                    Duration::from_secs(1),
                )?,
                upload_reconcile_interval: duration_or(
                    &mut get,
                    "KINUGASA_UPLOAD_RECONCILE_INTERVAL",
                    Duration::from_secs(1),
                )?,
                upload_batch_size: NonZeroU32::new(parse_or(
                    &mut get,
                    "KINUGASA_UPLOAD_BATCH_SIZE",
                    32,
                )?)
                .ok_or_else(|| {
                    ConfigError::invalid("KINUGASA_UPLOAD_BATCH_SIZE", "must be positive")
                })?,
            },
        })
    }
}

fn tls_identity(
    get: &mut impl FnMut(&str) -> Option<String>,
) -> Result<MoqTlsIdentity, ConfigError> {
    let certificate = non_empty(get("KINUGASA_MOQ_TLS_CERTIFICATE_CHAIN"));
    let private_key = non_empty(get("KINUGASA_MOQ_TLS_PRIVATE_KEY"));
    let self_signed = non_empty(get("KINUGASA_MOQ_SELF_SIGNED_HOSTNAMES"));
    match (certificate, private_key, self_signed) {
        (Some(certificate_chain), Some(private_key), None) => Ok(MoqTlsIdentity::Files {
            certificate_chain: certificate_chain.into(),
            private_key: private_key.into(),
        }),
        (None, None, Some(hostnames)) => {
            let hostnames = split_non_empty(&hostnames);
            if hostnames.is_empty() {
                return Err(ConfigError::invalid(
                    "KINUGASA_MOQ_SELF_SIGNED_HOSTNAMES",
                    "must contain at least one hostname",
                ));
            }
            Ok(MoqTlsIdentity::SelfSigned { hostnames })
        }
        _ => Err(ConfigError::invalid(
            "KINUGASA_MOQ_TLS_CERTIFICATE_CHAIN",
            "set both certificate and private-key paths, or set only KINUGASA_MOQ_SELF_SIGNED_HOSTNAMES",
        )),
    }
}

fn required(
    get: &mut impl FnMut(&str) -> Option<String>,
    name: &'static str,
) -> Result<String, ConfigError> {
    non_empty(get(name)).ok_or(ConfigError::Missing(name))
}

fn non_empty(value: Option<String>) -> Option<String> {
    value.filter(|value| !value.trim().is_empty())
}

fn parse_or<T>(
    get: &mut impl FnMut(&str) -> Option<String>,
    name: &'static str,
    default: T,
) -> Result<T, ConfigError>
where
    T: std::str::FromStr,
    T::Err: std::fmt::Display,
{
    match non_empty(get(name)) {
        Some(value) => value
            .parse()
            .map_err(|error| ConfigError::invalid(name, error)),
        None => Ok(default),
    }
}

fn duration_or(
    get: &mut impl FnMut(&str) -> Option<String>,
    name: &'static str,
    default: Duration,
) -> Result<Duration, ConfigError> {
    let duration = match non_empty(get(name)) {
        Some(value) => {
            humantime::parse_duration(&value).map_err(|error| ConfigError::invalid(name, error))?
        }
        None => default,
    };
    if duration.is_zero() {
        return Err(ConfigError::invalid(name, "must be positive"));
    }
    Ok(duration)
}

fn parse_url(name: &'static str, value: String) -> Result<Url, ConfigError> {
    Url::parse(&value).map_err(|error| ConfigError::invalid(name, error))
}

fn split_non_empty(value: &str) -> Vec<String> {
    value
        .split(',')
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect()
}

fn parse_ports(value: String) -> Result<Vec<u16>, ConfigError> {
    let mut ports = BTreeSet::new();
    for part in split_non_empty(&value) {
        if let Some((start, end)) = part.split_once('-') {
            let start = parse_port(start)?;
            let end = parse_port(end)?;
            if start > end {
                return Err(ConfigError::invalid(
                    "KINUGASA_RIST_PORTS",
                    format!("range starts after it ends: {part}"),
                ));
            }
            ports.extend(start..=end);
        } else {
            ports.insert(parse_port(&part)?);
        }
    }
    if ports.is_empty() {
        return Err(ConfigError::invalid(
            "KINUGASA_RIST_PORTS",
            "must contain at least one port",
        ));
    }
    Ok(ports.into_iter().collect())
}

fn parse_port(value: &str) -> Result<u16, ConfigError> {
    let port = value
        .trim()
        .parse::<u16>()
        .map_err(|error| ConfigError::invalid("KINUGASA_RIST_PORTS", error))?;
    if port == 0 {
        return Err(ConfigError::invalid(
            "KINUGASA_RIST_PORTS",
            "port zero is not valid",
        ));
    }
    Ok(port)
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;

    fn required_values() -> HashMap<String, String> {
        HashMap::from([
            (
                "DATABASE_URL".into(),
                "mysql://user:secret@db/kinugasa".into(),
            ),
            ("KINUGASA_S3_BUCKET".into(), "recordings".into()),
            ("KINUGASA_S3_REGION".into(), "ap-northeast-1".into()),
            (
                "KINUGASA_PREVIEW_ENDPOINT".into(),
                "https://recording.example.test/moq".into(),
            ),
            ("KINUGASA_GATEWAY_INSTANCE".into(), "gateway-1".into()),
            (
                "KINUGASA_RIST_PUBLIC_ENDPOINT".into(),
                "rist://recording.example.test".into(),
            ),
            ("KINUGASA_RIST_PORTS".into(), "9200-9202,9300".into()),
            (
                "KINUGASA_MOQ_SELF_SIGNED_HOSTNAMES".into(),
                "localhost,127.0.0.1".into(),
            ),
        ])
    }

    fn from(values: HashMap<String, String>) -> Result<AppConfig, ConfigError> {
        AppConfig::from_lookup(|name| values.get(name).cloned())
    }

    #[test]
    fn environment_is_converted_to_adapter_and_use_case_configuration() {
        let mut values = required_values();
        values.insert("KINUGASA_UPLOAD_BATCH_SIZE".into(), "64".into());
        values.insert("KINUGASA_PREVIEW_TOKEN_TTL".into(), "90s".into());

        let config = from(values).unwrap();

        assert_eq!(config.database.max_connections, 16);
        assert_eq!(config.rist.available_ports, vec![9200, 9201, 9202, 9300]);
        assert_eq!(config.preview_token_lifetime, Duration::from_secs(90));
        assert_eq!(config.runtime.upload_batch_size.get(), 64);
        assert_eq!(config.recording_layout.file_name(), "video.ts");
        assert_eq!(config.lockfile.schema_version(), "1.0");
        assert!(matches!(config.moq.tls, MoqTlsIdentity::SelfSigned { .. }));
    }

    #[test]
    fn database_url_is_redacted_from_debug_output() {
        let config = from(required_values()).unwrap();
        let debug = format!("{:?}", config.database);

        assert!(!debug.contains("secret"));
        assert!(debug.contains("[REDACTED]"));
    }

    #[test]
    fn tls_file_pair_must_be_complete_and_exclusive() {
        let mut values = required_values();
        values.remove("KINUGASA_MOQ_SELF_SIGNED_HOSTNAMES");
        values.insert(
            "KINUGASA_MOQ_TLS_CERTIFICATE_CHAIN".into(),
            "/tls/cert.pem".into(),
        );

        assert!(from(values).is_err());
    }

    #[test]
    fn zero_duration_is_rejected() {
        let mut values = required_values();
        values.insert("KINUGASA_UPLOAD_RECONCILE_INTERVAL".into(), "0s".into());

        assert!(from(values).is_err());
    }
}
