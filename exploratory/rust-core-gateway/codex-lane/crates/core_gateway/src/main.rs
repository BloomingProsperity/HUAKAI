use core_gateway::{config::StartupConfig, error::GatewayError, tracing_init};

fn main() -> Result<(), GatewayError> {
    let config = StartupConfig::from_env()?;

    tracing_init::install(&config)?;

    // 先同步解析配置，再用配置值显式固定 Tokio worker 数。
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(config.worker_threads)
        .thread_name("huakai-core-gateway")
        .enable_all()
        .build()?;

    runtime.block_on(core_gateway::run(config))
}
