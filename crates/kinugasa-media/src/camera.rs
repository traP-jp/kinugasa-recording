use std::{
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering},
    },
    time::{Duration, Instant},
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
use crate::{
    service::{IngressPacket, PreviewPacket, TransportStatistics},
    transport_stream::Inspector,
};

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
        recording_start_timeout: Duration,
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
            Arc::clone(&statistics),
            CameraWorkerConfig {
                camera_id: metadata.camera_id,
                recording_root,
                recording_start_timeout,
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
    recording_start_timeout: Duration,
    events: mpsc::UnboundedSender<MediaEvent>,
}

enum RecordingState {
    Idle,
    Arming {
        request: StartRecordingRequest,
        deadline: Instant,
        reply: oneshot::Sender<Result<RecordingStarted, MediaError>>,
    },
    Recording(RecordingFile),
}

struct WorkerState {
    inspector: Inspector,
    recording: RecordingState,
    sequence: SequenceState,
    connected: bool,
    errored: bool,
    input_blocked: bool,
}

async fn run_camera(
    mut commands: mpsc::Receiver<Command>,
    mut input_errors: mpsc::UnboundedReceiver<ErrorReason>,
    preview: broadcast::Sender<PreviewPacket>,
    overflowed: Arc<AtomicBool>,
    statistics: Arc<Statistics>,
    config: CameraWorkerConfig,
) {
    let mut state = WorkerState {
        inspector: Inspector::default(),
        recording: RecordingState::Idle,
        sequence: SequenceState::default(),
        connected: false,
        errored: false,
        input_blocked: false,
    };
    let mut tick = tokio::time::interval(Duration::from_millis(100));
    tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let mut input_errors_closed = false;

    loop {
        tokio::select! {
            input_error = input_errors.recv(), if !input_errors_closed => {
                if let Some(reason) = input_error {
                    fail_recording(&mut state.recording, reason.as_str()).await;
                    state.sequence = SequenceState::default();
                    state.inspector = Inspector::default();
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
                            statistics.discontinuities.fetch_add(1, Ordering::Relaxed);
                            fail_recording(&mut state.recording, "media ingress queue overflow").await;
                            if !state.errored {
                                emit_state(&config, CameraInputState::Errored(reason("media ingress queue overflow")));
                            }
                            state.connected = false;
                            state.errored = true;
                            state.sequence = SequenceState::default();
                            state.inspector = Inspector::default();
                        }
                        if let Err(error) = handle_packet(
                            packet,
                            &mut state,
                            &preview,
                            &statistics,
                            &config,
                        ).await {
                            let message = error.to_string();
                            fail_recording(&mut state.recording, &message).await;
                            if !state.errored {
                                emit_state(&config, CameraInputState::Errored(reason(message)));
                            }
                            state.connected = false;
                            state.errored = true;
                            state.sequence = SequenceState::default();
                            state.inspector = Inspector::default();
                        }
                    }
                    Command::Disconnected => {
                        fail_recording(&mut state.recording, "camera input disconnected").await;
                        state.sequence = SequenceState::default();
                        state.inspector = Inspector::default();
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
                            state.recording = RecordingState::Arming {
                                request,
                                deadline: Instant::now() + config.recording_start_timeout,
                                reply,
                            };
                        }
                    }
                    Command::FinishRecording { take_id, reply } => {
                        let recording = std::mem::replace(&mut state.recording, RecordingState::Idle);
                        match recording {
                            RecordingState::Recording(file) if file_take_id(&file) == take_id => {
                                let _ = reply.send(file.finish().await.map_err(unexpected));
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
                                let _ = reply.send(file.abort().await.map_err(unexpected));
                            }
                            RecordingState::Arming { request, reply: start_reply, .. }
                                if request.take_id == take_id => {
                                    let _ = start_reply.send(Err(MediaError::Conflict));
                                    let _ = reply.send(Ok(()));
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
            _ = tick.tick() => {
                if matches!(&state.recording, RecordingState::Arming { deadline, .. } if Instant::now() >= *deadline)
                    && let RecordingState::Arming { reply, .. } = std::mem::replace(&mut state.recording, RecordingState::Idle) {
                        let _ = reply.send(Err(MediaError::Timeout));
                }
            }
        }
    }
    fail_recording(&mut state.recording, "camera media task stopped").await;
}

async fn handle_packet(
    packet: IngressPacket,
    state: &mut WorkerState,
    preview: &broadcast::Sender<PreviewPacket>,
    statistics: &Statistics,
    config: &CameraWorkerConfig,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    Inspector::validate_payload(&packet.payload)?;
    match state
        .sequence
        .observe(packet.sequence, packet.ntp_timestamp, packet.discontinuity)
    {
        PacketOrder::DuplicateOrLate => return Ok(()),
        PacketOrder::Gap(count) => {
            statistics.lost.fetch_add(count, Ordering::Relaxed);
            return Err(format!("RIST sequence gap of {count} packets").into());
        }
        PacketOrder::Discontinuity => {
            statistics.discontinuities.fetch_add(1, Ordering::Relaxed);
            return Err("RIST stream discontinuity".into());
        }
        PacketOrder::Continuous => {}
    }
    for ts_packet in packet
        .payload
        .chunks_exact(crate::transport_stream::TS_PACKET_SIZE)
    {
        let start_prefix = state.inspector.observe(ts_packet)?;
        if let RecordingState::Recording(file) = &mut state.recording {
            file.write_packet(ts_packet).await?;
            continue;
        }
        if start_prefix.is_some() && matches!(state.recording, RecordingState::Arming { .. }) {
            let RecordingState::Arming { request, reply, .. } =
                std::mem::replace(&mut state.recording, RecordingState::Idle)
            else {
                unreachable!()
            };
            let started_at = Utc::now();
            match RecordingFile::create(
                &config.recording_root,
                request.clone(),
                started_at,
                start_prefix.as_deref().expect("checked above"),
            )
            .await
            {
                Ok(file) => {
                    state.recording = RecordingState::Recording(file);
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
    if !state.connected {
        emit_state(config, CameraInputState::Connected);
        state.connected = true;
        state.errored = false;
    }
    let _ = preview.send(PreviewPacket {
        camera_identity_id: config.camera_id,
        ntp_timestamp: packet.ntp_timestamp,
        payload: packet.payload,
    });
    Ok(())
}

async fn fail_recording(recording: &mut RecordingState, message: &str) {
    match std::mem::replace(recording, RecordingState::Idle) {
        RecordingState::Idle => {}
        RecordingState::Arming { reply, .. } => {
            let _ = reply.send(Err(unavailable(message)));
        }
        RecordingState::Recording(file) => {
            if let Err(error) = file.abort().await {
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

#[derive(Default)]
struct SequenceState {
    previous_sequence: Option<u64>,
    previous_timestamp: Option<u64>,
}

enum PacketOrder {
    Continuous,
    Gap(u64),
    DuplicateOrLate,
    Discontinuity,
}

impl SequenceState {
    fn observe(&mut self, sequence: u64, timestamp: u64, discontinuity: bool) -> PacketOrder {
        let Some(previous) = self.previous_sequence else {
            self.previous_sequence = Some(sequence);
            self.previous_timestamp = Some(timestamp);
            // librist marks the first delivered block as flow-buffer start.
            // With no previous baseline this is startup, not a broken stream.
            return PacketOrder::Continuous;
        };
        if discontinuity
            || self
                .previous_timestamp
                .is_some_and(|old| sequence > previous && timestamp < old)
        {
            self.previous_sequence = Some(sequence);
            self.previous_timestamp = Some(timestamp);
            return PacketOrder::Discontinuity;
        }
        if sequence <= previous {
            return PacketOrder::DuplicateOrLate;
        }
        self.previous_sequence = Some(sequence);
        self.previous_timestamp = Some(timestamp);
        let gap = sequence - previous - 1;
        if gap == 0 {
            PacketOrder::Continuous
        } else {
            PacketOrder::Gap(gap)
        }
    }
}

struct Statistics {
    interval_start: Mutex<DateTime<Utc>>,
    latest: Mutex<Option<StatisticsInterval>>,
    flow_id: AtomicU32,
    output: AtomicU64,
    lost: AtomicU64,
    recovered: AtomicU64,
    discontinuities: AtomicU64,
}

#[derive(Clone, Copy)]
struct StatisticsInterval {
    flow_id: u32,
    interval_start: DateTime<Utc>,
    interval_end: DateTime<Utc>,
    output_packets: u64,
    lost_packets: u64,
    recovered_packets: u64,
    discontinuities: u64,
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
            discontinuities: AtomicU64::new(0),
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
            discontinuities: self.discontinuities.swap(0, Ordering::Relaxed),
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
                discontinuities: interval.discontinuities,
            },
            stale,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

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
