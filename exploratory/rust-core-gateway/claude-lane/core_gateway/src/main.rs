// HUAKAI Rust Core Gateway — M-rust-1 启动入口
// 职责: 加载配置 / 初始化 tracing / 启动 minimal axum health endpoint
// 不实现业务 forwarding (M-rust-2+)

use std::net::SocketAddr;
use std::sync::Arc;

use axum::{extract::State, routing::get, Json, Router};
use bytes::Bytes;
use serde_json::json;
use tokio::runtime::Builder;
use tracing::info;

use core_gateway::config::GatewayConfig;
use core_gateway::error::GatewayError;
use core_gateway::request_id::new_request_id;
use core_gateway::tracing_init;

/// 共享应用状态 — 通过 axum::State 注入, 不用全局 Mutex
/// M-rust-2+ 会在 handler 中读取 config 字段
#[derive(Clone)]
struct AppState {
    /// 只读配置快照, 启动后不变 (M-rust-2+ handler 会使用)
    #[allow(dead_code)]
    config: Arc<GatewayConfig>,
}

fn main() {
    // 从环境变量加载配置, 缺失必填字段立即退出 (fail-fast)
    let config = GatewayConfig::from_env().unwrap_or_else(|e| {
        eprintln!("[main] 配置加载失败: {e}");
        std::process::exit(1);
    });

    if let Err(e) = config.validate() {
        eprintln!("[main] 配置验证失败: {e}");
        std::process::exit(1);
    }

    // 根据配置决定 worker 线程数 (D-rust-3: 启动期 pin worker_threads)
    let worker_threads = config.worker_threads.unwrap_or_else(num_cpus);

    // 构建 tokio 多线程运行时并 pin worker 数
    let rt = Builder::new_multi_thread()
        .worker_threads(worker_threads)
        .enable_all()
        .thread_name("gateway-worker")
        .build()
        .expect("无法构建 tokio runtime");

    rt.block_on(async_main(config));
}

/// 异步主逻辑 — 在 tokio runtime 内运行
async fn async_main(config: GatewayConfig) {
    // 初始化 tracing (JSON 日志 + 可选 OTLP)
    let _otlp_guard = tracing_init::init(&config.log_level, config.otlp_endpoint.as_deref());

    // 记录启动信息 (静态 field 名, 不做运行期字符串拼接)
    let node_id = new_request_id();
    info!(
        node_id = %node_id,
        listen_addr = %config.listen_addr,
        control_plane = %config.control_plane_endpoint,
        "core_gateway 启动"
    );

    let state = AppState {
        config: Arc::new(config.clone()),
    };

    // 路由表 — M-rust-1 只暴露 health endpoint
    let app: Router = Router::new()
        .route("/healthz", get(health_handler))
        .with_state(state);

    // 解析监听地址
    let addr: SocketAddr = config.listen_addr.parse().unwrap_or_else(|e| {
        tracing::error!("listen_addr 解析失败: {e}");
        std::process::exit(1);
    });

    info!(addr = %addr, "开始监听");

    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .unwrap_or_else(|e| {
            tracing::error!("bind 失败: {e}");
            std::process::exit(1);
        });

    axum::serve(listener, app)
        .await
        .expect("HTTP server 退出异常");
}

/// GET /healthz — 返回 {"status":"ok"}
/// 使用 axum::State 注入共享状态, 不依赖全局变量
async fn health_handler(State(_state): State<AppState>) -> Json<serde_json::Value> {
    Json(json!({"status": "ok"}))
}

/// 获取 CPU 核心数 (逻辑核), 用于默认 worker_threads
fn num_cpus() -> usize {
    std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(2)
}

// 确保 Bytes / GatewayError 引用在编译期可见 (避免 dead_code 警告)
const _: fn() = || {
    let _: Bytes = Bytes::new();
    let _: GatewayError = GatewayError::Internal("unused".into());
};
