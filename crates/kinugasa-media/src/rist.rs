use std::{
    collections::{BTreeSet, HashMap, HashSet},
    net::IpAddr,
    sync::{Arc, Mutex, mpsc},
    thread::{self, JoinHandle},
    time::Duration,
};

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use hmac::{Hmac, Mac};
use kinugasa_core::domain::{AccessToken, CameraIdentityId, SessionId};
use rist_rs::{
    AuthenticationRequest, ConnectionStatus, DataBlock, Driver, DriverBuilder, EncryptionKeySize,
    LogHandler, LogLevel, PeerConfig, PeerInfo, Profile, ReceiverConfig, ReceiverHandle,
    ReceiverHandler, ReceiverHandlers, ReceiverStatistics,
};
use sha2::Sha256;
use thiserror::Error;
use url::Url;

use crate::{IngressError, MediaIngress, TransportStatistics, service::SessionIngressPacket};

const MINIMUM_ENCRYPTION_PEPPER_LENGTH: usize = 32;
const SESSION_SECRET_CONTEXT: &[u8] = b"kinugasa-recording/rist-session/v1";

#[derive(Clone)]
pub struct RistConfig {
    /// Local address on which every configured camera port is opened.
    pub listen_address: IpAddr,
    /// Camera-facing base URL. Its port is replaced by the allocated camera
    /// port and its query is extended with the session credential.
    pub public_endpoint: Url,
    /// Exclusive pool of physical UDP ports. One port is leased per active
    /// camera and returned when that camera is revoked.
    pub available_ports: Vec<u16>,
    /// Deployment-wide secret used to deterministically derive a credential
    /// for each session. It must remain stable across process restarts.
    pub encryption_pepper: String,
    pub recovery_buffer: Duration,
    pub reorder_buffer: Duration,
    pub statistics_interval: Duration,
}

impl std::fmt::Debug for RistConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("RistConfig")
            .field("listen_address", &self.listen_address)
            .field("public_endpoint", &self.public_endpoint)
            .field("available_ports", &self.available_ports)
            .field("encryption_pepper", &"[REDACTED]")
            .field("recovery_buffer", &self.recovery_buffer)
            .field("reorder_buffer", &self.reorder_buffer)
            .field("statistics_interval", &self.statistics_interval)
            .finish()
    }
}

impl RistConfig {
    fn validate(&self) -> Result<(), RistError> {
        if self.public_endpoint.scheme() != "rist" || self.public_endpoint.host_str().is_none() {
            return Err(RistError::InvalidConfiguration(
                "public_endpoint must be a rist:// URL with a host".into(),
            ));
        }
        if self.public_endpoint.query_pairs().any(|(key, _)| {
            matches!(
                key.as_ref(),
                "secret" | "aes-type" | "virt-dst-port" | "buffer"
            )
        }) {
            return Err(RistError::InvalidConfiguration(
                "public_endpoint must not contain managed RIST parameters".into(),
            ));
        }
        if self.available_ports.is_empty() || self.available_ports.contains(&0) {
            return Err(RistError::InvalidConfiguration(
                "available_ports must contain at least one nonzero port".into(),
            ));
        }
        if self.encryption_pepper.len() < MINIMUM_ENCRYPTION_PEPPER_LENGTH {
            return Err(RistError::InvalidConfiguration(format!(
                "encryption_pepper must contain at least {MINIMUM_ENCRYPTION_PEPPER_LENGTH} bytes"
            )));
        }
        let unique = self
            .available_ports
            .iter()
            .copied()
            .collect::<BTreeSet<_>>();
        if unique.len() != self.available_ports.len() {
            return Err(RistError::InvalidConfiguration(
                "available_ports must not contain duplicates".into(),
            ));
        }
        if self.recovery_buffer.is_zero() || self.reorder_buffer >= self.recovery_buffer {
            return Err(RistError::InvalidConfiguration(
                "recovery_buffer must be positive and larger than reorder_buffer".into(),
            ));
        }
        if self.statistics_interval.is_zero() {
            return Err(RistError::InvalidConfiguration(
                "statistics_interval must be positive".into(),
            ));
        }
        Ok(())
    }

    fn peer(&self, port: u16, secret: &AccessToken) -> Result<PeerConfig, RistError> {
        let listen_endpoint = match self.listen_address {
            IpAddr::V4(address) => format!("rist://@{address}:{port}"),
            IpAddr::V6(address) => format!("rist://@[{address}]:{port}"),
        };
        let mut peer = PeerConfig::parse(&listen_endpoint)
            .map_err(|error| RistError::Librist(error.to_string()))?;
        peer.set_recovery_length(self.recovery_buffer, self.recovery_buffer)
            .map_err(|error| RistError::Librist(error.to_string()))?;
        peer.set_reorder_buffer(self.reorder_buffer)
            .map_err(|error| RistError::Librist(error.to_string()))?;
        peer.set_encryption(EncryptionKeySize::Aes256, secret.expose_secret())
            .map_err(|error| RistError::Librist(error.to_string()))?;
        Ok(peer)
    }

    fn camera_endpoint(&self, port: u16, secret: &AccessToken) -> Result<Url, RistError> {
        let mut endpoint = self.public_endpoint.clone();
        endpoint.set_port(Some(port)).map_err(|()| {
            RistError::InvalidConfiguration("public_endpoint cannot carry a UDP port".into())
        })?;
        endpoint
            .query_pairs_mut()
            .append_pair("aes-type", "256")
            .append_pair("secret", secret.expose_secret())
            .append_pair("buffer", &self.recovery_buffer.as_millis().to_string());
        Ok(endpoint)
    }

    fn session_secret(&self, session_id: SessionId) -> AccessToken {
        let mut mac = Hmac::<Sha256>::new_from_slice(self.encryption_pepper.as_bytes())
            .expect("HMAC accepts keys of every length");
        mac.update(SESSION_SECRET_CONTEXT);
        mac.update(&[0]);
        mac.update(session_id.to_string().as_bytes());
        AccessToken::new(URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes()))
            .expect("an HMAC-derived credential is never empty")
    }
}

#[derive(Debug, Error)]
pub enum RistError {
    #[error("invalid RIST configuration: {0}")]
    InvalidConfiguration(String),
    #[error("no RIST camera port is available")]
    PortPoolExhausted,
    #[error("failed to start RIST driver thread: {0}")]
    DriverThread(#[source] std::io::Error),
    #[error("RIST driver stopped during startup")]
    DriverStartup,
    #[error("librist operation failed: {0}")]
    Librist(String),
    #[error("RIST driver thread panicked")]
    DriverPanicked,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RistCameraPublishAccess {
    pub(crate) endpoint: Url,
}

pub(crate) trait CameraTransport: Send + Sync {
    fn provision_camera(
        &self,
        session_id: SessionId,
        camera_id: CameraIdentityId,
    ) -> Result<RistCameraPublishAccess, RistError>;

    fn revoke_camera(&self, camera_id: CameraIdentityId) -> Result<(), RistError>;

    fn shutdown(self: Box<Self>) -> Result<(), RistError>;
}

struct CameraReceiver {
    session_id: SessionId,
    port: u16,
    endpoint: Url,
    receiver: ReceiverHandle,
}

struct RistState {
    available_ports: BTreeSet<u16>,
    cameras: HashMap<CameraIdentityId, CameraReceiver>,
}

pub struct RistServer {
    config: RistConfig,
    ingress: MediaIngress,
    state: Mutex<RistState>,
    driver: Option<Driver>,
    driver_thread: Option<JoinHandle<Result<(), rist_rs::Error>>>,
}

impl RistServer {
    pub fn start(config: RistConfig, ingress: MediaIngress) -> Result<Self, RistError> {
        config.validate()?;
        let available_ports = config.available_ports.iter().copied().collect();
        let (driver_sender, driver_receiver) = mpsc::sync_channel(1);
        let driver_thread = thread::Builder::new()
            .name("kinugasa-rist".into())
            .spawn(move || {
                DriverBuilder::new(move |driver| drop(driver_sender.send(driver))).start()
            })
            .map_err(RistError::DriverThread)?;
        let driver = driver_receiver
            .recv()
            .map_err(|_| RistError::DriverStartup)?;
        Ok(Self {
            config,
            ingress,
            state: Mutex::new(RistState {
                available_ports,
                cameras: HashMap::new(),
            }),
            driver: Some(driver),
            driver_thread: Some(driver_thread),
        })
    }

    pub fn shutdown(mut self) -> Result<(), RistError> {
        let receivers = {
            let state = self
                .state
                .get_mut()
                .unwrap_or_else(|value| value.into_inner());
            state
                .cameras
                .drain()
                .map(|(_, camera)| camera.receiver)
                .collect::<Vec<_>>()
        };
        let mut first_error = None;
        for receiver in receivers {
            if let Err(error) = receiver.close()
                && first_error.is_none()
            {
                first_error = Some(RistError::Librist(error.to_string()));
            }
        }
        if let Some(driver) = self.driver.take()
            && let Err(error) = driver.shutdown()
            && first_error.is_none()
        {
            first_error = Some(RistError::Librist(error.to_string()));
        }
        if let Some(thread) = self.driver_thread.take() {
            let result = thread
                .join()
                .map_err(|_| RistError::DriverPanicked)
                .and_then(|result| result.map_err(|error| RistError::Librist(error.to_string())));
            if let Err(error) = result
                && first_error.is_none()
            {
                first_error = Some(error);
            }
        }
        first_error.map_or(Ok(()), Err)
    }

    fn create_camera_receiver(
        &self,
        session_id: SessionId,
        port: u16,
        secret: &AccessToken,
    ) -> Result<ReceiverHandle, RistError> {
        let peer = self.config.peer(port, secret)?;
        let handler = Arc::new(RistReceiver::new(session_id, self.ingress.clone()));
        let handlers = ReceiverHandlers::new(handler).with_log(Arc::new(RistLog));
        self.driver
            .as_ref()
            .ok_or_else(|| RistError::Librist("RIST driver is shut down".into()))?
            .create_receiver_with_config(
                ReceiverConfig {
                    profile: Profile::Main,
                    peers: vec![peer],
                    statistics_interval: Some(self.config.statistics_interval),
                    log_level: LogLevel::Info,
                    ..ReceiverConfig::default()
                },
                handlers,
            )
            .map_err(|error| RistError::Librist(error.to_string()))
    }
}

impl CameraTransport for RistServer {
    fn provision_camera(
        &self,
        session_id: SessionId,
        camera_id: CameraIdentityId,
    ) -> Result<RistCameraPublishAccess, RistError> {
        let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
        if let Some(camera) = state.cameras.get(&camera_id) {
            if camera.session_id != session_id {
                return Err(RistError::InvalidConfiguration(
                    "camera is already provisioned for another session".into(),
                ));
            }
            return Ok(RistCameraPublishAccess {
                endpoint: camera.endpoint.clone(),
            });
        }

        let port = state
            .available_ports
            .first()
            .copied()
            .ok_or(RistError::PortPoolExhausted)?;
        let secret = self.config.session_secret(session_id);
        let endpoint = self.config.camera_endpoint(port, &secret)?;
        let receiver = self.create_camera_receiver(session_id, port, &secret)?;

        state.available_ports.remove(&port);
        state.cameras.insert(
            camera_id,
            CameraReceiver {
                session_id,
                port,
                endpoint: endpoint.clone(),
                receiver,
            },
        );
        Ok(RistCameraPublishAccess { endpoint })
    }

    fn revoke_camera(&self, camera_id: CameraIdentityId) -> Result<(), RistError> {
        let camera = {
            let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
            let Some(camera) = state.cameras.remove(&camera_id) else {
                return Ok(());
            };
            camera
        };
        let port = camera.port;
        let close_result = camera
            .receiver
            .close()
            .map_err(|error| RistError::Librist(error.to_string()));
        self.state
            .lock()
            .unwrap_or_else(|value| value.into_inner())
            .available_ports
            .insert(port);
        close_result
    }

    fn shutdown(self: Box<Self>) -> Result<(), RistError> {
        RistServer::shutdown(*self)
    }
}

impl Drop for RistServer {
    fn drop(&mut self) {
        self.state
            .get_mut()
            .unwrap_or_else(|value| value.into_inner())
            .cameras
            .clear();
        self.driver.take();
        // Dropping Driver requests shutdown. Do not block in Drop; deliberate
        // shutdown joins the thread and reports errors.
        self.driver_thread.take();
    }
}

struct RistReceiver {
    session_id: SessionId,
    ingress: MediaIngress,
    routes: Mutex<FlowRoutes>,
}

impl RistReceiver {
    fn new(session_id: SessionId, ingress: MediaIngress) -> Self {
        Self {
            session_id,
            ingress,
            routes: Mutex::new(FlowRoutes::default()),
        }
    }
}

#[derive(Default)]
struct FlowRoutes {
    ports_by_flow: HashMap<u32, HashSet<u16>>,
    port_by_flow: HashMap<u32, u16>,
    flow_by_port: HashMap<u16, u32>,
    collided_flows: HashSet<u32>,
    errored_ports: HashSet<u16>,
}

#[derive(Debug, PartialEq, Eq)]
enum RouteDecision {
    Accepted,
    Rejected,
    Collision {
        affected_ports: Vec<u16>,
        existing_port: Option<u16>,
        existing_flow: Option<u32>,
    },
}

impl FlowRoutes {
    fn observe(&mut self, flow_id: u32, virtual_port: u16) -> RouteDecision {
        self.ports_by_flow
            .entry(flow_id)
            .or_default()
            .insert(virtual_port);

        if self.collided_flows.contains(&flow_id) || self.errored_ports.contains(&virtual_port) {
            self.collided_flows.insert(flow_id);
            return if self.errored_ports.insert(virtual_port) {
                RouteDecision::Collision {
                    affected_ports: vec![virtual_port],
                    existing_port: self.port_by_flow.get(&flow_id).copied(),
                    existing_flow: self.flow_by_port.get(&virtual_port).copied(),
                }
            } else {
                RouteDecision::Rejected
            };
        }

        let existing_port = self.port_by_flow.get(&flow_id).copied();
        let existing_flow = self.flow_by_port.get(&virtual_port).copied();
        match (existing_port, existing_flow) {
            (None, None) => {
                self.port_by_flow.insert(flow_id, virtual_port);
                self.flow_by_port.insert(virtual_port, flow_id);
                RouteDecision::Accepted
            }
            (Some(port), Some(flow)) if port == virtual_port && flow == flow_id => {
                RouteDecision::Accepted
            }
            _ => {
                let mut affected_flows = HashSet::from([flow_id]);
                if let Some(flow) = existing_flow {
                    affected_flows.insert(flow);
                }
                self.collided_flows.extend(affected_flows.iter().copied());

                let mut affected_ports = HashSet::from([virtual_port]);
                if let Some(port) = existing_port {
                    affected_ports.insert(port);
                }
                for flow in affected_flows {
                    if let Some(ports) = self.ports_by_flow.get(&flow) {
                        affected_ports.extend(ports.iter().copied());
                    }
                }
                self.errored_ports.extend(affected_ports.iter().copied());
                let mut affected_ports = affected_ports.into_iter().collect::<Vec<_>>();
                affected_ports.sort_unstable();
                RouteDecision::Collision {
                    affected_ports,
                    existing_port,
                    existing_flow,
                }
            }
        }
    }

    fn statistics_port(&self, flow_id: u32) -> Option<u16> {
        if self.collided_flows.contains(&flow_id) {
            None
        } else {
            self.port_by_flow.get(&flow_id).copied()
        }
    }

    fn timeout(&mut self, flow_id: u32) -> Vec<u16> {
        self.collided_flows.remove(&flow_id);
        if let Some(port) = self.port_by_flow.remove(&flow_id)
            && self.flow_by_port.get(&port) == Some(&flow_id)
        {
            self.flow_by_port.remove(&port);
        }
        let removed_ports = self.ports_by_flow.remove(&flow_id).unwrap_or_default();
        let disconnected_ports = removed_ports
            .into_iter()
            .filter(|port| {
                !self
                    .ports_by_flow
                    .values()
                    .any(|ports| ports.contains(port))
            })
            .collect::<Vec<_>>();
        for port in &disconnected_ports {
            self.errored_ports.remove(port);
        }
        disconnected_ports
    }
}

impl ReceiverHandler for RistReceiver {
    fn handle_data(&self, data: DataBlock) {
        let virtual_port = data.virtual_destination_port();
        let flow_id = data.flow_id();
        let _decision = self
            .routes
            .lock()
            .unwrap_or_else(|value| value.into_inner())
            .observe(flow_id, virtual_port);
        // TODO: Re-enable this error handling when multiplexing multiple cameras
        // on one UDP port with virt-dst-port is restored.
        /* match _decision {
            RouteDecision::Accepted => {}
            RouteDecision::Rejected => return,
            RouteDecision::Collision {
                affected_ports,
                existing_port,
                existing_flow,
            } => {
                tracing::error!(
                    %self.session_id,
                    flow_id,
                    virtual_port,
                    ?existing_port,
                    ?existing_flow,
                    "RIST flow ID and virtual port collision"
                );
                for port in affected_ports {
                    let reason = kinugasa_core::domain::ErrorReason::new(format!(
                        "RIST flow ID {flow_id} conflicts with another camera in the session"
                    ))
                    .expect("the RIST collision reason is non-empty");
                    match self
                        .ingress
                        .error_session_route(self.session_id, port, reason)
                    {
                        Ok(()) | Err(IngressError::CameraNotFound) => {}
                        Err(error) => tracing::warn!(
                            %self.session_id,
                            flow_id,
                            virtual_port = port,
                            %error,
                            "failed to report RIST route collision"
                        ),
                    }
                }
                return;
            }
        } */
        let result = self.ingress.push_for_session_route(SessionIngressPacket {
            session_id: self.session_id,
            virtual_port,
            flow_id,
            ntp_timestamp: data.ntp_timestamp(),
            payload: Bytes::from(data.into_payload()),
        });
        match result {
            Ok(()) => {}
            Err(IngressError::QueueFull) => {
                tracing::warn!(%self.session_id, flow_id, virtual_port, "RIST ingress queue overflow");
            }
            Err(IngressError::CameraNotFound) => {
                tracing::debug!(
                    %self.session_id,
                    flow_id,
                    virtual_port,
                    "dropping data for unknown camera route"
                );
            }
            Err(IngressError::Unavailable) => {
                tracing::warn!(%self.session_id, flow_id, virtual_port, "camera media task is unavailable");
            }
        }
    }

    fn authenticate(&self, request: AuthenticationRequest) -> bool {
        tracing::info!(
            %self.session_id,
            remote = %request.remote_address,
            remote_port = request.remote_port,
            "accepted incoming RIST peer"
        );
        true
    }

    fn handle_connection_status(&self, peer: PeerInfo, status: ConnectionStatus) {
        tracing::info!(
            %self.session_id,
            peer_id = peer.id,
            ?status,
            "RIST peer status changed"
        );
    }

    fn handle_statistics(&self, statistics: ReceiverStatistics) {
        let flow_id = statistics.flow.flow_id;
        let virtual_port = self
            .routes
            .lock()
            .unwrap_or_else(|value| value.into_inner())
            .statistics_port(flow_id);
        if let Some(virtual_port) = virtual_port {
            let _ = self.ingress.observe_statistics_for_session_route(
                self.session_id,
                virtual_port,
                TransportStatistics {
                    flow_id,
                    // Sequence tracking records unrecovered gaps. Adding the
                    // librist lost counter here would count the same loss twice.
                    lost_packets: 0,
                    recovered_packets: u64::from(statistics.flow.recovered),
                },
            );
        }
    }

    fn handle_session_timeout(&self, flow_id: u32) {
        let disconnected_ports = self
            .routes
            .lock()
            .unwrap_or_else(|value| value.into_inner())
            .timeout(flow_id);
        for virtual_port in disconnected_ports {
            let _ = self
                .ingress
                .disconnected_session_route(self.session_id, virtual_port);
        }
    }
}

struct RistLog;

impl LogHandler for RistLog {
    fn handle_log(&self, level: LogLevel, message: &str) {
        let message = message.trim_end();
        match level {
            LogLevel::Error => tracing::error!(target: "librist", %message),
            LogLevel::Warning => tracing::warn!(target: "librist", %message),
            LogLevel::Debug | LogLevel::Simulate => tracing::debug!(target: "librist", %message),
            LogLevel::Disable => {}
            _ => tracing::info!(target: "librist", %message),
        }
    }
}

#[cfg(test)]
mod tests {
    use std::{net::Ipv4Addr, str::FromStr};

    use super::*;

    fn config() -> RistConfig {
        RistConfig {
            listen_address: IpAddr::V4(Ipv4Addr::UNSPECIFIED),
            public_endpoint: Url::parse("rist://recording.example.test").unwrap(),
            available_ports: vec![9_200, 9_201],
            encryption_pepper: "test-pepper-with-at-least-32-bytes".into(),
            recovery_buffer: Duration::from_secs(5),
            reorder_buffer: Duration::from_millis(200),
            statistics_interval: Duration::from_secs(5),
        }
    }

    #[test]
    fn camera_endpoint_has_session_port_and_redacts_generated_secret_from_config() {
        let config = config();
        let secret = AccessToken::new("do-not-log").unwrap();
        let endpoint = config.camera_endpoint(9_201, &secret).unwrap();
        let query = endpoint
            .query_pairs()
            .map(|(key, value)| (key.into_owned(), value.into_owned()))
            .collect::<HashMap<_, _>>();
        assert_eq!(endpoint.port(), Some(9_201));
        assert_eq!(
            query,
            HashMap::from([
                ("aes-type".to_owned(), "256".to_owned()),
                ("secret".to_owned(), "do-not-log".to_owned()),
                ("buffer".to_owned(), "5000".to_owned()),
            ])
        );
        assert!(!format!("{config:?}").contains("do-not-log"));
    }

    #[test]
    fn rejects_empty_duplicate_or_zero_port_pools() {
        for ports in [vec![], vec![9_200, 9_200], vec![0]] {
            let mut config = config();
            config.available_ports = ports;
            assert!(config.validate().is_err());
        }
    }

    #[test]
    fn derives_a_stable_session_secret_like_v2() {
        let config = config();
        let session_id = SessionId::from_str("019c240d-a6de-7de0-a826-0f26e8803fc0").unwrap();

        assert_eq!(
            config.session_secret(session_id).expose_secret(),
            "SiRUeJ8S7Pm3al4p5suRq-dsrlHD0l02unseAzrkbWY"
        );
        assert_eq!(
            config.session_secret(session_id),
            config.session_secret(session_id)
        );
        assert!(!format!("{config:?}").contains("test-pepper"));
    }

    #[test]
    fn rejects_a_short_encryption_pepper() {
        let mut config = config();
        config.encryption_pepper = "too-short".into();

        assert!(config.validate().is_err());
    }

    #[test]
    fn formats_ipv6_listener_for_librist() {
        let mut config = config();
        config.listen_address = IpAddr::from_str("::").unwrap();
        let secret = AccessToken::new("secret").unwrap();
        let peer = config.peer(9_200, &secret).unwrap();
        assert!(peer.address().to_string_lossy().contains("[::]:9200"));
    }

    #[test]
    fn binds_one_flow_to_one_virtual_port() {
        let mut routes = FlowRoutes::default();

        assert_eq!(routes.observe(42, 1_024), RouteDecision::Accepted);
        assert_eq!(routes.observe(42, 1_024), RouteDecision::Accepted);
        assert_eq!(routes.statistics_port(42), Some(1_024));
    }

    #[test]
    fn rejects_a_flow_id_reused_by_another_camera() {
        let mut routes = FlowRoutes::default();
        assert_eq!(routes.observe(42, 1_024), RouteDecision::Accepted);

        assert_eq!(
            routes.observe(42, 1_025),
            RouteDecision::Collision {
                affected_ports: vec![1_024, 1_025],
                existing_port: Some(1_024),
                existing_flow: None,
            }
        );
        assert_eq!(routes.statistics_port(42), None);
        assert_eq!(routes.observe(42, 1_024), RouteDecision::Rejected);
    }

    #[test]
    fn rejects_a_virtual_port_reused_by_another_flow() {
        let mut routes = FlowRoutes::default();
        assert_eq!(routes.observe(42, 1_024), RouteDecision::Accepted);

        assert_eq!(
            routes.observe(43, 1_024),
            RouteDecision::Collision {
                affected_ports: vec![1_024],
                existing_port: None,
                existing_flow: Some(42),
            }
        );
        assert_eq!(routes.statistics_port(42), None);
        assert_eq!(routes.statistics_port(43), None);
        assert_eq!(routes.observe(43, 1_024), RouteDecision::Rejected);
    }

    #[test]
    fn collision_quarantine_is_released_after_every_involved_flow_times_out() {
        let mut routes = FlowRoutes::default();
        assert_eq!(routes.observe(42, 1_024), RouteDecision::Accepted);
        assert!(matches!(
            routes.observe(43, 1_024),
            RouteDecision::Collision { .. }
        ));

        assert!(routes.timeout(43).is_empty());
        assert_eq!(routes.timeout(42), vec![1_024]);
        assert_eq!(routes.observe(44, 1_024), RouteDecision::Accepted);
    }
}
