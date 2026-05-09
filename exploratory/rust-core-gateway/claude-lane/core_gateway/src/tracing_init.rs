// tracing 初始化模块
// 支持: JSON 结构化日志 + OTLP export stub
// OTLP 端点缺失时跳过导出, 不报错

use opentelemetry::trace::TracerProvider as _;
use opentelemetry_otlp::WithExportConfig;
use opentelemetry_sdk::runtime::Tokio;
use tracing_subscriber::{fmt, layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

/// 初始化 tracing subscriber
///
/// - `log_level`: EnvFilter 字符串, 如 "info" 或 "warn,core_gateway=debug"
/// - `otlp_endpoint`: 可选 OTLP gRPC 端点, None 时只输出本地 JSON 日志
///
/// 返回 OTLP tracer provider 的 shutdown guard; 调用者持有到进程退出
pub fn init(
    log_level: &str,
    otlp_endpoint: Option<&str>,
) -> Option<opentelemetry_sdk::trace::TracerProvider> {
    let env_filter = EnvFilter::try_new(log_level).unwrap_or_else(|_| EnvFilter::new("info"));

    // JSON 格式日志层 — 静态字段名, 避免运行期字符串分配
    let fmt_layer = fmt::layer()
        .json()
        .with_current_span(true)
        .with_span_list(false) // 减少日志体积
        .with_target(true);

    // 如果提供了 OTLP 端点, 则初始化 OTLP tracer
    let (otel_layer, provider) = match otlp_endpoint {
        Some(endpoint) => {
            match build_otlp_provider(endpoint) {
                Ok(provider) => {
                    let tracer = provider.tracer("core_gateway");
                    let layer = tracing_opentelemetry::layer().with_tracer(tracer);
                    (Some(layer), Some(provider))
                }
                Err(e) => {
                    // OTLP 初始化失败不阻断启动, 记录警告后退化到纯日志
                    eprintln!("[tracing_init] OTLP 初始化失败, 退化到本地日志: {e}");
                    (None, None)
                }
            }
        }
        None => (None, None),
    };

    // 组合所有层并安装为全局 subscriber
    let registry = tracing_subscriber::registry()
        .with(env_filter)
        .with(fmt_layer);

    if let Some(layer) = otel_layer {
        registry.with(layer).init();
    } else {
        registry.init();
    }

    provider
}

/// 构建 OTLP gRPC tracer provider
fn build_otlp_provider(
    endpoint: &str,
) -> Result<opentelemetry_sdk::trace::TracerProvider, opentelemetry::trace::TraceError> {
    let exporter = opentelemetry_otlp::SpanExporter::builder()
        .with_tonic()
        .with_endpoint(endpoint)
        .build()?;

    let provider = opentelemetry_sdk::trace::TracerProvider::builder()
        .with_batch_exporter(exporter, Tokio)
        .build();

    Ok(provider)
}
