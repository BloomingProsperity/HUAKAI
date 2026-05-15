use serde::Deserialize;
use serde_json::Value;

/// HTTP/1 业务请求层字段；header_order 保留模板中的真实大小写。
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HttpLayerProfile {
    pub protocol: String,
    pub endpoint: String,
    pub method: String,
    pub user_agent: String,
    pub header_order: Vec<String>,
    pub auth_mechanism: String,
    pub refresh_endpoint: String,
    #[serde(default)]
    pub source_note: Option<String>,
    #[serde(default)]
    pub x_amz_target: Option<String>,
    #[serde(default)]
    pub content_type: Option<String>,
    #[serde(default)]
    pub x_amz_user_agent: Option<String>,
    #[serde(default)]
    pub x_goog_api_client: Option<String>,
    #[serde(default)]
    pub accept: Option<String>,
    #[serde(default)]
    pub accept_encoding: Option<String>,
    #[serde(default)]
    pub connection: Option<String>,
    #[serde(default)]
    pub body_shape: Option<String>,
    #[serde(default)]
    pub auxiliary_endpoints: Vec<String>,
}

/// 当前模板只记录 SETTINGS 可用性和限制说明；真实 SETTINGS 值后续由 capture gate 扩展。
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Http2SettingsCapture {
    pub available: bool,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub settings: Vec<Value>,
    #[serde(default)]
    pub limitation_note: Option<String>,
}

/// Auth 层只保存脱敏后的鉴权形态和 token 来源说明。
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthLayerProfile {
    pub mechanism: String,
    pub authorization_header: String,
    #[serde(default)]
    pub account_header: Option<String>,
    #[serde(default)]
    pub conditional_headers: Vec<String>,
    #[serde(default)]
    pub refresh_endpoint: Option<String>,
    #[serde(default)]
    pub token_source: Option<String>,
    #[serde(default)]
    pub model_api_token_length: Option<String>,
    #[serde(default)]
    pub telemetry_mechanism: Option<String>,
}
