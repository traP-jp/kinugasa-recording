mod metrics;
mod receiver;
mod rtp;
mod sequence;

use std::net::{SocketAddr, UdpSocket};
use std::process::ExitCode;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, mpsc};
use std::thread;
use std::time::Duration;

use clap::Parser;
use rist_rs::{
    Driver, DriverBuilder, EncryptionKeySize, PeerConfig, Profile, ReceiverConfig, ReceiverHandlers,
};
use tracing::{error, info};
use tracing_subscriber::EnvFilter;

use crate::metrics::{GatewayIdentity, GatewayMetrics};
use crate::receiver::{GatewayReceiver, StderrLogHandler};

const STATISTICS_INTERVAL: Duration = Duration::from_secs(5);

#[derive(Debug, Parser)]
#[command(name = "kinugasa-video-gateway")]
struct Args {
    #[arg(short = 'i', long, value_name = "RIST_URL")]
    input_url: String,
    #[arg(short = 'o', long, value_name = "HOST:PORT")]
    output_address: SocketAddr,
    #[arg(long, default_value_t = 5_000, value_name = "MILLISECONDS")]
    recovery_buffer_ms: u64,
    /// Wait for out-of-order packets before requesting retransmission.
    #[arg(long, default_value_t = 200, value_name = "MILLISECONDS")]
    reorder_buffer_ms: u64,
    #[arg(long, env = "KINUGASA_RIST_SECRET", hide_env_values = true)]
    rist_secret: Option<String>,
    #[arg(long, env = "OTEL_EXPORTER_OTLP_ENDPOINT")]
    otlp_endpoint: String,
    #[arg(long, env = "KINUGASA_SESSION_NAME")]
    session_name: String,
    #[arg(long, env = "KINUGASA_CAMERA_NAME")]
    camera_name: String,
    #[arg(long, env = "KINUGASA_GATEWAY_INSTANCE")]
    gateway_instance: String,
}

fn main() -> ExitCode {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env().add_directive(tracing::Level::INFO.into()))
        .init();
    match run(Args::parse()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            error!(%error, "video gateway stopped");
            ExitCode::FAILURE
        }
    }
}

fn run(args: Args) -> Result<(), String> {
    if args.recovery_buffer_ms == 0 || args.reorder_buffer_ms >= args.recovery_buffer_ms {
        return Err("recovery buffer must be positive and larger than reorder buffer".into());
    }
    let mut peer = PeerConfig::parse(&args.input_url)
        .map_err(|error| format!("parse RIST input URL: {error}"))?;
    let recovery = Duration::from_millis(args.recovery_buffer_ms);
    peer.set_recovery_length(recovery, recovery)
        .map_err(|error| format!("configure RIST recovery buffer: {error}"))?;
    peer.set_reorder_buffer(Duration::from_millis(args.reorder_buffer_ms))
        .map_err(|error| format!("configure RIST reorder buffer: {error}"))?;
    info!(
        recovery_buffer_ms = args.recovery_buffer_ms,
        reorder_buffer_ms = args.reorder_buffer_ms,
        "configured RIST receive buffers"
    );
    if let Some(secret) = args.rist_secret.as_deref() {
        peer.set_encryption(EncryptionKeySize::Aes256, secret)
            .map_err(|error| format!("configure RIST encryption: {error}"))?;
    }

    let socket = UdpSocket::bind(match args.output_address {
        SocketAddr::V4(_) => "0.0.0.0:0",
        SocketAddr::V6(_) => "[::]:0",
    })
    .map_err(|error| format!("open RTP output socket: {error}"))?;
    socket
        .connect(args.output_address)
        .map_err(|error| format!("connect RTP output socket: {error}"))?;

    let telemetry = GatewayMetrics::new(
        &args.otlp_endpoint,
        GatewayIdentity {
            session_name: args.session_name,
            camera_name: args.camera_name,
            gateway_instance: args.gateway_instance,
        },
        STATISTICS_INTERVAL,
    )?;
    let receiver_events = Arc::new(GatewayReceiver::new(socket, telemetry.metrics()));
    let handlers = ReceiverHandlers::new(receiver_events).with_log(Arc::new(StderrLogHandler));

    let (driver_tx, driver_rx) = mpsc::sync_channel(1);
    let driver_thread = thread::Builder::new()
        .name("rist-driver".to_owned())
        .spawn(move || DriverBuilder::new(move |driver| drop(driver_tx.send(driver))).start())
        .map_err(|error| format!("start RIST driver thread: {error}"))?;
    let driver = driver_rx
        .recv()
        .map_err(|_| "RIST driver stopped during startup".to_owned())?;

    let result = run_receiver(&driver, peer, handlers);
    let shutdown_result = driver
        .shutdown()
        .map_err(|error| format!("shut down RIST driver: {error}"));
    let join_result = driver_thread
        .join()
        .map_err(|_| "RIST driver thread panicked".to_owned())?
        .map_err(|error| format!("RIST driver failed: {error}"));
    let telemetry_result = telemetry.shutdown();
    result
        .and(shutdown_result)
        .and(join_result)
        .and(telemetry_result)
}

fn run_receiver(
    driver: &Driver,
    peer: PeerConfig,
    handlers: ReceiverHandlers,
) -> Result<(), String> {
    let receiver = driver
        .create_receiver_with_config(
            ReceiverConfig {
                profile: Profile::Main,
                peers: vec![peer],
                statistics_interval: Some(STATISTICS_INTERVAL),
                ..ReceiverConfig::default()
            },
            handlers,
        )
        .map_err(|error| format!("create RIST receiver: {error}"))?;
    let running = Arc::new(AtomicBool::new(true));
    let signal_running = Arc::clone(&running);
    ctrlc::set_handler(move || signal_running.store(false, Ordering::Relaxed))
        .map_err(|error| format!("install signal handler: {error}"))?;
    info!("RIST receiver started");
    while running.load(Ordering::Relaxed) {
        thread::park_timeout(Duration::from_millis(250));
    }
    receiver
        .close()
        .map_err(|error| format!("close RIST receiver: {error}"))
}
