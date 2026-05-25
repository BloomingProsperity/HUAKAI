// HUAKAI Rust Core Gateway — M-rust-1 启动入口
// 职责: 加载配置 / 初始化 tracing / 启动 minimal axum health endpoint
// 不实现业务 forwarding (M-rust-2+)

use core_gateway::{config::StartupConfig, error::GatewayError, tracing_init};

fn main() -> Result<(), GatewayError> {
    // 先同步解析配置, 缺失必填项立即 fail-fast
    let config = StartupConfig::from_env()?;

    // 初始化 tracing (非阻塞 stdout 日志 + 可选 OTLP batch exporter)。
    // guard 绑定在 main 作用域内, 确保进程退出前日志 worker 不会提前停止。
    let _tracing_guards = tracing_init::install(&config)?;

    // 用配置值显式固定 Tokio worker 线程数
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(config.worker_threads)
        .thread_name("huakai-core-gateway")
        .enable_all()
        .build()?;

    runtime.block_on(core_gateway::run(config))
}
