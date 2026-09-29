use std::{collections::HashMap, process::ExitCode};

use kinugasa_app::{AppConfig, Backend};
use opentelemetry_appender_tracing::layer::OpenTelemetryTracingBridge;
use opentelemetry_otlp::{WithExportConfig, WithHttpConfig};
use opentelemetry_sdk::{Resource, logs::SdkLoggerProvider};
use tracing::{error, info};
use tracing_subscriber::{EnvFilter, prelude::*};

fn main() -> ExitCode {
    let logger_provider = otlp_logger_provider();
    tracing_subscriber::registry()
        .with(EnvFilter::from_default_env().add_directive(tracing::Level::INFO.into()))
        .with(tracing_subscriber::fmt::layer())
        .with(
            logger_provider
                .as_ref()
                .map(OpenTelemetryTracingBridge::new),
        )
        .init();

    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
    {
        Ok(runtime) => runtime,
        Err(error) => {
            eprintln!("failed to create Tokio runtime: {error}");
            return ExitCode::FAILURE;
        }
    };
    let result = match runtime.block_on(run()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            error!(%error, "kinugasa stopped");
            ExitCode::FAILURE
        }
    };
    if let Some(provider) = logger_provider {
        if let Err(error) = provider.shutdown() {
            eprintln!("failed to flush OTLP logs: {error}");
        }
    }
    result
}

fn otlp_logger_provider() -> Option<SdkLoggerProvider> {
    let endpoint = std::env::var("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT").ok()?;
    let token = match std::env::var("VL_ALLOY_INGEST_TOKEN") {
        Ok(token) if !token.is_empty() => token,
        _ => {
            eprintln!("OTLP log export disabled: VL_ALLOY_INGEST_TOKEN is missing");
            return None;
        }
    };
    let headers = HashMap::from([("authorization".to_owned(), format!("Bearer {token}"))]);
    let exporter = match opentelemetry_otlp::LogExporter::builder()
        .with_http()
        .with_endpoint(endpoint)
        .with_headers(headers)
        .build()
    {
        Ok(exporter) => exporter,
        Err(error) => {
            eprintln!("OTLP log export disabled: {error}");
            return None;
        }
    };
    Some(
        SdkLoggerProvider::builder()
            .with_resource(
                Resource::builder()
                    .with_service_name("kinugasa-recording")
                    .build(),
            )
            .with_batch_exporter(exporter)
            .build(),
    )
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let config = AppConfig::from_env()?;
    let backend = Backend::build(config).await?;
    info!("kinugasa backend started");
    backend.run_until(shutdown_signal()).await?;
    info!("kinugasa backend stopped");
    Ok(())
}

async fn shutdown_signal() {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{SignalKind, signal};

        let mut terminate = signal(SignalKind::terminate()).expect("install SIGTERM handler");
        tokio::select! {
            result = tokio::signal::ctrl_c() => {
                result.expect("install Ctrl-C handler");
            }
            _ = terminate.recv() => {}
        }
    }
    #[cfg(not(unix))]
    tokio::signal::ctrl_c()
        .await
        .expect("install Ctrl-C handler");
}
