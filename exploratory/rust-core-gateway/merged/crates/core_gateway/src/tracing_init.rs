// tracing 初始化模块
// M-rust-1: JSON 结构化日志 + OTLP provider 构建 (batch exporter 留 M-rust-2 接线)
// OTLP 端点缺失或构建失败时优雅退化, 不阻断进程启动
//
// 架构说明: tracing-subscriber 的 Layer trait 以 Subscriber 类型为泛型参数;
// OpenTelemetryLayer<S,T> 的 S 在 .with() 调用时被固定到当前 registry 的具体类型.
// 若 JSON / 紧凑两条分支共用同一个 OTel layer 变量, 编译器无法统一两个不同的 S,
// 导致类型错误. M-rust-1 的设计选择: subscriber 只挂 fmt 层;
// OTel TracerProvider 在此处构建并作为 guard 返回, M-rust-2 再将 span exporter 接入.

use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::runtime::Tokio;
use tracing_appender::non_blocking::WorkerGuard;
use tracing_subscriber::{filter::EnvFilter, fmt, layer::SubscriberExt, util::SubscriberInitExt};

use crate::{config::StartupConfig, error::GatewayError};

/// tracing 生命周期 guard。
/// 调用方必须持有到进程退出, 否则非阻塞日志 worker 会提前停止。
pub struct TracingGuards {
    pub log: WorkerGuard,
    pub otlp: Option<opentelemetry_sdk::trace::TracerProvider>,
}

/// 安装全局 tracing subscriber 并可选地构建 OTLP TracerProvider
///
/// - config.json_logs = true  → JSON 格式结构化日志
/// - config.json_logs = false → 紧凑文本格式 (本地开发用)
/// - 若 config.otlp_endpoint 存在, 尝试构建 OTLP batch exporter provider
///   (M-rust-1 构建但不挂到 subscriber; M-rust-2 接入 span exporter)
/// - OTLP 构建失败时优雅退化, 不 panic
///
/// 返回日志 worker 与 OTLP TracerProvider guard; 调用者持有到进程退出以确保 flush
pub fn install(config: &StartupConfig) -> Result<TracingGuards, GatewayError> {
    let filter = EnvFilter::try_new(config.log_level.to_string())
        .map_err(|err| GatewayError::Config(format!("invalid tracing filter: {err}")))?;
    let (log_writer, log_guard) = tracing_appender::non_blocking(std::io::stdout());

    // 两条分支各自独立注册, 避免跨分支类型统一问题
    if config.json_logs {
        tracing_subscriber::registry()
            .with(filter)
            .with(
                fmt::layer()
                    .json()
                    .flatten_event(true)
                    .with_target(true)
                    .with_thread_ids(true)
                    .with_writer(log_writer),
            )
            .try_init()
            .map_err(|err| GatewayError::Internal(err.to_string()))?;
    } else {
        // 紧凑文本格式 (本地开发调试用)
        tracing_subscriber::registry()
            .with(filter)
            .with(
                fmt::layer()
                    .compact()
                    .with_target(true)
                    .with_thread_ids(true)
                    .with_writer(log_writer),
            )
            .try_init()
            .map_err(|err| GatewayError::Internal(err.to_string()))?;
    }

    // 构建 OTLP provider (M-rust-1 只构建不接入 subscriber span exporter)
    let provider = match &config.otlp_endpoint {
        Some(endpoint) => {
            let ep = endpoint.to_string();
            match build_otlp_provider(ep.trim_end_matches('/')) {
                Ok(p) => {
                    tracing::info!(
                        otlp_endpoint = %endpoint,
                        otlp_exporter_active = false,
                        "OTLP provider 已构建 (M-rust-2 接入 span exporter)"
                    );
                    Some(p)
                }
                Err(e) => {
                    // OTLP 初始化失败不阻断启动 — 退化到本地日志
                    tracing::warn!(error = %e, "OTLP provider 构建失败, 退化到本地日志");
                    None
                }
            }
        }
        None => {
            tracing::info!(
                otlp_endpoint = "none",
                otlp_exporter_active = false,
                "OTLP 端点未配置, 仅本地日志"
            );
            None
        }
    };

    Ok(TracingGuards {
        log: log_guard,
        otlp: provider,
    })
}

/// 构建 OTLP gRPC batch exporter provider
pub fn build_otlp_provider(
    endpoint: &str,
) -> Result<opentelemetry_sdk::trace::TracerProvider, opentelemetry::trace::TraceError> {
    use opentelemetry_sdk::trace::TracerProvider;

    let exporter = opentelemetry_otlp::SpanExporter::builder()
        .with_tonic()
        .with_endpoint(endpoint)
        .build()?;

    let provider = TracerProvider::builder()
        .with_batch_exporter(exporter, Tokio)
        .build();

    Ok(provider)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::StartupConfig;

    fn make_config(json_logs: bool) -> StartupConfig {
        StartupConfig::from_env_iter(vec![
            ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
            (
                "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
                "http://127.0.0.1:48080".to_owned(),
            ),
            ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
            ("HUAKAI_JSON_LOGS".to_owned(), json_logs.to_string()),
            ("HUAKAI_WORKER_THREADS".to_owned(), "1".to_owned()),
        ])
        .expect("测试配置应可解析")
    }

    #[test]
    fn install_json_logs_without_otlp_succeeds() {
        // 多次调用会因 subscriber 已注册而返回 Internal 错误, 但不应 panic
        let cfg = make_config(true);
        let result = install(&cfg);
        match result {
            Ok(guards) => {
                assert!(guards.otlp.is_none());
                let _keep_log_guard_alive = &guards.log;
            }
            Err(GatewayError::Internal(_)) => {}
            Err(e) => panic!("不应出现非 Internal 错误: {e}"),
        }
    }

    #[test]
    fn install_text_logs_without_otlp_succeeds() {
        let cfg = make_config(false);
        let result = install(&cfg);
        match result {
            Ok(guards) => {
                assert!(guards.otlp.is_none());
                let _keep_log_guard_alive = &guards.log;
            }
            Err(GatewayError::Internal(_)) => {}
            Err(e) => panic!("不应出现非 Internal 错误: {e}"),
        }
    }
}
