use std::collections::HashMap;
use std::net::UdpSocket;
use std::sync::{Arc, Mutex};

use rist_rs::{
    AuthenticationRequest, ConnectionStatus, DataBlock, LogHandler, LogLevel, PeerInfo,
    ReceiverHandler, ReceiverStatistics,
};
use tracing::{debug, error, info, warn};

use crate::metrics::MetricCounters;
use crate::rtp::rtp_packet;
use crate::sequence::{PacketOrder, SequenceState};

pub struct GatewayReceiver {
    output: UdpSocket,
    metrics: Arc<MetricCounters>,
    flows: Mutex<HashMap<u32, SequenceState>>,
}

impl GatewayReceiver {
    pub fn new(output: UdpSocket, metrics: Arc<MetricCounters>) -> Self {
        Self {
            output,
            metrics,
            flows: Mutex::new(HashMap::new()),
        }
    }

    fn observe(&self, data: &DataBlock) {
        self.metrics.output(data.flow_id());
        let mut flows = self
            .flows
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        let state = flows.entry(data.flow_id()).or_default();
        let explicitly_discontinuous =
            data.is_discontinuity() || data.is_flow_buffer_start() || data.is_overflow();
        match state.observe(
            data.sequence(),
            data.ntp_timestamp(),
            explicitly_discontinuous,
        ) {
            PacketOrder::Continuous | PacketOrder::DuplicateOrLate => {}
            PacketOrder::Gap(count) => self.metrics.lost(data.flow_id(), count),
            PacketOrder::Discontinuity => self.metrics.discontinuity(data.flow_id()),
        }
    }
}

impl ReceiverHandler for GatewayReceiver {
    fn handle_data(&self, data: DataBlock) {
        self.observe(&data);
        let packet = rtp_packet(&data);
        if let Err(error) = self.output.send(&packet) {
            warn!(flow_id = data.flow_id(), %error, "failed to send RTP packet");
        }
    }

    fn authenticate(&self, request: AuthenticationRequest) -> bool {
        info!(remote = %request.remote_address, remote_port = request.remote_port, "accepted incoming RIST peer");
        true
    }

    fn handle_connection_status(&self, peer: PeerInfo, status: ConnectionStatus) {
        info!(peer_id = peer.id, ?status, "RIST peer status changed");
    }

    fn handle_statistics(&self, statistics: ReceiverStatistics) {
        self.metrics.recovered(
            statistics.flow.flow_id,
            u64::from(statistics.flow.recovered),
        );
        debug!(
            flow_id = statistics.flow.flow_id,
            recovered = statistics.flow.recovered
        );
    }

    fn handle_session_timeout(&self, flow_id: u32) {
        let removed = self
            .flows
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .remove(&flow_id)
            .is_some();
        if removed {
            self.metrics.discontinuity(flow_id);
        }
        info!(flow_id, "RIST flow timed out");
    }
}

pub struct StderrLogHandler;

impl LogHandler for StderrLogHandler {
    fn handle_log(&self, level: LogLevel, message: &str) {
        let message = message.trim_end();
        match level {
            LogLevel::Error => error!(target: "librist", %message),
            LogLevel::Warning => warn!(target: "librist", %message),
            LogLevel::Debug | LogLevel::Simulate => debug!(target: "librist", %message),
            LogLevel::Disable => {}
            _ => info!(target: "librist", %message),
        }
    }
}
