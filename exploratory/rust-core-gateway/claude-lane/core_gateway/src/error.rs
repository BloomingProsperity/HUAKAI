// 顶层错误类型 — 覆盖网关所有子系统
// 使用 thiserror 避免手写 Display/From 样板代码

use thiserror::Error;

/// 网关顶层错误枚举
/// 每个 variant 对应一个子系统, 便于 metrics label 和 attempt report 的 error_class 映射
#[derive(Debug, Error)]
pub enum GatewayError {
    /// 配置解析或验证失败
    #[error("配置错误: {0}")]
    Config(String),

    /// 网络层错误 (TCP connect / TLS handshake / DNS 解析)
    #[error("网络错误: {0}")]
    Network(String),

    /// 上游供应商返回非预期响应 (4xx/5xx 或协议不合规)
    #[error("上游错误: {0}")]
    Upstream(String),

    /// 与 Go control plane 通信失败
    #[error("控制面错误: {0}")]
    ControlPlane(String),

    /// 流式帧解析或协议错误 (SSE / EventStream CRC 失败等)
    #[error("流式错误: {0}")]
    Stream(String),

    /// 内部逻辑错误 (不应发生的状态, 用于 panic 替代)
    #[error("内部错误: {0}")]
    Internal(String),
}

impl GatewayError {
    /// 返回用于 metrics/attempt_report 的 error_class 字符串
    /// 字面值保持英文, 与 Go 侧 AttemptReport.error_class 对齐
    pub fn error_class(&self) -> &'static str {
        match self {
            GatewayError::Config(_) => "config",
            GatewayError::Network(_) => "network_error",
            GatewayError::Upstream(_) => "upstream_error",
            GatewayError::ControlPlane(_) => "control_plane_error",
            GatewayError::Stream(_) => "protocol_error",
            GatewayError::Internal(_) => "internal_error",
        }
    }
}

/// 从 figment 配置错误自动转换 (boxed 版本, 与 config::from_env 返回类型一致)
impl From<Box<figment::Error>> for GatewayError {
    fn from(e: Box<figment::Error>) -> Self {
        GatewayError::Config(e.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn error_class_labels_are_correct() {
        assert_eq!(GatewayError::Config("x".into()).error_class(), "config");
        assert_eq!(
            GatewayError::Network("x".into()).error_class(),
            "network_error"
        );
        assert_eq!(
            GatewayError::Upstream("x".into()).error_class(),
            "upstream_error"
        );
        assert_eq!(
            GatewayError::ControlPlane("x".into()).error_class(),
            "control_plane_error"
        );
        assert_eq!(
            GatewayError::Stream("x".into()).error_class(),
            "protocol_error"
        );
        assert_eq!(
            GatewayError::Internal("x".into()).error_class(),
            "internal_error"
        );
    }

    #[test]
    fn error_display_contains_message() {
        let e = GatewayError::Config("缺少必填字段".into());
        assert!(e.to_string().contains("缺少必填字段"));
    }
}
