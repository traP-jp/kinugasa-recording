use std::{
    collections::HashMap,
    net::SocketAddr,
    path::PathBuf,
    sync::{Arc, Mutex},
};

use async_trait::async_trait;
use chrono::{DateTime, Utc};
use kinugasa_core::domain::{AccessToken, CameraIdentityId, CameraName, SessionId};
use kinugasa_core::ports::ServerCertificateHash;
use moq_tokio::server::{Reject, Request};
use thiserror::Error;
use tokio::{
    sync::oneshot,
    task::{JoinHandle, JoinSet},
};
use url::{Url, form_urlencoded};
use uuid::Uuid;

use crate::PreviewSubscription;

/// TLS identity served by the WebTransport listener.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MoqTlsIdentity {
    /// A PEM certificate chain and its matching private key.
    Files {
        certificate_chain: PathBuf,
        private_key: PathBuf,
    },
    /// An ephemeral self-signed identity for local development and tests.
    /// Browsers require an out-of-band certificate fingerprint, so production
    /// deployments should use [`Self::Files`].
    SelfSigned { hostnames: Vec<String> },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MoqConfig {
    pub listen_address: SocketAddr,
    pub tls: MoqTlsIdentity,
}

impl MoqConfig {
    fn validate(&self) -> Result<(), MoqError> {
        match &self.tls {
            MoqTlsIdentity::Files {
                certificate_chain,
                private_key,
            } if certificate_chain.as_os_str().is_empty() || private_key.as_os_str().is_empty() => {
                Err(MoqError::InvalidConfiguration(
                    "TLS certificate and private-key paths must not be empty".into(),
                ))
            }
            MoqTlsIdentity::SelfSigned { hostnames } if hostnames.is_empty() => {
                Err(MoqError::InvalidConfiguration(
                    "at least one self-signed TLS hostname is required".into(),
                ))
            }
            MoqTlsIdentity::SelfSigned { hostnames }
                if hostnames.iter().any(|hostname| hostname.is_empty()) =>
            {
                Err(MoqError::InvalidConfiguration(
                    "self-signed TLS hostnames must not be empty".into(),
                ))
            }
            _ => Ok(()),
        }
    }
}

#[derive(Debug, Error)]
pub enum MoqError {
    #[error("invalid Media over QUIC configuration: {0}")]
    InvalidConfiguration(String),
    #[error("Media over QUIC camera publication conflicts with an existing publication")]
    Conflict,
    #[error("Media over QUIC transport failed: {0}")]
    Transport(#[from] moq_tokio::Error),
    #[error("Media over QUIC publication failed: {0}")]
    Publish(#[from] moq_net::Error),
    #[error("Media over QUIC listener stopped unexpectedly")]
    ListenerStopped,
    #[error("Media over QUIC task panicked")]
    TaskPanicked,
}

#[derive(Clone)]
pub(crate) struct PreviewAuthorizer {
    grants: Arc<Mutex<HashMap<String, PreviewGrant>>>,
}

#[derive(Clone, Copy)]
struct PreviewGrant {
    session_id: SessionId,
    expires_at: DateTime<Utc>,
}

impl PreviewAuthorizer {
    pub(crate) fn new() -> Self {
        Self {
            grants: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    pub(crate) fn issue(
        &self,
        session_id: SessionId,
        valid_for: std::time::Duration,
    ) -> Result<(AccessToken, DateTime<Utc>), MoqError> {
        let lifetime = chrono::Duration::from_std(valid_for).map_err(|error| {
            MoqError::InvalidConfiguration(format!("preview token lifetime is invalid: {error}"))
        })?;
        let expires_at = Utc::now().checked_add_signed(lifetime).ok_or_else(|| {
            MoqError::InvalidConfiguration("preview token expiry overflows the timestamp".into())
        })?;
        let secret = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
        let token = AccessToken::new(secret.clone()).map_err(|error| {
            MoqError::InvalidConfiguration(format!("generated preview token is invalid: {error}"))
        })?;
        let mut grants = self
            .grants
            .lock()
            .unwrap_or_else(|value| value.into_inner());
        grants.retain(|_, grant| grant.expires_at > Utc::now());
        grants.insert(
            secret,
            PreviewGrant {
                session_id,
                expires_at,
            },
        );
        Ok((token, expires_at))
    }

    pub(crate) fn authorize(&self, session_id: SessionId, token: &str) -> Option<DateTime<Utc>> {
        let now = Utc::now();
        let mut grants = self
            .grants
            .lock()
            .unwrap_or_else(|value| value.into_inner());
        grants.retain(|_, grant| grant.expires_at > now);
        grants
            .get(token)
            .filter(|grant| grant.session_id == session_id)
            .map(|grant| grant.expires_at)
    }
}

#[async_trait]
pub(crate) trait PreviewTransport: Send + Sync {
    fn server_certificate_hashes(&self) -> Vec<ServerCertificateHash>;

    fn provision_session(&self, session_id: SessionId);

    fn provision_camera(
        &self,
        session_id: SessionId,
        camera_id: CameraIdentityId,
        camera_name: &CameraName,
        subscription: PreviewSubscription,
    ) -> Result<(), MoqError>;

    fn revoke_camera(&self, camera_id: CameraIdentityId);

    async fn shutdown(self: Box<Self>) -> Result<(), MoqError>;
}

pub(crate) struct MoqServer {
    state: Arc<Mutex<MoqState>>,
    server_certificate_hashes: Vec<ServerCertificateHash>,
    shutdown: Option<oneshot::Sender<()>>,
    listener_task: Option<JoinHandle<Result<(), MoqError>>>,
    #[cfg(test)]
    local_addr: SocketAddr,
}

struct MoqState {
    sessions: HashMap<SessionId, PreviewSession>,
}

struct PreviewSession {
    origin: moq_net::origin::Producer,
    cameras: HashMap<CameraIdentityId, CameraPublication>,
}

struct CameraPublication {
    name: CameraName,
    stop: Option<oneshot::Sender<()>>,
    task: Option<JoinHandle<()>>,
}

impl Drop for CameraPublication {
    fn drop(&mut self) {
        if let Some(stop) = self.stop.take() {
            let _ = stop.send(());
        }
    }
}

impl MoqServer {
    pub(crate) async fn start(
        config: MoqConfig,
        public_endpoint: &Url,
        authorizer: PreviewAuthorizer,
    ) -> Result<Self, MoqError> {
        config.validate()?;
        let endpoint_path = endpoint_prefix(public_endpoint)?;
        let uses_self_signed_certificate = matches!(&config.tls, MoqTlsIdentity::SelfSigned { .. });
        let mut listen = moq_tokio::listen::Config::default();
        listen.bind = Some(moq_tokio::listen::Bind::Addr(config.listen_address));
        match config.tls {
            MoqTlsIdentity::Files {
                certificate_chain,
                private_key,
            } => {
                listen.tls.cert = vec![certificate_chain];
                listen.tls.key = vec![private_key];
            }
            MoqTlsIdentity::SelfSigned { hostnames } => {
                listen.tls.generate = hostnames;
            }
        }
        let server = listen.init(Default::default())?;
        let server_certificate_hashes = if uses_self_signed_certificate {
            server
                .certificates()
                .fingerprints()
                .into_iter()
                .map(|fingerprint| {
                    ServerCertificateHash::from_hex(&fingerprint).map_err(|error| {
                        MoqError::InvalidConfiguration(format!(
                            "generated certificate fingerprint is invalid: {error}"
                        ))
                    })
                })
                .collect::<Result<Vec<_>, _>>()?
        } else {
            Vec::new()
        };
        let listener = server.listen().await?;
        #[cfg(test)]
        let local_addr = listener.local_addr()?;
        let state = Arc::new(Mutex::new(MoqState {
            sessions: HashMap::new(),
        }));
        let (shutdown, shutdown_receiver) = oneshot::channel();
        let listener_task = tokio::spawn(run_listener(
            listener,
            Arc::clone(&state),
            authorizer,
            endpoint_path,
            shutdown_receiver,
        ));
        Ok(Self {
            state,
            server_certificate_hashes,
            shutdown: Some(shutdown),
            listener_task: Some(listener_task),
            #[cfg(test)]
            local_addr,
        })
    }

    #[must_use]
    #[cfg(test)]
    pub(crate) const fn local_addr(&self) -> SocketAddr {
        self.local_addr
    }

    pub async fn shutdown(mut self) -> Result<(), MoqError> {
        if let Some(shutdown) = self.shutdown.take() {
            let _ = shutdown.send(());
        }
        let publications = {
            let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
            state
                .sessions
                .drain()
                .flat_map(|(_, session)| session.cameras.into_values())
                .collect::<Vec<_>>()
        };
        for mut publication in publications {
            if let Some(stop) = publication.stop.take() {
                let _ = stop.send(());
            }
            if let Some(task) = publication.task.take() {
                let _ = task.await;
            }
        }
        match self.listener_task.take() {
            Some(task) => task.await.map_err(|_| MoqError::TaskPanicked)?,
            None => Ok(()),
        }
    }
}

#[async_trait]
impl PreviewTransport for MoqServer {
    fn server_certificate_hashes(&self) -> Vec<ServerCertificateHash> {
        self.server_certificate_hashes.clone()
    }

    fn provision_session(&self, session_id: SessionId) {
        let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
        state
            .sessions
            .entry(session_id)
            .or_insert_with(|| PreviewSession {
                origin: moq_tokio::origin::spawn(),
                cameras: HashMap::new(),
            });
    }

    fn provision_camera(
        &self,
        session_id: SessionId,
        camera_id: CameraIdentityId,
        camera_name: &CameraName,
        mut subscription: PreviewSubscription,
    ) -> Result<(), MoqError> {
        let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
        let session = state
            .sessions
            .entry(session_id)
            .or_insert_with(|| PreviewSession {
                origin: moq_tokio::origin::spawn(),
                cameras: HashMap::new(),
            });
        if let Some(existing) = session.cameras.get(&camera_id) {
            if &existing.name != camera_name {
                return Err(MoqError::Conflict);
            }
            if existing
                .task
                .as_ref()
                .is_some_and(|task| !task.is_finished())
            {
                return Ok(());
            }
            session.cameras.remove(&camera_id);
        }
        if session
            .cameras
            .values()
            .any(|publication| &publication.name == camera_name)
        {
            return Err(MoqError::Conflict);
        }

        let broadcast = session.origin.create_broadcast(camera_name.as_str())?;
        let mut track = broadcast.create_track(
            "mpegts",
            moq_net::track::Info::default().with_max_age(std::time::Duration::from_secs(5)),
        )?;
        broadcast.announce(Default::default())?;
        let (stop, stop_receiver) = oneshot::channel();
        let task = tokio::spawn(async move {
            if let Err(error) =
                publish_camera(&mut subscription, &mut track, broadcast, stop_receiver).await
            {
                tracing::warn!(%camera_id, %error, "Media over QUIC camera publication stopped");
            }
        });
        session.cameras.insert(
            camera_id,
            CameraPublication {
                name: camera_name.clone(),
                stop: Some(stop),
                task: Some(task),
            },
        );
        Ok(())
    }

    fn revoke_camera(&self, camera_id: CameraIdentityId) {
        let mut state = self.state.lock().unwrap_or_else(|value| value.into_inner());
        for session in state.sessions.values_mut() {
            if session.cameras.remove(&camera_id).is_some() {
                break;
            }
        }
    }

    async fn shutdown(self: Box<Self>) -> Result<(), MoqError> {
        MoqServer::shutdown(*self).await
    }
}

impl Drop for MoqServer {
    fn drop(&mut self) {
        if let Some(shutdown) = self.shutdown.take() {
            let _ = shutdown.send(());
        }
        if let Some(task) = self.listener_task.take() {
            task.abort();
        }
    }
}

async fn publish_camera(
    subscription: &mut PreviewSubscription,
    track: &mut moq_net::track::Producer,
    broadcast: moq_net::broadcast::Producer,
    mut stop: oneshot::Receiver<()>,
) -> Result<(), String> {
    let mut group: Option<moq_net::group::Producer> = None;
    let mut group_bytes = 0usize;
    let mut group_frames = 0usize;
    let mut rotation = tokio::time::interval(std::time::Duration::from_millis(100));
    rotation.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            _ = &mut stop => break,
            _ = rotation.tick() => {
                if let Some(group) = group.take() {
                    group.finish().map_err(|error| error.to_string())?;
                }
                group_bytes = 0;
                group_frames = 0;
            }
            packet = subscription.recv() => {
                match packet {
                    Ok(packet) => {
                        let payload_len = packet.payload.len();
                        if group.is_some() && (group_bytes.saturating_add(payload_len) > 1024 * 1024 || group_frames >= 256) {
                            if let Some(previous) = group.take() {
                                previous.finish().map_err(|error| error.to_string())?;
                            }
                            group_bytes = 0;
                            group_frames = 0;
                        }
                        let group = match group.as_mut() {
                            Some(group) => group,
                            None => group.insert(track.append_group().map_err(|error| error.to_string())?),
                        };
                        group.write_frame(moq_net::Timestamp::now(), packet.payload)
                            .map_err(|error| error.to_string())?;
                        group_bytes += payload_len;
                        group_frames += 1;
                    }
                    Err(crate::PreviewReceiveError::Lagged(count)) => {
                        tracing::warn!(count, "Media over QUIC publisher skipped lagged ingress packets");
                    }
                    Err(crate::PreviewReceiveError::Closed) => break,
                }
            }
        }
    }
    if let Some(group) = group {
        group.finish().map_err(|error| error.to_string())?;
    }
    track.finish().map_err(|error| error.to_string())?;
    broadcast.finish();
    Ok(())
}

async fn run_listener(
    mut listener: moq_tokio::Listener,
    state: Arc<Mutex<MoqState>>,
    authorizer: PreviewAuthorizer,
    endpoint_path: String,
    mut shutdown: oneshot::Receiver<()>,
) -> Result<(), MoqError> {
    let mut sessions = JoinSet::new();
    loop {
        tokio::select! {
            _ = &mut shutdown => break,
            request = listener.accept() => {
                let Some(request) = request else {
                    return Err(MoqError::ListenerStopped);
                };
                let state = Arc::clone(&state);
                let authorizer = authorizer.clone();
                let endpoint_path = endpoint_path.clone();
                sessions.spawn(async move {
                    if let Err(error) = serve_request(request, state, authorizer, &endpoint_path).await {
                        tracing::debug!(%error, "Media over QUIC subscriber session ended");
                    }
                });
            }
            Some(result) = sessions.join_next(), if !sessions.is_empty() => {
                if let Err(error) = result {
                    tracing::warn!(%error, "Media over QUIC subscriber task panicked");
                }
            }
        }
    }
    listener.close().await;
    while let Some(result) = sessions.join_next().await {
        if let Err(error) = result {
            tracing::warn!(%error, "Media over QUIC subscriber task panicked during shutdown");
        }
    }
    Ok(())
}

async fn serve_request(
    request: Request,
    state: Arc<Mutex<MoqState>>,
    authorizer: PreviewAuthorizer,
    endpoint_path: &str,
) -> Result<(), MoqError> {
    if request.role() == Some(moq_net::Role::Publisher) {
        request.reject(Reject::Forbidden).await?;
        return Ok(());
    }
    let Some(session_id) = parse_session_path(request.path(), endpoint_path) else {
        request.reject(Reject::Forbidden).await?;
        return Ok(());
    };
    let token = request.query().and_then(|query| {
        form_urlencoded::parse(query.as_bytes())
            .find_map(|(key, value)| (key == "jwt").then(|| value.into_owned()))
    });
    let Some(expires_at) = token
        .as_deref()
        .and_then(|token| authorizer.authorize(session_id, token))
    else {
        request.reject(Reject::Unauthorized).await?;
        return Ok(());
    };
    let origin = {
        let state = state.lock().unwrap_or_else(|value| value.into_inner());
        state
            .sessions
            .get(&session_id)
            .map(|session| session.origin.consume())
    };
    let Some(origin) = origin else {
        request.reject(Reject::Forbidden).await?;
        return Ok(());
    };
    let session = request.with_publisher(origin).ok().await?;
    let remaining = expires_at
        .signed_duration_since(Utc::now())
        .to_std()
        .unwrap_or_default();
    tokio::select! {
        error = session.closed() => tracing::debug!(%error, "Media over QUIC subscriber disconnected"),
        _ = tokio::time::sleep(remaining) => session.abort(moq_net::Error::Unauthorized),
    }
    Ok(())
}

fn endpoint_prefix(endpoint: &Url) -> Result<String, MoqError> {
    if endpoint.scheme() != "https" || endpoint.host_str().is_none() {
        return Err(MoqError::InvalidConfiguration(
            "preview endpoint must be an https:// URL with a host".into(),
        ));
    }
    if endpoint.query().is_some() || endpoint.fragment().is_some() {
        return Err(MoqError::InvalidConfiguration(
            "preview endpoint must not contain a query or fragment".into(),
        ));
    }
    let path = endpoint.path().trim_end_matches('/');
    Ok(if path.is_empty() {
        String::new()
    } else {
        path.to_owned()
    })
}

fn parse_session_path(path: &str, prefix: &str) -> Option<SessionId> {
    let suffix = path.strip_prefix(prefix)?.strip_prefix('/')?;
    if suffix.contains('/') {
        return None;
    }
    suffix.parse().ok()
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use kinugasa_core::domain::CameraName;

    use super::*;

    #[test]
    fn session_paths_are_exactly_scoped_below_the_endpoint() {
        let session_id = SessionId::new_v7();
        assert_eq!(
            parse_session_path(&format!("/moq/{session_id}"), "/moq"),
            Some(session_id)
        );
        assert_eq!(
            parse_session_path(&format!("/other/{session_id}"), "/moq"),
            None
        );
        assert_eq!(
            parse_session_path(&format!("/moq/{session_id}/camera"), "/moq"),
            None
        );
    }

    #[test]
    fn grants_are_scoped_to_one_session_and_expire() {
        let authorizer = PreviewAuthorizer::new();
        let first = SessionId::new_v7();
        let second = SessionId::new_v7();
        let (token, _) = authorizer
            .issue(first, std::time::Duration::from_secs(1))
            .unwrap();
        assert!(authorizer.authorize(first, token.expose_secret()).is_some());
        assert!(
            authorizer
                .authorize(second, token.expose_secret())
                .is_none()
        );
    }

    #[tokio::test]
    async fn authenticated_subscriber_only_sees_its_session_origin() {
        let authorizer = PreviewAuthorizer::new();
        let endpoint = Url::parse("https://localhost/moq").unwrap();
        let server = MoqServer::start(
            MoqConfig {
                listen_address: "127.0.0.1:0".parse().unwrap(),
                tls: MoqTlsIdentity::SelfSigned {
                    hostnames: vec!["localhost".into()],
                },
            },
            &endpoint,
            authorizer.clone(),
        )
        .await
        .unwrap();
        let certificate_hashes = server.server_certificate_hashes();
        assert_eq!(certificate_hashes.len(), 1);
        assert_eq!(certificate_hashes[0].to_hex().len(), 64);
        let session_id = SessionId::new_v7();
        let camera_id = CameraIdentityId::new_v7();
        let camera_name = CameraName::new("front").unwrap();
        let (packets, receiver) = tokio::sync::broadcast::channel(4);
        server
            .provision_camera(
                session_id,
                camera_id,
                &camera_name,
                PreviewSubscription::new(receiver),
            )
            .unwrap();
        let (token, _) = authorizer
            .issue(session_id, Duration::from_secs(30))
            .unwrap();

        let denied_subscriber = moq_tokio::origin::spawn();
        let mut denied_config = moq_tokio::connect::Config::default();
        denied_config.bind = Some("127.0.0.1:0".parse().unwrap());
        denied_config.tls.fingerprint = certificate_hashes
            .iter()
            .copied()
            .map(|hash| hash.to_hex())
            .collect();
        let denied_client = denied_config
            .init(Default::default())
            .unwrap()
            .with_subscriber(denied_subscriber)
            .with_reconnect(false);
        let denied_url = Url::parse(&format!(
            "https://localhost:{}/moq/{session_id}?jwt=invalid",
            server.local_addr().port()
        ))
        .unwrap();
        let denied_connection = tokio::time::timeout(
            Duration::from_secs(5),
            denied_client.connect(denied_url).established(),
        )
        .await
        .expect("unauthorized MoQ connection timed out")
        .expect("WebTransport handshake failed before MoQ authorization");
        let error = tokio::time::timeout(Duration::from_secs(5), denied_connection.closed())
            .await
            .expect("unauthorized MoQ session did not close")
            .expect_err("unauthorized MoQ session was accepted");
        assert_eq!(
            error.connect_error(),
            Some(moq_tokio::ConnectError::Unauthorized)
        );

        let subscriber = moq_tokio::origin::spawn();
        let consumer = subscriber.consume();
        let mut announcements = consumer.announced();
        let mut client_config = moq_tokio::connect::Config::default();
        client_config.bind = Some("127.0.0.1:0".parse().unwrap());
        client_config.tls.fingerprint = certificate_hashes
            .iter()
            .copied()
            .map(|hash| hash.to_hex())
            .collect();
        let client = client_config
            .init(Default::default())
            .unwrap()
            .with_subscriber(subscriber)
            .with_reconnect(false);
        let url = Url::parse(&format!(
            "https://localhost:{}/moq/{session_id}?jwt={}",
            server.local_addr().port(),
            token.expose_secret()
        ))
        .unwrap();
        let connection =
            tokio::time::timeout(Duration::from_secs(5), client.connect(url).established())
                .await
                .expect("MoQ connection timed out")
                .expect("MoQ connection failed");
        let announcement = tokio::time::timeout(Duration::from_secs(5), announcements.next())
            .await
            .expect("MoQ announcement timed out")
            .expect("subscriber origin closed");
        assert_eq!(announcement.prefix.as_str(), camera_name.as_str());
        assert!(announcement.kind.is_active());
        let broadcast = tokio::time::timeout(
            Duration::from_secs(5),
            consumer.request_broadcast(camera_name.as_str()),
        )
        .await
        .expect("broadcast request timed out")
        .expect("camera broadcast did not resolve");
        let payload = bytes::Bytes::from_static(b"raw transport-stream payload (not parsed)");
        let next_payload = bytes::Bytes::from_static(b"second raw payload");
        packets
            .send(crate::PreviewPacket {
                camera_identity_id: camera_id,
                ntp_timestamp: 90_000,
                payload: payload.clone(),
            })
            .unwrap();
        packets
            .send(crate::PreviewPacket {
                camera_identity_id: camera_id,
                ntp_timestamp: 93_000,
                payload: next_payload.clone(),
            })
            .unwrap();
        let mut stream = broadcast
            .track("mpegts")
            .unwrap()
            .subscribe(None)
            .await
            .unwrap();
        let mut group = tokio::time::timeout(Duration::from_secs(5), stream.recv_group())
            .await
            .expect("MPEG-TS group timed out")
            .expect("MPEG-TS track failed")
            .expect("MPEG-TS track ended");
        let frame = tokio::time::timeout(Duration::from_secs(5), group.read_frame())
            .await
            .expect("MPEG-TS frame timed out")
            .expect("MPEG-TS group failed")
            .expect("MPEG-TS group ended");
        assert_eq!(frame.payload, payload);
        let second = tokio::time::timeout(Duration::from_secs(5), async {
            match group.read_frame().await? {
                Some(frame) => Ok(Some(frame)),
                None => {
                    let mut next_group = stream.recv_group().await?.expect("MPEG-TS track ended");
                    next_group.read_frame().await
                }
            }
        })
        .await
        .expect("second MPEG-TS frame timed out")
        .expect("second MPEG-TS group failed")
        .expect("second MPEG-TS frame missing");
        assert_eq!(second.payload, next_payload);

        drop(connection);
        server.shutdown().await.unwrap();
    }
}
