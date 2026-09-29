use std::{
    collections::HashMap,
    sync::{Arc, RwLock},
};

use async_trait::async_trait;
use bytes::Bytes;
use kinugasa_core::{
    domain::{AccessToken, CameraIdentityId, FinalizedRecording, SessionId, SessionName, TakeId},
    ports::{
        CameraIngress, CameraPublishAccess, MediaError, MediaEvent, MediaEventSource,
        PreviewAccess, PreviewAccessRequest, PreviewService, ProvisionCameraRequest,
        RecordingService, RecordingStarted, RistStatisticsSnapshot, RistStatisticsSource,
        StartRecordingRequest,
    },
};
use tokio::sync::{broadcast, mpsc};
use url::Url;

use crate::{
    IngressError, MediaBuildError, MediaConfig, MediaShutdownError, MoqConfig, MoqError,
    camera::{CameraHandle, CameraMetadata},
    moq::{MoqServer, PreviewAuthorizer, PreviewTransport},
    rist::{CameraTransport, RistConfig, RistServer},
};

const FIRST_VIRTUAL_PORT: u16 = 1_024;
const LAST_VIRTUAL_PORT: u16 = u16::MAX - 1;

#[derive(Debug, Clone)]
pub struct IngressPacket {
    pub camera_identity_id: CameraIdentityId,
    pub flow_id: u32,
    pub ntp_timestamp: u64,
    pub payload: Bytes,
}

pub(crate) struct SessionIngressPacket {
    pub(crate) session_id: SessionId,
    pub(crate) virtual_port: u16,
    pub(crate) flow_id: u32,
    pub(crate) ntp_timestamp: u64,
    pub(crate) payload: Bytes,
}

#[derive(Debug, Clone)]
pub struct PreviewPacket {
    pub camera_identity_id: CameraIdentityId,
    pub ntp_timestamp: u64,
    pub payload: Bytes,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TransportStatistics {
    pub flow_id: u32,
    pub lost_packets: u64,
    pub recovered_packets: u64,
}

#[derive(Debug, thiserror::Error)]
pub enum PreviewReceiveError {
    #[error("preview producer stopped")]
    Closed,
    #[error("preview subscriber fell behind by {0} packets")]
    Lagged(u64),
}

pub struct PreviewSubscription {
    receiver: broadcast::Receiver<PreviewPacket>,
}

impl PreviewSubscription {
    pub(crate) fn new(receiver: broadcast::Receiver<PreviewPacket>) -> Self {
        Self { receiver }
    }

    pub async fn recv(&mut self) -> Result<PreviewPacket, PreviewReceiveError> {
        self.receiver.recv().await.map_err(|error| match error {
            broadcast::error::RecvError::Closed => PreviewReceiveError::Closed,
            broadcast::error::RecvError::Lagged(count) => PreviewReceiveError::Lagged(count),
        })
    }
}

pub struct MediaService {
    inner: Arc<Inner>,
    events: tokio::sync::Mutex<mpsc::UnboundedReceiver<MediaEvent>>,
    rist: Box<dyn CameraTransport>,
    moq: Box<dyn PreviewTransport>,
}

#[derive(Clone)]
pub struct MediaIngress {
    inner: Arc<Inner>,
}

struct Inner {
    config: MediaConfig,
    cameras: RwLock<CameraRegistry>,
    preview_authorizer: PreviewAuthorizer,
    event_sender: mpsc::UnboundedSender<MediaEvent>,
}

#[derive(Default)]
struct CameraRegistry {
    by_id: HashMap<CameraIdentityId, Arc<CameraHandle>>,
    sessions: HashMap<SessionId, SessionRoutes>,
}

struct SessionRoutes {
    session_name: SessionName,
    by_virtual_port: HashMap<u16, CameraIdentityId>,
    next_virtual_port: u16,
}

impl MediaService {
    pub async fn new(
        config: MediaConfig,
        rist_config: RistConfig,
        moq_config: MoqConfig,
    ) -> Result<Self, MediaBuildError> {
        let (inner, events) = Self::prepare(config).await?;
        let moq = MoqServer::start(
            moq_config,
            &inner.config.preview_endpoint,
            inner.preview_authorizer.clone(),
        )
        .await?;
        let rist = RistServer::start(
            rist_config,
            MediaIngress {
                inner: Arc::clone(&inner),
            },
        )?;
        Ok(Self {
            inner,
            events: tokio::sync::Mutex::new(events),
            rist: Box::new(rist),
            moq: Box::new(moq),
        })
    }

    async fn prepare(
        mut config: MediaConfig,
    ) -> Result<(Arc<Inner>, mpsc::UnboundedReceiver<MediaEvent>), MediaBuildError> {
        config.validate()?;
        tokio::fs::create_dir_all(&config.recording_root)
            .await
            .map_err(MediaBuildError::RecordingRoot)?;
        config.recording_root = tokio::fs::canonicalize(&config.recording_root)
            .await
            .map_err(MediaBuildError::RecordingRoot)?;
        let (event_sender, events) = mpsc::unbounded_channel();
        Ok((
            Arc::new(Inner {
                config,
                cameras: RwLock::new(CameraRegistry::default()),
                preview_authorizer: PreviewAuthorizer::new(),
                event_sender,
            }),
            events,
        ))
    }

    #[must_use]
    pub fn ingress(&self) -> MediaIngress {
        MediaIngress {
            inner: Arc::clone(&self.inner),
        }
    }

    pub fn subscribe_preview(
        &self,
        access_token: &AccessToken,
        camera_id: CameraIdentityId,
    ) -> Result<PreviewSubscription, MediaError> {
        let camera = self.inner.camera(camera_id)?;
        self.inner
            .preview_authorizer
            .authorize(camera.metadata.session_id, access_token.expose_secret())
            .ok_or(MediaError::NotFound)?;
        Ok(PreviewSubscription::new(camera.subscribe()))
    }

    /// Stops the RIST and WebTransport listeners and drains their tasks.
    pub async fn shutdown(self) -> Result<(), MediaShutdownError> {
        let rist = self.rist.shutdown();
        let moq = self.moq.shutdown().await;
        match (rist, moq) {
            (Ok(()), Ok(())) => Ok(()),
            (Err(rist), Ok(())) => Err(MediaShutdownError::Rist(rist)),
            (Ok(()), Err(moq)) => Err(MediaShutdownError::Moq(moq)),
            (Err(rist), Err(moq)) => Err(MediaShutdownError::Both { rist, moq }),
        }
    }
}

impl MediaIngress {
    pub fn push(&self, packet: IngressPacket) -> Result<(), IngressError> {
        self.inner
            .camera_ingress(packet.camera_identity_id)?
            .ingest(packet)
    }

    pub(crate) fn push_for_session_route(
        &self,
        packet: SessionIngressPacket,
    ) -> Result<(), IngressError> {
        let camera = self
            .inner
            .camera_for_session_route(packet.session_id, packet.virtual_port)?;
        camera.ingest(IngressPacket {
            camera_identity_id: camera.metadata.camera_id,
            flow_id: packet.flow_id,
            ntp_timestamp: packet.ntp_timestamp,
            payload: packet.payload,
        })
    }

    pub fn disconnected(&self, camera_id: CameraIdentityId) -> Result<(), IngressError> {
        self.inner.camera_ingress(camera_id)?.disconnected()
    }

    pub fn disconnected_session_route(
        &self,
        session_id: SessionId,
        virtual_port: u16,
    ) -> Result<(), IngressError> {
        self.inner
            .camera_for_session_route(session_id, virtual_port)?
            .disconnected()
    }

    // Retained for the flow-collision error path that will be restored with
    // virt-dst-port multiplexing.
    #[allow(dead_code)]
    pub(crate) fn error_session_route(
        &self,
        session_id: SessionId,
        virtual_port: u16,
        reason: kinugasa_core::domain::ErrorReason,
    ) -> Result<(), IngressError> {
        self.inner
            .camera_for_session_route(session_id, virtual_port)?
            .input_error(reason)
    }

    pub fn observe_statistics(
        &self,
        camera_id: CameraIdentityId,
        statistics: TransportStatistics,
    ) -> Result<(), IngressError> {
        self.inner
            .camera_ingress(camera_id)?
            .observe_statistics(statistics);
        Ok(())
    }

    pub fn observe_statistics_for_session_route(
        &self,
        session_id: SessionId,
        virtual_port: u16,
        statistics: TransportStatistics,
    ) -> Result<(), IngressError> {
        self.inner
            .camera_for_session_route(session_id, virtual_port)?
            .observe_statistics(statistics);
        Ok(())
    }
}

impl Inner {
    fn camera(&self, camera_id: CameraIdentityId) -> Result<Arc<CameraHandle>, MediaError> {
        self.cameras
            .read()
            .unwrap_or_else(|value| value.into_inner())
            .by_id
            .get(&camera_id)
            .cloned()
            .ok_or(MediaError::NotFound)
    }

    fn camera_ingress(
        &self,
        camera_id: CameraIdentityId,
    ) -> Result<Arc<CameraHandle>, IngressError> {
        self.cameras
            .read()
            .unwrap_or_else(|value| value.into_inner())
            .by_id
            .get(&camera_id)
            .cloned()
            .ok_or(IngressError::CameraNotFound)
    }

    fn camera_for_session_route(
        &self,
        session_id: SessionId,
        virtual_port: u16,
    ) -> Result<Arc<CameraHandle>, IngressError> {
        let cameras = self
            .cameras
            .read()
            .unwrap_or_else(|value| value.into_inner());
        let camera_id = cameras
            .sessions
            .get(&session_id)
            .and_then(|session| session.by_virtual_port.get(&virtual_port))
            .ok_or(IngressError::CameraNotFound)?;
        cameras
            .by_id
            .get(camera_id)
            .cloned()
            .ok_or(IngressError::CameraNotFound)
    }

    fn camera_snapshot(&self) -> Vec<Arc<CameraHandle>> {
        self.cameras
            .read()
            .unwrap_or_else(|value| value.into_inner())
            .by_id
            .values()
            .cloned()
            .collect()
    }
}

#[async_trait]
impl CameraIngress for MediaService {
    async fn provision_camera(
        &self,
        request: &ProvisionCameraRequest,
    ) -> Result<CameraPublishAccess, MediaError> {
        let mut cameras = self
            .inner
            .cameras
            .write()
            .unwrap_or_else(|value| value.into_inner());
        if let Some(camera) = cameras.by_id.get(&request.camera_identity_id) {
            if camera.metadata.session_id != request.session_id
                || camera.metadata.session_name != request.session_name
                || camera.metadata.camera_name != request.camera_name
                || request
                    .virtual_port
                    .is_some_and(|port| port != camera.metadata.virtual_port)
            {
                return Err(MediaError::Conflict);
            }
            self.moq
                .provision_camera(
                    request.session_id,
                    request.camera_identity_id,
                    &request.camera_name,
                    PreviewSubscription::new(camera.subscribe()),
                )
                .map_err(media_moq_error)?;
            return Ok(CameraPublishAccess {
                endpoint: camera.metadata.publish_endpoint.clone(),
                virtual_port: camera.metadata.virtual_port,
                access_token: None,
                expires_at: None,
            });
        }

        if let Some(session) = cameras.sessions.get(&request.session_id)
            && session.session_name != request.session_name
        {
            return Err(MediaError::Conflict);
        }
        let mut new_session = false;
        if let std::collections::hash_map::Entry::Vacant(entry) =
            cameras.sessions.entry(request.session_id)
        {
            entry.insert(SessionRoutes {
                session_name: request.session_name.clone(),
                by_virtual_port: HashMap::new(),
                next_virtual_port: FIRST_VIRTUAL_PORT,
            });
            new_session = true;
        }
        let virtual_port = {
            let session = cameras
                .sessions
                .get_mut(&request.session_id)
                .expect("the session route was inserted above");
            if let Some(port) = request.virtual_port {
                if !(FIRST_VIRTUAL_PORT..=LAST_VIRTUAL_PORT).contains(&port)
                    || session.by_virtual_port.contains_key(&port)
                {
                    if new_session {
                        cameras.sessions.remove(&request.session_id);
                    }
                    return Err(MediaError::Conflict);
                }
                Some(port)
            } else {
                allocate_virtual_port(session)
            }
        };
        let Some(virtual_port) = virtual_port else {
            if new_session {
                cameras.sessions.remove(&request.session_id);
            }
            return Err(MediaError::Unavailable(Box::new(std::io::Error::other(
                "RIST virtual port space is exhausted",
            ))));
        };
        let rist_access = match self
            .rist
            .provision_camera(request.session_id, request.camera_identity_id)
        {
            Ok(access) => access,
            Err(error) => {
                if new_session {
                    cameras.sessions.remove(&request.session_id);
                }
                return Err(MediaError::Unavailable(Box::new(error)));
            }
        };
        let endpoint = publish_endpoint(&rist_access.endpoint, virtual_port);
        let handle = Arc::new(CameraHandle::spawn(
            CameraMetadata {
                session_id: request.session_id,
                session_name: request.session_name.clone(),
                camera_id: request.camera_identity_id,
                camera_name: request.camera_name.clone(),
                virtual_port,
                publish_endpoint: endpoint.clone(),
            },
            self.inner.config.recording_root.clone(),
            self.inner.config.ingress_queue_capacity,
            self.inner.config.preview_queue_capacity,
            self.inner.event_sender.clone(),
        ));
        if let Err(error) = self.moq.provision_camera(
            request.session_id,
            request.camera_identity_id,
            &request.camera_name,
            PreviewSubscription::new(handle.subscribe()),
        ) {
            if new_session {
                cameras.sessions.remove(&request.session_id);
            }
            if let Err(revoke_error) = self.rist.revoke_camera(request.camera_identity_id) {
                tracing::warn!(
                    %revoke_error,
                    camera_identity_id = %request.camera_identity_id,
                    "failed to roll back RIST camera after MoQ publication failure"
                );
            }
            return Err(media_moq_error(error));
        }
        let session = cameras
            .sessions
            .get_mut(&request.session_id)
            .expect("the session route was inserted above");
        session
            .by_virtual_port
            .insert(virtual_port, request.camera_identity_id);
        cameras.by_id.insert(request.camera_identity_id, handle);
        Ok(CameraPublishAccess {
            endpoint,
            virtual_port,
            access_token: None,
            expires_at: None,
        })
    }

    async fn revoke_camera(&self, camera_id: CameraIdentityId) -> Result<(), MediaError> {
        let mut cameras = self
            .inner
            .cameras
            .write()
            .unwrap_or_else(|value| value.into_inner());
        let Some(camera) = cameras.by_id.get(&camera_id).cloned() else {
            return Ok(());
        };
        self.rist
            .revoke_camera(camera_id)
            .map_err(|error| MediaError::Unavailable(Box::new(error)))?;
        cameras.by_id.remove(&camera_id);
        self.moq.revoke_camera(camera_id);
        let session_id = camera.metadata.session_id;
        let session = cameras
            .sessions
            .get_mut(&session_id)
            .expect("a provisioned camera always has a session route");
        session
            .by_virtual_port
            .remove(&camera.metadata.virtual_port);
        if session.by_virtual_port.is_empty() {
            cameras.sessions.remove(&session_id);
        }
        Ok(())
    }
}

#[async_trait]
impl PreviewService for MediaService {
    async fn issue_preview_access(
        &self,
        request: &PreviewAccessRequest,
    ) -> Result<PreviewAccess, MediaError> {
        self.moq.provision_session(request.session_id);
        let (token, expires_at) = self
            .inner
            .preview_authorizer
            .issue(request.session_id, request.valid_for)
            .map_err(media_moq_error)?;
        Ok(PreviewAccess {
            endpoint: preview_endpoint(&self.inner.config.preview_endpoint, request.session_id),
            access_token: token,
            expires_at,
            server_certificate_hashes: self.moq.server_certificate_hashes(),
        })
    }
}

fn media_moq_error(error: MoqError) -> MediaError {
    match error {
        MoqError::Conflict => MediaError::Conflict,
        error => MediaError::Unavailable(Box::new(error)),
    }
}

#[async_trait]
impl RecordingService for MediaService {
    async fn start_recording(
        &self,
        request: &StartRecordingRequest,
    ) -> Result<RecordingStarted, MediaError> {
        let camera = self.inner.camera(request.camera_identity_id)?;
        if camera.metadata.session_id != request.session_id {
            return Err(MediaError::NotFound);
        }
        camera.start_recording(request.clone()).await
    }

    async fn finish_recording(
        &self,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<FinalizedRecording, MediaError> {
        self.inner
            .camera(camera_id)?
            .finish_recording(take_id)
            .await
    }

    async fn abort_recording(
        &self,
        take_id: TakeId,
        camera_id: CameraIdentityId,
    ) -> Result<(), MediaError> {
        self.inner.camera(camera_id)?.abort_recording(take_id).await
    }
}

#[async_trait]
impl MediaEventSource for MediaService {
    async fn next_event(&self) -> Result<MediaEvent, MediaError> {
        self.events.lock().await.recv().await.ok_or_else(|| {
            MediaError::Unavailable(Box::new(std::io::Error::other(
                "media event source stopped",
            )))
        })
    }
}

#[async_trait]
impl RistStatisticsSource for MediaService {
    async fn list_rist_statistics(
        &self,
        session_name: &kinugasa_core::domain::SessionName,
    ) -> Result<Vec<RistStatisticsSnapshot>, MediaError> {
        let mut snapshots = self
            .inner
            .camera_snapshot()
            .into_iter()
            .filter(|camera| &camera.metadata.session_name == session_name)
            .filter_map(|camera| {
                camera.statistics(
                    &self.inner.config.gateway_instance,
                    self.inner.config.statistics_stale_after,
                )
            })
            .collect::<Vec<_>>();
        snapshots.sort_by(|left, right| {
            left.statistics
                .camera_name
                .cmp(&right.statistics.camera_name)
                .then_with(|| left.statistics.flow_id.cmp(&right.statistics.flow_id))
        });
        Ok(snapshots)
    }
}

fn allocate_virtual_port(session: &mut SessionRoutes) -> Option<u16> {
    for _ in FIRST_VIRTUAL_PORT..=LAST_VIRTUAL_PORT {
        let candidate = session.next_virtual_port.max(FIRST_VIRTUAL_PORT);
        session.next_virtual_port = if candidate == LAST_VIRTUAL_PORT {
            FIRST_VIRTUAL_PORT
        } else {
            candidate + 1
        };
        if !session.by_virtual_port.contains_key(&candidate) {
            return Some(candidate);
        }
    }
    None
}

fn publish_endpoint(base: &Url, virtual_port: u16) -> Url {
    let mut endpoint = base.clone();
    endpoint
        .query_pairs_mut()
        .append_pair("virt-dst-port", &virtual_port.to_string());
    endpoint
}

fn preview_endpoint(base: &Url, session_id: SessionId) -> Url {
    let mut endpoint = base.clone();
    let mut path = endpoint.path().trim_end_matches('/').to_owned();
    path.push('/');
    path.push_str(&session_id.to_string());
    endpoint.set_path(&path);
    endpoint
}

#[cfg(test)]
mod tests {
    use std::{collections::BTreeSet, sync::Mutex, time::Duration};

    use kinugasa_core::{
        domain::{CameraName, ErrorReason, GatewayInstance, RelativePath, SessionName},
        ports::{CameraIngress, MediaEventSource, RistStatisticsSource},
    };

    use super::*;

    struct TestTransport {
        state: Mutex<TestTransportState>,
    }

    struct TestTransportState {
        available_ports: BTreeSet<u16>,
        cameras: HashMap<CameraIdentityId, (SessionId, u16, Url)>,
    }

    struct TestPreviewTransport;

    impl TestTransport {
        fn new() -> Self {
            Self {
                state: Mutex::new(TestTransportState {
                    available_ports: [9_200, 9_201].into_iter().collect(),
                    cameras: HashMap::new(),
                }),
            }
        }
    }

    impl CameraTransport for TestTransport {
        fn provision_camera(
            &self,
            session_id: SessionId,
            camera_id: CameraIdentityId,
        ) -> Result<crate::rist::RistCameraPublishAccess, crate::RistError> {
            let mut state = self.state.lock().unwrap();
            if let Some((existing_session_id, _, endpoint)) = state.cameras.get(&camera_id) {
                assert_eq!(*existing_session_id, session_id);
                return Ok(crate::rist::RistCameraPublishAccess {
                    endpoint: endpoint.clone(),
                });
            }
            let port = state
                .available_ports
                .pop_first()
                .ok_or(crate::RistError::PortPoolExhausted)?;
            let mut endpoint = Url::parse("rist://recording.example.test").unwrap();
            endpoint.set_port(Some(port)).unwrap();
            endpoint
                .query_pairs_mut()
                .append_pair("aes-type", "256")
                .append_pair("secret", &session_id.to_string());
            state
                .cameras
                .insert(camera_id, (session_id, port, endpoint.clone()));
            Ok(crate::rist::RistCameraPublishAccess { endpoint })
        }

        fn revoke_camera(&self, camera_id: CameraIdentityId) -> Result<(), crate::RistError> {
            let mut state = self.state.lock().unwrap();
            if let Some((_, port, _)) = state.cameras.remove(&camera_id) {
                state.available_ports.insert(port);
            }
            Ok(())
        }

        fn shutdown(self: Box<Self>) -> Result<(), crate::RistError> {
            Ok(())
        }
    }

    #[async_trait]
    impl PreviewTransport for TestPreviewTransport {
        fn server_certificate_hashes(&self) -> Vec<kinugasa_core::ports::ServerCertificateHash> {
            Vec::new()
        }

        fn provision_session(&self, _session_id: SessionId) {}

        fn provision_camera(
            &self,
            _session_id: SessionId,
            _camera_id: CameraIdentityId,
            _camera_name: &CameraName,
            _subscription: PreviewSubscription,
        ) -> Result<(), MoqError> {
            Ok(())
        }

        fn revoke_camera(&self, _camera_id: CameraIdentityId) {}

        async fn shutdown(self: Box<Self>) -> Result<(), MoqError> {
            Ok(())
        }
    }

    fn config(root: &std::path::Path) -> MediaConfig {
        MediaConfig {
            recording_root: root.to_owned(),
            preview_endpoint: Url::parse("https://recording.example.test/moq").unwrap(),
            gateway_instance: GatewayInstance::new("test-instance").unwrap(),
            ingress_queue_capacity: 8,
            preview_queue_capacity: 8,
            statistics_stale_after: Duration::from_secs(10),
        }
    }

    async fn service(root: &std::path::Path) -> MediaService {
        let (inner, events) = MediaService::prepare(config(root)).await.unwrap();
        MediaService {
            inner,
            events: tokio::sync::Mutex::new(events),
            rist: Box::new(TestTransport::new()),
            moq: Box::new(TestPreviewTransport),
        }
    }

    #[tokio::test]
    async fn provisioning_is_idempotent_and_allocates_one_udp_port_per_camera() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let first = ProvisionCameraRequest {
            session_id,
            session_name: SessionName::new("studio").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        let second = ProvisionCameraRequest {
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("side").unwrap(),
            ..first.clone()
        };

        let first_access = service.provision_camera(&first).await.unwrap();
        assert_eq!(
            service.provision_camera(&first).await.unwrap(),
            first_access
        );
        let second_access = service.provision_camera(&second).await.unwrap();
        assert_ne!(first_access.endpoint, second_access.endpoint);
        assert_ne!(first_access.endpoint.port(), second_access.endpoint.port());
        assert_eq!(
            query_value(&first_access.endpoint, "secret"),
            query_value(&second_access.endpoint, "secret")
        );
        assert_ne!(
            query_value(&first_access.endpoint, "virt-dst-port"),
            query_value(&second_access.endpoint, "virt-dst-port")
        );
        assert!(
            first_access
                .endpoint
                .query()
                .unwrap()
                .contains("virt-dst-port=")
        );
    }

    #[tokio::test]
    async fn provisioning_restores_a_persisted_virtual_port() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let request = ProvisionCameraRequest {
            session_id,
            session_name: SessionName::new("studio").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: Some(12_345),
        };

        let access = service.provision_camera(&request).await.unwrap();

        assert_eq!(access.virtual_port, 12_345);
        assert_eq!(query_value(&access.endpoint, "virt-dst-port"), "12345");

        let duplicate = ProvisionCameraRequest {
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("side").unwrap(),
            ..request
        };
        assert!(matches!(
            service.provision_camera(&duplicate).await,
            Err(MediaError::Conflict)
        ));
    }

    #[tokio::test]
    async fn physical_ports_and_credentials_are_isolated_and_reused_by_camera() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let first_session = SessionId::new_v7();
        let second_session = SessionId::new_v7();
        let first = ProvisionCameraRequest {
            session_id: first_session,
            session_name: SessionName::new("first").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        let second = ProvisionCameraRequest {
            session_id: second_session,
            session_name: SessionName::new("second").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };

        let first_access = service.provision_camera(&first).await.unwrap();
        let second_access = service.provision_camera(&second).await.unwrap();
        assert_ne!(first_access.endpoint.port(), second_access.endpoint.port());
        assert_ne!(
            query_value(&first_access.endpoint, "secret"),
            query_value(&second_access.endpoint, "secret")
        );
        assert_eq!(
            query_value(&first_access.endpoint, "virt-dst-port"),
            query_value(&second_access.endpoint, "virt-dst-port")
        );

        service
            .ingress()
            .push_for_session_route(SessionIngressPacket {
                session_id: second_session,
                virtual_port: query_value(&second_access.endpoint, "virt-dst-port")
                    .parse()
                    .unwrap(),
                flow_id: 42,
                ntp_timestamp: 1,
                payload: stream_payloads().0,
            })
            .unwrap();
        let event = service.next_event().await.unwrap();
        assert!(matches!(
            event.kind,
            kinugasa_core::ports::MediaEventKind::CameraInputChanged {
                camera_identity_id,
                state: kinugasa_core::domain::CameraInputState::Connected,
            } if camera_identity_id == second.camera_identity_id
        ));

        service
            .revoke_camera(first.camera_identity_id)
            .await
            .unwrap();
        let replacement = ProvisionCameraRequest {
            session_id: SessionId::new_v7(),
            session_name: SessionName::new("replacement").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        let replacement_access = service.provision_camera(&replacement).await.unwrap();
        assert_eq!(
            replacement_access.endpoint.port(),
            first_access.endpoint.port()
        );
        assert_ne!(
            query_value(&replacement_access.endpoint, "secret"),
            query_value(&first_access.endpoint, "secret")
        );
    }

    #[tokio::test]
    async fn production_transport_uses_the_configured_camera_port_pool() {
        let root = tempfile::tempdir().unwrap();
        let socket = std::net::UdpSocket::bind((std::net::Ipv4Addr::LOCALHOST, 0)).unwrap();
        let port = socket.local_addr().unwrap().port();
        drop(socket);
        let service = MediaService::new(
            config(root.path()),
            RistConfig {
                listen_address: std::net::IpAddr::V4(std::net::Ipv4Addr::LOCALHOST),
                public_endpoint: Url::parse("rist://recording.example.test").unwrap(),
                available_ports: vec![port],
                encryption_pepper: "test-pepper-with-at-least-32-bytes".into(),
                recovery_buffer: Duration::from_secs(5),
                reorder_buffer: Duration::from_millis(200),
                statistics_interval: Duration::from_secs(5),
            },
            MoqConfig {
                listen_address: "127.0.0.1:0".parse().unwrap(),
                tls: crate::MoqTlsIdentity::SelfSigned {
                    hostnames: vec!["localhost".into()],
                },
            },
        )
        .await
        .unwrap();
        let first = ProvisionCameraRequest {
            session_id: SessionId::new_v7(),
            session_name: SessionName::new("first").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        let second = ProvisionCameraRequest {
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("side").unwrap(),
            ..first.clone()
        };

        let access = service.provision_camera(&first).await.unwrap();
        assert_eq!(access.endpoint.port(), Some(port));
        assert!(matches!(
            service.provision_camera(&second).await,
            Err(MediaError::Unavailable(_))
        ));
        service.shutdown().await.unwrap();
    }

    #[tokio::test]
    async fn preview_tokens_are_scoped_to_a_session() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let request = ProvisionCameraRequest {
            session_id,
            session_name: SessionName::new("studio").unwrap(),
            camera_identity_id: CameraIdentityId::new_v7(),
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        service.provision_camera(&request).await.unwrap();
        let access = service
            .issue_preview_access(&PreviewAccessRequest {
                session_id,
                valid_for: Duration::from_secs(30),
            })
            .await
            .unwrap();
        assert_eq!(access.endpoint.scheme(), "https");
        assert_eq!(access.endpoint.path(), format!("/moq/{session_id}"));
        assert!(access.endpoint.query().is_none());
        assert!(access.server_certificate_hashes.is_empty());
        assert!(
            service
                .subscribe_preview(&access.access_token, request.camera_identity_id)
                .is_ok()
        );
    }

    #[tokio::test]
    async fn recording_captures_only_packets_after_start_and_atomically_finalizes() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let camera_id = CameraIdentityId::new_v7();
        let provision = ProvisionCameraRequest {
            session_id,
            session_name: SessionName::new("studio").unwrap(),
            camera_identity_id: camera_id,
            camera_name: CameraName::new("front").unwrap(),
            virtual_port: None,
        };
        service.provision_camera(&provision).await.unwrap();
        let ingress = service.ingress();
        ingress
            .push(IngressPacket {
                camera_identity_id: camera_id,
                flow_id: 42,
                ntp_timestamp: 1,
                payload: Bytes::from_static(b"not-an-mpeg-ts-packet"),
            })
            .unwrap();
        let connected = service.next_event().await.unwrap();
        assert!(matches!(
            connected.kind,
            kinugasa_core::ports::MediaEventKind::CameraInputChanged {
                camera_identity_id,
                state: kinugasa_core::domain::CameraInputState::Connected,
            } if camera_identity_id == camera_id
        ));

        let take_id = TakeId::new_v7();
        let request = StartRecordingRequest {
            take_id,
            session_id,
            camera_identity_id: camera_id,
            relative_path: RelativePath::new("studio/take/front/video.ts").unwrap(),
        };
        let started = service.start_recording(&request).await.unwrap();
        assert_eq!(started.take_id, take_id);
        let captured = stream_payloads().1;
        ingress
            .push(IngressPacket {
                camera_identity_id: camera_id,
                flow_id: 42,
                ntp_timestamp: 2,
                payload: captured.clone(),
            })
            .unwrap();

        let finalized = service.finish_recording(take_id, camera_id).await.unwrap();
        assert_eq!(finalized.take_id(), take_id);
        assert_eq!(finalized.media_type().as_str(), "video/mp2t");
        let path = root.path().join("studio/take/front/video.ts");
        let contents = std::fs::read(path).unwrap();
        assert_eq!(contents, captured.as_ref());
        assert!(
            !root
                .path()
                .join("studio/take/front/video.ts.partial")
                .exists()
        );
    }

    #[tokio::test]
    async fn disconnect_aborts_partial_recording_and_returns_camera_to_waiting() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let camera_id = CameraIdentityId::new_v7();
        let session_name = SessionName::new("studio").unwrap();
        service
            .provision_camera(&ProvisionCameraRequest {
                session_id,
                session_name: session_name.clone(),
                camera_identity_id: camera_id,
                camera_name: CameraName::new("front").unwrap(),
                virtual_port: None,
            })
            .await
            .unwrap();
        let ingress = service.ingress();
        ingress
            .push(IngressPacket {
                camera_identity_id: camera_id,
                flow_id: 42,
                ntp_timestamp: 1,
                payload: stream_payloads().0,
            })
            .unwrap();
        let _connected = service.next_event().await.unwrap();

        let take_id = TakeId::new_v7();
        let request = StartRecordingRequest {
            take_id,
            session_id,
            camera_identity_id: camera_id,
            relative_path: RelativePath::new("studio/take/front/video.ts").unwrap(),
        };
        service.start_recording(&request).await.unwrap();
        ingress
            .push(IngressPacket {
                camera_identity_id: camera_id,
                flow_id: 42,
                ntp_timestamp: 2,
                payload: stream_payloads().1,
            })
            .unwrap();
        assert!(
            root.path()
                .join("studio/take/front/video.ts.partial")
                .exists()
        );

        ingress.disconnected(camera_id).unwrap();
        let event = service.next_event().await.unwrap();
        assert!(matches!(
            event.kind,
            kinugasa_core::ports::MediaEventKind::CameraInputChanged {
                state: kinugasa_core::domain::CameraInputState::Waiting,
                ..
            }
        ));
        assert!(
            !root
                .path()
                .join("studio/take/front/video.ts.partial")
                .exists()
        );
        assert!(service.finish_recording(take_id, camera_id).await.is_err());

        ingress
            .observe_statistics(
                camera_id,
                TransportStatistics {
                    flow_id: 42,
                    lost_packets: 0,
                    recovered_packets: 0,
                },
            )
            .unwrap();
        let statistics = service.list_rist_statistics(&session_name).await.unwrap();
        assert_eq!(statistics.len(), 1);
        assert_eq!(statistics[0].statistics.camera_name.as_str(), "front");
        assert_eq!(statistics[0].statistics.output_packets, 2);
    }

    #[tokio::test]
    async fn route_collision_errors_and_quarantines_the_camera_until_disconnect() {
        let root = tempfile::tempdir().unwrap();
        let service = service(root.path()).await;
        let session_id = SessionId::new_v7();
        let camera_id = CameraIdentityId::new_v7();
        let access = service
            .provision_camera(&ProvisionCameraRequest {
                session_id,
                session_name: SessionName::new("studio").unwrap(),
                camera_identity_id: camera_id,
                camera_name: CameraName::new("front").unwrap(),
                virtual_port: None,
            })
            .await
            .unwrap();
        let virtual_port = query_value(&access.endpoint, "virt-dst-port")
            .parse()
            .unwrap();
        let ingress = service.ingress();
        ingress
            .error_session_route(
                session_id,
                virtual_port,
                ErrorReason::new("RIST flow ID collision").unwrap(),
            )
            .unwrap();

        let event = service.next_event().await.unwrap();
        assert!(matches!(
            event.kind,
            kinugasa_core::ports::MediaEventKind::CameraInputChanged {
                camera_identity_id,
                state: kinugasa_core::domain::CameraInputState::Errored(reason),
            } if camera_identity_id == camera_id && reason.as_str() == "RIST flow ID collision"
        ));

        ingress
            .push_for_session_route(SessionIngressPacket {
                session_id,
                virtual_port,
                flow_id: 42,
                ntp_timestamp: 1,
                payload: stream_payloads().0,
            })
            .unwrap();
        assert!(
            tokio::time::timeout(Duration::from_millis(20), service.next_event())
                .await
                .is_err()
        );

        ingress
            .disconnected_session_route(session_id, virtual_port)
            .unwrap();
        let event = service.next_event().await.unwrap();
        assert!(matches!(
            event.kind,
            kinugasa_core::ports::MediaEventKind::CameraInputChanged {
                camera_identity_id,
                state: kinugasa_core::domain::CameraInputState::Waiting,
            } if camera_identity_id == camera_id
        ));
    }

    fn stream_payloads() -> (Bytes, Bytes) {
        use broadcast_common::Package;
        use transmux::{
            AVCConfigurationBox, AVCDecoderConfigurationRecord, AvcPps, AvcSps, CodecConfig, Media,
            Sample, Track, TrackSpec, TsMux,
        };

        let config = AVCDecoderConfigurationRecord {
            configuration_version: 1,
            profile_indication: 66,
            profile_compatibility: 0xc0,
            level_indication: 10,
            length_size_minus_one: 3,
            sps: vec![AvcSps(vec![
                0x67, 0x42, 0xc0, 0x0a, 0xdd, 0xec, 0x04, 0x40, 0x00, 0x00, 0x03, 0x00, 0x40, 0x00,
                0x00, 0x0f, 0x03, 0xc4, 0x89, 0xe0,
            ])],
            pps: vec![AvcPps(vec![0x68, 0xce, 0x0f, 0x2c, 0x80])],
            chroma_format: None,
            bit_depth_luma_minus8: None,
            bit_depth_chroma_minus8: None,
            sps_ext: vec![],
        };
        let spec = TrackSpec::new(
            1,
            90_000,
            CodecConfig::Avc {
                config: AVCConfigurationBox::new(config),
                width: 16,
                height: 16,
            },
        );
        let samples = [true, false, false, true, false, false]
            .into_iter()
            .enumerate()
            .map(|(index, is_sync)| {
                let nal = if is_sync { 0x65 } else { 0x41 };
                let dts = 90_000 + i64::try_from(index).unwrap() * 3_000;
                Sample::new(
                    vec![0, 0, 0, 4, nal, 0x88, 0x84, 0x21],
                    Some(dts),
                    Some(dts),
                    Some(3_000),
                    is_sync,
                )
            })
            .collect();
        let media = Media::new(vec![Track::new(spec, samples)], 90_000);
        let stream = TsMux::new().package(&media).unwrap();
        let split = 5 * 188;
        assert_eq!(stream.len(), 8 * 188);
        (
            Bytes::copy_from_slice(&stream[..split]),
            Bytes::copy_from_slice(&stream[split..]),
        )
    }

    fn query_value(endpoint: &Url, name: &str) -> String {
        endpoint
            .query_pairs()
            .find_map(|(key, value)| (key == name).then(|| value.into_owned()))
            .unwrap()
    }
}
