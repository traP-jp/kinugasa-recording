use std::{
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering},
    },
    time::Duration,
};

use chrono::{DateTime, Utc};
use kinugasa_core::{
    domain::{
        CameraIdentityId, CameraInputState, CameraName, ErrorReason, SessionId, SessionName, TakeId,
    },
    ports::{
        MediaError, MediaEvent, MediaEventKind, RecordingStarted, RistStatistics,
        RistStatisticsSnapshot, StartRecordingRequest,
    },
};
use tokio::sync::{broadcast, mpsc, oneshot};
use url::Url;

use crate::recording::RecordingFile;
use crate::service::{IngressPacket, PreviewPacket, TransportStatistics};

pub(crate) struct CameraMetadata {
    pub(crate) session_id: SessionId,
    pub(crate) session_name: SessionName,
    pub(crate) camera_id: CameraIdentityId,
    pub(crate) camera_name: CameraName,
    pub(crate) virtual_port: u16,
    pub(crate) publish_endpoint: Url,
}

pub(crate) struct CameraHandle {
    pub(crate) metadata: CameraMetadata,
    commands: mpsc::Sender<Command>,
    // Retained for the flow-collision error path that will be restored with
    // virt-dst-port multiplexing.
    #[allow(dead_code)]
    input_errors: mpsc::UnboundedSender<ErrorReason>,
    preview: broadcast::Sender<PreviewPacket>,
    overflowed: Arc<AtomicBool>,
    statistics: Arc<Statistics>,
}

impl CameraHandle {
    pub(crate) fn spawn(
        metadata: CameraMetadata,
        recording_root: PathBuf,
        queue_capacity: usize,
        preview_capacity: usize,
        events: mpsc::UnboundedSender<MediaEvent>,
    ) -> Self {
        let (commands, receiver) = mpsc::channel(queue_capacity);
        let (input_errors, error_receiver) = mpsc::unbounded_channel();
        let (preview, _) = broadcast::channel(preview_capacity);
        let overflowed = Arc::new(AtomicBool::new(false));
        let statistics = Arc::new(Statistics::new());
        tokio::spawn(run_camera(
            receiver,
            error_receiver,
            preview.clone(),
            Arc::clone(&overflowed),
            CameraWorkerConfig {
                camera_id: metadata.camera_id,
                recording_root,
                events,
            },
        ));
        Self {
            metadata,
            commands,
            input_errors,
            preview,
            overflowed,
            statistics,
        }
    }

    pub(crate) fn ingest(&self, packet: IngressPacket) -> Result<(), crate::IngressError> {
        self.statistics.output.fetch_add(1, Ordering::Relaxed);
        self.statistics
            .flow_id
            .store(packet.flow_id, Ordering::Relaxed);
        match self.commands.try_send(Command::Packet(packet)) {
            Ok(()) => Ok(()),
            Err(mpsc::error::TrySendError::Full(_)) => {
                self.overflowed.store(true, Ordering::Release);
                Err(crate::IngressError::QueueFull)
            }
            Err(mpsc::error::TrySendError::Closed(_)) => Err(crate::IngressError::Unavailable),
        }
    }

    pub(crate) fn disconnected(&self) -> Result<(), crate::IngressError> {
        self.commands
            .try_send(Command::Disconnected)
            .map_err(|error| match error {
                mpsc::error::TrySendError::Full(_) => crate::IngressError::QueueFull,
                mpsc::error::TrySendError::Closed(_) => crate::IngressError::Unavailable,
            })
    }

    #[allow(dead_code)]
    pub(crate) fn input_error(&self, reason: ErrorReason) -> Result<(), crate::IngressError> {
        self.input_errors
            .send(reason)
            .map_err(|_| crate::IngressError::Unavailable)
    }

    pub(crate) fn observe_statistics(&self, statistics: TransportStatistics) {
        self.statistics.observe_transport(statistics);
    }

    pub(crate) async fn start_recording(
        &self,
        request: StartRecordingRequest,
    ) -> Result<RecordingStarted, MediaError> {
        let (reply, response) = oneshot::channel();
        self.commands
            .send(Command::StartRecording { request, reply })
            .await
            .map_err(|_| unavailable("camera media task stopped"))?;
        response
            .await
            .map_err(|_| unavailable("camera media task stopped"))?
    }

    pub(crate) async fn finish_recording(
        &self,
        take_id: TakeId,
    ) -> Result<kinugasa_core::domain::FinalizedRecording, MediaError> {
        let (reply, response) = oneshot::channel();
        self.commands
            .send(Command::FinishRecording { take_id, reply })
            .await
            .map_err(|_| unavailable("camera media task stopped"))?;
        response
            .await
            .map_err(|_| unavailable("camera media task stopped"))?
    }

    pub(crate) async fn abort_recording(&self, take_id: TakeId) -> Result<(), MediaError> {
        let (reply, response) = oneshot::channel();
        self.commands
            .send(Command::AbortRecording { take_id, reply })
            .await
            .map_err(|_| unavailable("camera media task stopped"))?;
        response
            .await
            .map_err(|_| unavailable("camera media task stopped"))?
    }

    pub(crate) fn subscribe(&self) -> broadcast::Receiver<PreviewPacket> {
        self.preview.subscribe()
    }

    pub(crate) fn statistics(
        &self,
        gateway_instance: &kinugasa_core::domain::GatewayInstance,
        stale_after: Duration,
    ) -> Option<RistStatisticsSnapshot> {
        self.statistics.snapshot(
            &self.metadata.session_name,
            &self.metadata.camera_name,
            gateway_instance,
            stale_after,
        )
    }
}

impl Drop for CameraHandle {
    fn drop(&mut self) {
        let _ = self.commands.try_send(Command::Shutdown);
    }
}

enum Command {
    Packet(IngressPacket),
    Disconnected,
    StartRecording {
        request: StartRecordingRequest,
        reply: oneshot::Sender<Result<RecordingStarted, MediaError>>,
    },
    FinishRecording {
        take_id: TakeId,
        reply: oneshot::Sender<Result<kinugasa_core::domain::FinalizedRecording, MediaError>>,
    },
    AbortRecording {
        take_id: TakeId,
        reply: oneshot::Sender<Result<(), MediaError>>,
    },
    Shutdown,
}

struct CameraWorkerConfig {
    camera_id: CameraIdentityId,
    recording_root: PathBuf,
    events: mpsc::UnboundedSender<MediaEvent>,
}

enum RecordingState {
    Idle,
    Recording(Box<RecordingFile>),
}

struct WorkerState {
    recording: RecordingState,
    connected: bool,
    errored: bool,
    input_blocked: bool,
}

async fn run_camera(
    mut commands: mpsc::Receiver<Command>,
    mut input_errors: mpsc::UnboundedReceiver<ErrorReason>,
    preview: broadcast::Sender<PreviewPacket>,
    overflowed: Arc<AtomicBool>,
    config: CameraWorkerConfig,
) {
    let mut state = WorkerState {
        recording: RecordingState::Idle,
        connected: false,
        errored: false,
        input_blocked: false,
    };
    let mut input_errors_closed = false;

    loop {
        tokio::select! {
            input_error = input_errors.recv(), if !input_errors_closed => {
                if let Some(reason) = input_error {
                    fail_recording(&mut state.recording).await;
                    if !state.errored {
                        emit_state(&config, CameraInputState::Errored(reason));
                    }
                    state.connected = false;
                    state.errored = true;
                    state.input_blocked = true;
                } else {
                    input_errors_closed = true;
                }
            }
            command = commands.recv() => {
                let Some(command) = command else { break };
                match command {
                    Command::Packet(packet) => {
                        if state.input_blocked {
                            continue;
                        }
                        if overflowed.swap(false, Ordering::AcqRel) {
                            fail_recording(&mut state.recording).await;
                            if !state.errored {
                                emit_state(&config, CameraInputState::Errored(reason("media ingress queue overflow")));
                            }
                            state.connected = false;
                            state.errored = true;
                        }
                        if let Err(error) = handle_packet(
                            packet,
                            &mut state,
                            &preview,
                            &config,
                        ).await {
                            let message = error.to_string();
                            fail_recording(&mut state.recording).await;
                            if !state.errored {
                                emit_state(&config, CameraInputState::Errored(reason(message)));
                            }
                            state.connected = false;
                            state.errored = true;
                        }
                    }
                    Command::Disconnected => {
                        fail_recording(&mut state.recording).await;
                        if state.connected || state.errored {
                            emit_state(&config, CameraInputState::Waiting);
                        }
                        state.connected = false;
                        state.errored = false;
                        state.input_blocked = false;
                    }
                    Command::StartRecording { request, reply } => {
                        if !state.connected || !matches!(state.recording, RecordingState::Idle) {
                            let _ = reply.send(Err(MediaError::Conflict));
                        } else {
                            let started_at = Utc::now();
                            match RecordingFile::create(&config.recording_root, request.clone(), started_at).await {
                                Ok(file) => {
                                    state.recording = RecordingState::Recording(Box::new(file));
                                    let _ = reply.send(Ok(RecordingStarted {
                                        take_id: request.take_id,
                                        camera_identity_id: request.camera_identity_id,
                                        started_at,
                                    }));
                                }
                                Err(error) => {
                                    let _ = reply.send(Err(unexpected(error)));
                                }
                            }
                        }
                    }
                    Command::FinishRecording { take_id, reply } => {
                        if overflowed.swap(false, Ordering::AcqRel) {
                            fail_recording(&mut state.recording).await;
                            if !state.errored {
                                emit_state(&config, CameraInputState::Errored(reason("media ingress queue overflow")));
                            }
                            state.connected = false;
                            state.errored = true;
                            let _ = reply.send(Err(unavailable("media ingress queue overflow")));
                            continue;
                        }
                        let recording = std::mem::replace(&mut state.recording, RecordingState::Idle);
                        match recording {
                            RecordingState::Recording(file) if file_take_id(&file) == take_id => {
                                let _ = reply.send((*file).finish().await.map_err(unexpected));
                            }
                            other => {
                                state.recording = other;
                                let _ = reply.send(Err(MediaError::Conflict));
                            }
                        }
                    }
                    Command::AbortRecording { take_id, reply } => {
                        let recording = std::mem::replace(&mut state.recording, RecordingState::Idle);
                        match recording {
                            RecordingState::Recording(file) if file_take_id(&file) == take_id => {
                                let _ = reply.send((*file).abort().await.map_err(unexpected));
                            }
                            other => {
                                state.recording = other;
                                let _ = reply.send(Err(MediaError::NotFound));
                            }
                        }
                    }
                    Command::Shutdown => break,
                }
            }
        }
    }
    fail_recording(&mut state.recording).await;
}

async fn handle_packet(
    packet: IngressPacket,
    state: &mut WorkerState,
    preview: &broadcast::Sender<PreviewPacket>,
    config: &CameraWorkerConfig,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    if !state.connected {
        emit_state(config, CameraInputState::Connected);
        state.connected = true;
        state.errored = false;
    }
    if let RecordingState::Recording(file) = &mut state.recording {
        file.write_packet(&packet.payload).await?;
    }
    let _ = preview.send(PreviewPacket {
        camera_identity_id: config.camera_id,
        ntp_timestamp: packet.ntp_timestamp,
        payload: packet.payload,
    });
    Ok(())
}

async fn fail_recording(recording: &mut RecordingState) {
    match std::mem::replace(recording, RecordingState::Idle) {
        RecordingState::Idle => {}
        RecordingState::Recording(file) => {
            if let Err(error) = (*file).abort().await {
                tracing::warn!(%error, "failed to remove partial recording");
            }
        }
    }
}

fn file_take_id(file: &RecordingFile) -> TakeId {
    file.take_id()
}

fn emit_state(config: &CameraWorkerConfig, state: CameraInputState) {
    let _ = config.events.send(MediaEvent {
        occurred_at: Utc::now(),
        kind: MediaEventKind::CameraInputChanged {
            camera_identity_id: config.camera_id,
            state,
        },
    });
}

fn reason(message: impl Into<String>) -> ErrorReason {
    ErrorReason::new(message).expect("media failure message is non-empty")
}

fn unexpected(error: impl std::error::Error + Send + Sync + 'static) -> MediaError {
    MediaError::Unexpected(Box::new(error))
}

fn unavailable(message: impl Into<String>) -> MediaError {
    MediaError::Unavailable(Box::new(std::io::Error::other(message.into())))
}

struct Statistics {
    interval_start: Mutex<DateTime<Utc>>,
    latest: Mutex<Option<StatisticsInterval>>,
    flow_id: AtomicU32,
    output: AtomicU64,
    lost: AtomicU64,
    recovered: AtomicU64,
}

#[derive(Clone, Copy)]
struct StatisticsInterval {
    flow_id: u32,
    interval_start: DateTime<Utc>,
    interval_end: DateTime<Utc>,
    output_packets: u64,
    lost_packets: u64,
    recovered_packets: u64,
}

impl Statistics {
    fn new() -> Self {
        Self {
            interval_start: Mutex::new(Utc::now()),
            latest: Mutex::new(None),
            flow_id: AtomicU32::new(0),
            output: AtomicU64::new(0),
            lost: AtomicU64::new(0),
            recovered: AtomicU64::new(0),
        }
    }

    fn observe_transport(&self, statistics: TransportStatistics) {
        self.flow_id.store(statistics.flow_id, Ordering::Relaxed);
        self.recovered
            .fetch_add(statistics.recovered_packets, Ordering::Relaxed);
        self.lost
            .fetch_add(statistics.lost_packets, Ordering::Relaxed);
        let interval_end = Utc::now();
        let interval_start = {
            let mut start = self
                .interval_start
                .lock()
                .unwrap_or_else(|value| value.into_inner());
            std::mem::replace(&mut *start, interval_end)
        };
        *self
            .latest
            .lock()
            .unwrap_or_else(|value| value.into_inner()) = Some(StatisticsInterval {
            flow_id: statistics.flow_id,
            interval_start,
            interval_end,
            output_packets: self.output.swap(0, Ordering::Relaxed),
            lost_packets: self.lost.swap(0, Ordering::Relaxed),
            recovered_packets: self.recovered.swap(0, Ordering::Relaxed),
        });
    }

    fn snapshot(
        &self,
        session_name: &SessionName,
        camera_name: &CameraName,
        gateway_instance: &kinugasa_core::domain::GatewayInstance,
        stale_after: Duration,
    ) -> Option<RistStatisticsSnapshot> {
        let interval = *self
            .latest
            .lock()
            .unwrap_or_else(|value| value.into_inner());
        let interval = interval?;
        let stale = Utc::now()
            .signed_duration_since(interval.interval_end)
            .to_std()
            .is_ok_and(|age| age > stale_after);
        Some(RistStatisticsSnapshot {
            statistics: RistStatistics {
                session_name: session_name.clone(),
                camera_name: camera_name.clone(),
                gateway_instance: gateway_instance.clone(),
                flow_id: interval.flow_id,
                interval_start: interval.interval_start,
                interval_end: interval.interval_end,
                output_packets: interval.output_packets,
                lost_packets: interval.lost_packets,
                recovered_packets: interval.recovered_packets,
            },
            stale,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test(flavor = "current_thread")]
    async fn ingress_overflow_never_finalizes_an_incomplete_capture() {
        let root = tempfile::tempdir().unwrap();
        let camera_id = CameraIdentityId::new_v7();
        let session_id = SessionId::new_v7();
        let (events, mut received_events) = mpsc::unbounded_channel();
        let camera = CameraHandle::spawn(
            CameraMetadata {
                session_id,
                session_name: SessionName::new("studio").unwrap(),
                camera_id,
                camera_name: CameraName::new("front").unwrap(),
                virtual_port: 1_024,
                publish_endpoint: Url::parse("rist://localhost:9200").unwrap(),
            },
            root.path().to_owned(),
            1,
            1,
            events,
        );
        camera
            .ingest(IngressPacket {
                camera_identity_id: camera_id,
                flow_id: 42,
                ntp_timestamp: 1,
                payload: bytes::Bytes::from_static(b"initial"),
            })
            .unwrap();
        assert!(matches!(
            received_events.recv().await.unwrap().kind,
            MediaEventKind::CameraInputChanged {
                state: CameraInputState::Connected,
                ..
            }
        ));

        let take_id = TakeId::new_v7();
        camera
            .start_recording(StartRecordingRequest {
                take_id,
                session_id,
                camera_identity_id: camera_id,
                relative_path: kinugasa_core::domain::RelativePath::new(
                    "studio/take/front/video.ts",
                )
                .unwrap(),
            })
            .await
            .unwrap();
        let packet = IngressPacket {
            camera_identity_id: camera_id,
            flow_id: 42,
            ntp_timestamp: 2,
            payload: bytes::Bytes::from_static(b"captured"),
        };
        camera.ingest(packet.clone()).unwrap();
        assert!(matches!(
            camera.ingest(packet),
            Err(crate::IngressError::QueueFull)
        ));
        assert!(camera.finish_recording(take_id).await.is_err());
        assert!(!root.path().join("studio/take/front/video.ts").exists());
        assert!(
            !root
                .path()
                .join("studio/take/front/video.ts.partial")
                .exists()
        );
    }

    #[test]
    fn rist_statistics_rotate_as_delta_intervals() {
        let statistics = Statistics::new();
        statistics.output.fetch_add(5, Ordering::Relaxed);
        statistics.lost.fetch_add(2, Ordering::Relaxed);
        statistics.observe_transport(TransportStatistics {
            flow_id: 42,
            lost_packets: 0,
            recovered_packets: 3,
        });
        let session = SessionName::new("studio").unwrap();
        let camera = CameraName::new("front").unwrap();
        let instance = kinugasa_core::domain::GatewayInstance::new("gateway").unwrap();
        let first = statistics
            .snapshot(&session, &camera, &instance, Duration::from_secs(10))
            .unwrap();
        assert_eq!(first.statistics.output_packets, 5);
        assert_eq!(first.statistics.lost_packets, 2);
        assert_eq!(first.statistics.recovered_packets, 3);

        statistics.output.fetch_add(1, Ordering::Relaxed);
        statistics.observe_transport(TransportStatistics {
            flow_id: 42,
            lost_packets: 0,
            recovered_packets: 1,
        });
        let second = statistics
            .snapshot(&session, &camera, &instance, Duration::from_secs(10))
            .unwrap();
        assert_eq!(second.statistics.output_packets, 1);
        assert_eq!(second.statistics.lost_packets, 0);
        assert_eq!(second.statistics.recovered_packets, 1);
        assert!(second.statistics.interval_start >= first.statistics.interval_end);
    }
}
