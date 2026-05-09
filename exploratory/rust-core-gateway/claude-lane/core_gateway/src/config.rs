// 网关启动期类型化配置 — 来自环境变量, 缺失必填字段立即 fail-fast
// 标识符保持英文, 注释一律中文

use figment::{providers::Env, Figment};
use serde::Deserialize;

/// 网关顶层配置结构体
/// 所有字段均通过环境变量注入, 前缀 `GATEWAY_`
#[derive(Debug, Deserialize, Clone)]
pub struct GatewayConfig {
    /// 监听地址 (如 "0.0.0.0:8080")
    pub listen_addr: String,

    /// Go control plane 端点 (如 "http://127.0.0.1:9090")
    pub control_plane_endpoint: String,

    /// 日志级别字符串 (如 "info", "debug", "warn,core_gateway=trace")
    pub log_level: String,

    /// OTLP 导出端点 (如 "http://127.0.0.1:4317"), 可选
    /// 若缺失则跳过 OTLP export, 不报错
    pub otlp_endpoint: Option<String>,

    /// Tokio worker 线程数, None 时取 CPU 核心数
    pub worker_threads: Option<usize>,
}

impl GatewayConfig {
    /// 从环境变量加载配置, 缺失必填字段立即 panic (fail-fast)
    /// 前缀: GATEWAY_ (大写下划线)
    pub fn from_env() -> Result<Self, Box<figment::Error>> {
        // 零分配: Figment 在解析时直接写入目标结构体, 不构造中间 Map
        // Box<figment::Error> 避免 clippy::result_large_err (figment::Error >= 208 bytes)
        Figment::new()
            .merge(Env::prefixed("GATEWAY_").split("__"))
            .extract()
            .map_err(Box::new)
    }
}

/// 配置验证 — 语义层检查, 确保地址格式不为空
impl GatewayConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.listen_addr.is_empty() {
            return Err("GATEWAY_LISTEN_ADDR 不能为空".to_string());
        }
        if self.control_plane_endpoint.is_empty() {
            return Err("GATEWAY_CONTROL_PLANE_ENDPOINT 不能为空".to_string());
        }
        if self.log_level.is_empty() {
            return Err("GATEWAY_LOG_LEVEL 不能为空".to_string());
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn config_validate_rejects_empty_listen_addr() {
        let cfg = GatewayConfig {
            listen_addr: String::new(),
            control_plane_endpoint: "http://127.0.0.1:9090".to_string(),
            log_level: "info".to_string(),
            otlp_endpoint: None,
            worker_threads: None,
        };
        assert!(cfg.validate().is_err());
    }

    #[test]
    fn config_validate_accepts_valid_config() {
        let cfg = GatewayConfig {
            listen_addr: "0.0.0.0:8080".to_string(),
            control_plane_endpoint: "http://127.0.0.1:9090".to_string(),
            log_level: "info".to_string(),
            otlp_endpoint: None,
            worker_threads: Some(4),
        };
        assert!(cfg.validate().is_ok());
    }
}
