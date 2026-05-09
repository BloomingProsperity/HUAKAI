use tracing_subscriber::{filter::EnvFilter, fmt, layer::SubscriberExt, util::SubscriberInitExt};

use crate::{config::StartupConfig, error::GatewayError};

pub fn install(config: &StartupConfig) -> Result<(), GatewayError> {
    let filter = EnvFilter::try_new(config.log_level.to_string())
        .map_err(|err| GatewayError::Config(format!("invalid tracing filter: {err}")))?;

    if config.json_logs {
        tracing_subscriber::registry()
            .with(filter)
            .with(
                fmt::layer()
                    .json()
                    .flatten_event(true)
                    .with_target(true)
                    .with_thread_ids(true),
            )
            .try_init()
            .map_err(|err| GatewayError::Internal(err.to_string()))?;
    } else {
        tracing_subscriber::registry()
            .with(filter)
            .with(
                fmt::layer()
                    .compact()
                    .with_target(true)
                    .with_thread_ids(true),
            )
            .try_init()
            .map_err(|err| GatewayError::Internal(err.to_string()))?;
    }

    // OTLP 端点先进入配置和日志，真实 exporter 留给后续 atom 接线。
    tracing::info!(
        otlp_endpoint = %config.tracing_endpoint,
        otlp_exporter_active = false,
        "otlp exporter stub configured"
    );

    Ok(())
}
