use std::sync::Arc;
use std::time::Duration;

use opentelemetry::metrics::{Counter, MeterProvider};
use opentelemetry::{KeyValue, global};
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::{PeriodicReader, SdkMeterProvider, Temporality};

pub const OUTPUT_PACKETS: &str = "kinugasa.rist.output.packets";
pub const LOST_PACKETS: &str = "kinugasa.rist.lost.packets";
pub const RECOVERED_PACKETS: &str = "kinugasa.rist.recovered.packets";
pub const DISCONTINUITIES: &str = "kinugasa.rist.discontinuities";
pub const FLOW_ID_ATTRIBUTE: &str = "rist.flow.id";

pub struct GatewayIdentity {
    pub session_name: String,
    pub camera_name: String,
    pub gateway_instance: String,
}

#[derive(Clone)]
pub struct MetricCounters {
    output_packets: Counter<u64>,
    lost_packets: Counter<u64>,
    recovered_packets: Counter<u64>,
    discontinuities: Counter<u64>,
}

impl MetricCounters {
    pub fn output(&self, flow_id: u32) {
        self.output_packets.add(1, &flow_attributes(flow_id));
    }

    pub fn lost(&self, flow_id: u32, count: u64) {
        if count > 0 {
            self.lost_packets.add(count, &flow_attributes(flow_id));
        }
    }

    pub fn recovered(&self, flow_id: u32, count: u64) {
        if count > 0 {
            self.recovered_packets.add(count, &flow_attributes(flow_id));
        }
    }

    pub fn discontinuity(&self, flow_id: u32) {
        self.discontinuities.add(1, &flow_attributes(flow_id));
    }
}

fn flow_attributes(flow_id: u32) -> [KeyValue; 1] {
    [KeyValue::new(FLOW_ID_ATTRIBUTE, i64::from(flow_id))]
}

pub struct GatewayMetrics {
    provider: SdkMeterProvider,
    counters: Arc<MetricCounters>,
}

impl GatewayMetrics {
    pub fn new(
        endpoint: &str,
        identity: GatewayIdentity,
        interval: Duration,
    ) -> Result<Self, String> {
        let exporter = opentelemetry_otlp::MetricExporter::builder()
            .with_tonic()
            .with_endpoint(endpoint)
            .with_timeout(Duration::from_secs(2))
            .with_temporality(Temporality::Delta)
            .build()
            .map_err(|error| format!("create OTLP metrics exporter: {error}"))?;
        let reader = PeriodicReader::builder(exporter)
            .with_interval(interval)
            .build();
        let resource = Resource::builder()
            .with_attributes([
                KeyValue::new("service.name", "kinugasa-video-gateway"),
                KeyValue::new("service.instance.id", identity.gateway_instance),
                KeyValue::new("kinugasa.session.name", identity.session_name),
                KeyValue::new("kinugasa.camera.name", identity.camera_name),
            ])
            .build();
        let provider = SdkMeterProvider::builder()
            .with_resource(resource)
            .with_reader(reader)
            .build();
        global::set_meter_provider(provider.clone());
        let meter = provider.meter("kinugasa-video-gateway");
        let counters = Arc::new(MetricCounters {
            output_packets: meter
                .u64_counter(OUTPUT_PACKETS)
                .with_unit("{packet}")
                .build(),
            lost_packets: meter
                .u64_counter(LOST_PACKETS)
                .with_unit("{packet}")
                .build(),
            recovered_packets: meter
                .u64_counter(RECOVERED_PACKETS)
                .with_unit("{packet}")
                .build(),
            discontinuities: meter
                .u64_counter(DISCONTINUITIES)
                .with_unit("{event}")
                .build(),
        });
        Ok(Self { provider, counters })
    }

    pub fn metrics(&self) -> Arc<MetricCounters> {
        Arc::clone(&self.counters)
    }

    pub fn shutdown(self) -> Result<(), String> {
        self.provider
            .shutdown()
            .map_err(|error| format!("shut down metrics exporter: {error}"))
    }
}
