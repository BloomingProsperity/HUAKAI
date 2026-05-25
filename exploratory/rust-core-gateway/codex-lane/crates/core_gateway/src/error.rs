use thiserror::Error;

#[derive(Debug, Error)]
pub enum GatewayError {
    #[error("config error: {0}")]
    Config(String),
    #[error("network error: {0}")]
    Network(#[from] std::io::Error),
    #[error("upstream error: {0}")]
    Upstream(String),
    #[error("control plane error: {0}")]
    ControlPlane(String),
    #[error("stream error: {0}")]
    Stream(String),
    #[error("internal error: {0}")]
    Internal(String),
}
