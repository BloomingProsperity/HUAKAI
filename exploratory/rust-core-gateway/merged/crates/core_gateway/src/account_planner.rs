// M-rust-5 account planner
// 职责: 将 listener 请求映射为 route query, 校验 per-attempt plan, 并维护 attempt 状态机。

use std::{
    fmt,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use bytes::Bytes;
use http::{HeaderMap, Uri, header::ACCEPT};
use thiserror::Error;
use uuid::Uuid;

use serde::Deserialize;

use crate::{
    error::GatewayError,
    redaction::redact_acquisition_token,
    request_id::RequestId,
    route_client::RouteClient,
    route_proto::v1::{RoutePlan, RouteQueryRequest},
};

const DEFAULT_CLIENT_DEADLINE_MS: u64 = 30_000;
const TENANT_ID_HEADER: &str = "x-tenant-id";
const REQUESTED_MODEL_HEADER: &str = "x-huakai-model";
const SESSION_HASH_HEADER: &str = "x-huakai-session-hash";
const STREAM_HEADER: &str = "x-huakai-stream";

#[derive(Clone)]
pub struct AccountPlanner {
    inner: Arc<AccountPlannerInner>,
}

struct AccountPlannerInner {
    route_client: RouteClient,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum GatewayProtocol {
    AnthropicMessages,
    OpenAiChatCompletions,
}

/// W11-A D-1a: 从客户端 bounded JSON 请求体抽出 routing signal,
/// 让控制面以 body 为权威, 防 header 单独伪造模型/流式信号绕开计费/路由。
///
/// `model` 与 `stream` 来自 OpenAI Chat Completions / Anthropic Messages 通用契约;
/// 其他字段一律忽略 (避免 Rust 端解析负担)。
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct BodyRouteSignal {
    pub model: Option<String>,
    pub stream: Option<bool>,
}

/// P2 codex 2026-05-23: model 字符串若被客户端塞超大, 会原样进入控制面 RouteQueryRequest
/// 导致 RPC payload 膨胀 + 日志 / 缓存 key 污染。OpenAI / Anthropic 实际模型名 <100 字符,
/// 256 足够覆盖任何合理 vendor 命名 + 防注入超长值。
const MAX_ROUTE_SIGNAL_MODEL_LEN: usize = 256;

impl BodyRouteSignal {
    /// 解析 body 字节; 任意 IO / JSON / 类型错误一律 swallow 返回 default (无信号),
    /// 由调用方决定是否走 header / "unknown" fallback。
    /// 调用方必须已对 body 长度做 bounded 限制 (max_body_bytes), 本函数不再二次检查。
    /// model 字段额外 bounded MAX_ROUTE_SIGNAL_MODEL_LEN, 超长 / 空 → 视为缺失。
    pub fn from_json_body(body: &[u8]) -> Self {
        #[derive(Deserialize)]
        struct RoutingPayload {
            #[serde(default)]
            model: Option<String>,
            #[serde(default)]
            stream: Option<bool>,
        }

        serde_json::from_slice::<RoutingPayload>(body)
            .map(|p| Self {
                model: p
                    .model
                    .filter(|m| !m.is_empty() && m.len() <= MAX_ROUTE_SIGNAL_MODEL_LEN),
                stream: p.stream,
            })
            .unwrap_or_default()
    }
}

impl GatewayProtocol {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::AnthropicMessages => "anthropic_messages",
            Self::OpenAiChatCompletions => "openai_chat_completions",
        }
    }
}

#[derive(Debug, Error)]
pub enum PlanningError {
    #[error("route query failed: {0}")]
    ControlPlane(#[from] GatewayError),
    #[error("invalid route plan: {0}")]
    InvalidRoutePlan(String),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthMode {
    Bearer,
}

impl AuthMode {
    pub fn parse(raw: &str) -> Result<Self, PlanningError> {
        if raw.eq_ignore_ascii_case("bearer") {
            Ok(Self::Bearer)
        } else {
            Err(PlanningError::InvalidRoutePlan(format!(
                "unsupported auth_mode {raw:?}"
            )))
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AttemptState {
    Planned,
    Forwarding,
    Reporting,
    Done,
    Failed,
}

#[derive(Clone, Debug)]
pub struct AttemptLifecycle {
    attempt_id: String,
    state: AttemptState,
}

impl AttemptLifecycle {
    pub fn new(attempt_id: String) -> Self {
        Self {
            attempt_id,
            state: AttemptState::Planned,
        }
    }

    pub fn attempt_id(&self) -> &str {
        &self.attempt_id
    }

    pub fn state(&self) -> AttemptState {
        self.state
    }

    pub fn mark_forwarding(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Planned, AttemptState::Forwarding)
    }

    pub fn mark_reporting(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Forwarding, AttemptState::Reporting)
    }

    pub fn mark_done(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Reporting, AttemptState::Done)
    }

    pub fn mark_failed(&mut self) -> Result<(), GatewayError> {
        match self.state {
            AttemptState::Planned | AttemptState::Forwarding | AttemptState::Reporting => {
                self.state = AttemptState::Failed;
                Ok(())
            }
            AttemptState::Done | AttemptState::Failed => Err(GatewayError::Internal(format!(
                "invalid attempt transition {:?} -> Failed",
                self.state
            ))),
        }
    }

    fn transition(
        &mut self,
        expected: AttemptState,
        next: AttemptState,
    ) -> Result<(), GatewayError> {
        if self.state == expected {
            self.state = next;
            Ok(())
        } else {
            Err(GatewayError::Internal(format!(
                "invalid attempt transition {:?} -> {:?}",
                self.state, next
            )))
        }
    }
}

#[derive(Clone)]
pub struct PlannedAttempt {
    pub route_plan: RoutePlan,
    pub account_id: String,
    pub acquisition_token: Bytes,
    pub vendor_endpoint: Uri,
    pub auth_mode: AuthMode,
    pub attempt: AttemptLifecycle,
}

impl fmt::Debug for PlannedAttempt {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("PlannedAttempt")
            .field("route_plan", &self.route_plan)
            .field("account_id", &self.account_id)
            .field(
                "acquisition_token",
                &redact_acquisition_token(self.acquisition_token.as_ref()),
            )
            .field("vendor_endpoint", &self.vendor_endpoint)
            .field("auth_mode", &self.auth_mode)
            .field("attempt", &self.attempt)
            .finish()
    }
}

impl AccountPlanner {
    pub fn new(route_client: RouteClient) -> Self {
        Self {
            inner: Arc::new(AccountPlannerInner { route_client }),
        }
    }

    pub fn route_client(&self) -> &RouteClient {
        &self.inner.route_client
    }

    pub async fn plan(
        &self,
        headers: &HeaderMap,
        protocol: GatewayProtocol,
        request_id: &RequestId,
        body_signal: &BodyRouteSignal,
    ) -> Result<PlannedAttempt, PlanningError> {
        let query = build_route_query(headers, protocol, request_id, body_signal);
        let plan = self.inner.route_client.query_route(query).await?;

        planned_attempt(plan)
    }
}

pub fn build_route_query(
    headers: &HeaderMap,
    protocol: GatewayProtocol,
    request_id: &RequestId,
    body_signal: &BodyRouteSignal,
) -> RouteQueryRequest {
    RouteQueryRequest {
        request_id: request_id.as_str().to_owned(),
        tenant_id: header_str(headers, TENANT_ID_HEADER)
            .unwrap_or("default-tenant")
            .to_owned(),
        // W11-A D-1a: body 是 model 的权威来源, header 仅在 body 未提供时 legacy fallback。
        // mutation: 把下面这行改回 header_str(...) 优先 →
        // build_route_query_body_model_wins_over_header 测试红 (应红)。
        requested_model: body_signal
            .model
            .clone()
            .or_else(|| header_str(headers, REQUESTED_MODEL_HEADER).map(str::to_owned))
            .unwrap_or_else(|| "unknown".to_owned()),
        session_hash: header_str(headers, SESSION_HASH_HEADER)
            .unwrap_or(request_id.as_str())
            .to_owned(),
        request_protocol: protocol.as_str().to_owned(),
        // W11-A D-1a: body.stream 优先; 否则保留 wants_stream() (含 x-huakai-stream / Accept SSE)。
        stream: body_signal.stream.unwrap_or_else(|| wants_stream(headers)),
        client_deadline_ms: DEFAULT_CLIENT_DEADLINE_MS,
        previous_attempts: Vec::new(),
        capability_hints: Vec::new(),
    }
}

fn planned_attempt(plan: RoutePlan) -> Result<PlannedAttempt, PlanningError> {
    if plan.account_id.is_empty() {
        return Err(PlanningError::InvalidRoutePlan(
            "missing account_id".to_owned(),
        ));
    }

    if plan.acquisition_token.is_empty() {
        return Err(PlanningError::InvalidRoutePlan(
            "missing acquisition_token".to_owned(),
        ));
    }

    if plan.credentials_handle.is_empty() {
        return Err(PlanningError::InvalidRoutePlan(
            "missing credentials_handle".to_owned(),
        ));
    }

    let vendor_endpoint = plan
        .vendor_endpoint
        .parse::<Uri>()
        .map_err(|err| PlanningError::InvalidRoutePlan(format!("bad vendor_endpoint: {err}")))?;

    if vendor_endpoint.scheme().is_none() || vendor_endpoint.authority().is_none() {
        return Err(PlanningError::InvalidRoutePlan(
            "vendor_endpoint missing scheme or authority".to_owned(),
        ));
    }

    let auth_mode = AuthMode::parse(&plan.auth_mode)?;
    validate_upstream_auth_material(&plan, auth_mode)?;
    let attempt_id = format!("attempt-{}", Uuid::now_v7());

    Ok(PlannedAttempt {
        account_id: plan.account_id.clone(),
        acquisition_token: plan.acquisition_token.clone(),
        vendor_endpoint,
        auth_mode,
        route_plan: plan,
        attempt: AttemptLifecycle::new(attempt_id),
    })
}

fn validate_upstream_auth_material(
    plan: &RoutePlan,
    auth_mode: AuthMode,
) -> Result<(), PlanningError> {
    match auth_mode {
        AuthMode::Bearer => {
            let upstream_auth = plan.upstream_auth.as_ref().ok_or_else(|| {
                PlanningError::InvalidRoutePlan("missing upstream_auth".to_owned())
            })?;
            if upstream_auth.material_kind != "bearer_token" {
                return Err(PlanningError::InvalidRoutePlan(format!(
                    "unsupported upstream_auth.material_kind {:?}",
                    upstream_auth.material_kind
                )));
            }
            if upstream_auth.material.is_empty() {
                return Err(PlanningError::InvalidRoutePlan(
                    "missing upstream_auth.material".to_owned(),
                ));
            }
            let material = std::str::from_utf8(upstream_auth.material.as_ref()).map_err(|err| {
                PlanningError::InvalidRoutePlan(format!(
                    "upstream_auth.material must be utf8: {err}"
                ))
            })?;
            if material.trim().as_bytes() != upstream_auth.material.as_ref()
                || material.chars().any(char::is_control)
            {
                return Err(PlanningError::InvalidRoutePlan(
                    "upstream_auth.material must not contain leading, trailing, or control whitespace"
                        .to_owned(),
                ));
            }
            if upstream_auth.material == plan.acquisition_token {
                return Err(PlanningError::InvalidRoutePlan(
                    "upstream_auth.material must differ from acquisition_token".to_owned(),
                ));
            }
            if material.as_bytes().trim_ascii() == plan.acquisition_token.as_ref().trim_ascii() {
                return Err(PlanningError::InvalidRoutePlan(
                    "upstream_auth.material must differ from acquisition_token after trim"
                        .to_owned(),
                ));
            }
            if upstream_auth.expires_at_unix_ms != 0
                && upstream_auth.expires_at_unix_ms < now_unix_ms()
            {
                return Err(PlanningError::InvalidRoutePlan(
                    "upstream_auth.material expired".to_owned(),
                ));
            }
        }
    }

    Ok(())
}

fn header_str<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name).and_then(|value| value.to_str().ok())
}

fn wants_stream(headers: &HeaderMap) -> bool {
    header_str(headers, STREAM_HEADER).is_some_and(|value| {
        value.eq_ignore_ascii_case("true") || value == "1" || value.eq_ignore_ascii_case("yes")
    }) || headers
        .get(ACCEPT)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.contains("text/event-stream"))
}

fn now_unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn attempt_lifecycle_accepts_happy_path() {
        let mut attempt = AttemptLifecycle::new("attempt-test".to_owned());
        assert_eq!(attempt.state(), AttemptState::Planned);

        attempt.mark_forwarding().unwrap();
        attempt.mark_reporting().unwrap();
        attempt.mark_done().unwrap();

        assert_eq!(attempt.state(), AttemptState::Done);
    }

    #[test]
    fn attempt_lifecycle_rejects_out_of_order_transition() {
        let mut attempt = AttemptLifecycle::new("attempt-test".to_owned());
        assert!(attempt.mark_reporting().is_err());
        assert_eq!(attempt.state(), AttemptState::Planned);
    }

    #[test]
    fn planned_attempt_debug_redacts_nested_and_local_tokens() {
        let plan = RoutePlan {
            route_plan_id: "route-plan-redact-1".to_owned(),
            account_id: "account-redact-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-mock".to_owned(),
            vendor_endpoint: "https://api.anthropic.com".to_owned(),
            credentials_handle: "credential-handle-mock-1".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: 1000,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 4 * 1024 * 1024,
            max_stream_frame_bytes: 64 * 1024,
            upstream_auth: Some(crate::route_proto::v1::UpstreamAuthMaterial {
                material_kind: "bearer_token".to_owned(),
                material: Bytes::from_static(b"upstream-secret-mock-1"),
                header_name: "authorization".to_owned(),
                expires_at_unix_ms: 0,
            }),
        };

        let planned = planned_attempt(plan).expect("测试 RoutePlan 应合法");
        let debug = format!("{:?}", planned);

        assert!(!debug.contains("lease-token-mock-1"));
        assert!(!debug.contains("upstream-secret-mock-1"));
        assert!(!debug.contains("credential-handle-mock-1"));
        assert!(debug.contains("[ACQUISITION_TOKEN_REDACTED]"));
        assert!(debug.contains("[UPSTREAM_AUTH_MATERIAL_REDACTED]"));
        assert!(debug.contains("[CREDENTIAL_HANDLE_REDACTED]"));
        assert!(debug.contains("route-plan-redact-1"));
        assert!(debug.contains("account-redact-1"));
        assert!(debug.contains("anthropic"));
    }

    #[test]
    fn planned_attempt_rejects_trimmed_material_reusing_acquisition_token() {
        let mut plan = valid_route_plan_for_auth();
        plan.acquisition_token = Bytes::from_static(b"lease-token");
        plan.upstream_auth.as_mut().unwrap().material = Bytes::from_static(b" lease-token\n");

        let err = planned_attempt(plan).expect_err("planner 必须先拒绝 trim 绕过");

        assert!(matches!(err, PlanningError::InvalidRoutePlan(_)));
    }

    #[test]
    fn planned_attempt_rejects_material_with_embedded_control_character() {
        let mut plan = valid_route_plan_for_auth();
        plan.upstream_auth.as_mut().unwrap().material = Bytes::from_static(b"upstream-\x1fsecret");

        let err = planned_attempt(plan).expect_err("planner 必须拒绝内嵌控制字符");

        assert!(matches!(err, PlanningError::InvalidRoutePlan(_)));
    }

    fn valid_route_plan_for_auth() -> RoutePlan {
        RoutePlan {
            route_plan_id: "route-plan-auth-test".to_owned(),
            account_id: "account-auth-test".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-auth-test"),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-test".to_owned(),
            vendor_endpoint: "https://api.anthropic.com".to_owned(),
            credentials_handle: "credential-auth-test".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: 0,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 1024 * 1024,
            max_stream_frame_bytes: 64 * 1024,
            upstream_auth: Some(crate::route_proto::v1::UpstreamAuthMaterial {
                material_kind: "bearer_token".to_owned(),
                material: Bytes::from_static(b"upstream-secret-auth-test"),
                header_name: String::new(),
                expires_at_unix_ms: 0,
            }),
        }
    }

    // ---------- W11-A D-1a tests ----------

    /// 关键判别性: body 提供 model + stream → 必须覆盖同时存在的 header 值。
    /// mutation: 改 build_route_query 回 header 优先 → assert_eq! requested_model 红。
    #[test]
    fn build_route_query_body_model_wins_over_header() {
        let mut headers = HeaderMap::new();
        headers.insert(REQUESTED_MODEL_HEADER, "cheap-header-model".parse().unwrap());
        headers.insert(STREAM_HEADER, "false".parse().unwrap());

        let body_signal = BodyRouteSignal {
            model: Some("claude-real-from-body".to_owned()),
            stream: Some(true),
        };
        let request_id = RequestId::generate();

        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal,
        );

        assert_eq!(
            q.requested_model, "claude-real-from-body",
            "body.model 必须比 x-huakai-model header 权威 (D-1a 防 header 篡改路由)"
        );
        assert!(
            q.stream,
            "body.stream=true 必须比 x-huakai-stream=false header 权威"
        );
    }

    /// 无 body signal 时退回 header 是兼容路径; 不破坏既有客户端。
    #[test]
    fn build_route_query_falls_back_to_header_when_body_signal_missing() {
        let mut headers = HeaderMap::new();
        headers.insert(REQUESTED_MODEL_HEADER, "header-model".parse().unwrap());
        headers.insert(STREAM_HEADER, "true".parse().unwrap());

        let body_signal = BodyRouteSignal::default();
        let q = build_route_query(
            &headers,
            GatewayProtocol::OpenAiChatCompletions,
            &RequestId::generate(),
            &body_signal,
        );

        assert_eq!(
            q.requested_model, "header-model",
            "无 body.model 时 header x-huakai-model 仍可 fallback"
        );
        assert!(
            q.stream,
            "无 body.stream 时 header x-huakai-stream=true 仍生效"
        );
    }

    #[test]
    fn build_route_query_unknown_when_no_model_anywhere() {
        let headers = HeaderMap::new();
        let body_signal = BodyRouteSignal::default();
        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
        );
        assert_eq!(q.requested_model, "unknown");
        assert!(!q.stream);
    }

    #[test]
    fn body_route_signal_extracts_model_and_stream_from_json() {
        let body = br#"{"model":"claude-3-5-sonnet","stream":true,"messages":[]}"#;
        let signal = BodyRouteSignal::from_json_body(body);
        assert_eq!(signal.model.as_deref(), Some("claude-3-5-sonnet"));
        assert_eq!(signal.stream, Some(true));
    }

    #[test]
    fn body_route_signal_default_when_body_is_invalid_json() {
        let body = b"not a valid json {";
        let signal = BodyRouteSignal::from_json_body(body);
        assert!(
            signal.model.is_none(),
            "malformed body 不能让 model 被部分污染"
        );
        assert!(signal.stream.is_none());
    }

    #[test]
    fn body_route_signal_default_when_routing_fields_missing() {
        let body = br#"{"messages":[{"role":"user","content":"hi"}]}"#;
        let signal = BodyRouteSignal::from_json_body(body);
        assert!(signal.model.is_none());
        assert!(signal.stream.is_none());
    }

    #[test]
    fn body_route_signal_empty_model_string_treated_as_missing() {
        let body = br#"{"model":"","stream":false}"#;
        let signal = BodyRouteSignal::from_json_body(body);
        assert!(
            signal.model.is_none(),
            "空字符串 model 不能让 build_route_query 返回 requested_model=\"\""
        );
        assert_eq!(signal.stream, Some(false));
    }

    /// P2 codex 2026-05-23: 防客户端塞超大 model 污染 control plane RouteQueryRequest。
    /// mutation: 删 `m.len() <= MAX_ROUTE_SIGNAL_MODEL_LEN` filter → 此测试断言红。
    /// 实测 MAX = 256, OpenAI / Anthropic 模型名实际 <100 字节足够。
    #[test]
    fn body_route_signal_drops_oversized_model_to_prevent_payload_pollution() {
        let huge_model = "a".repeat(300); // > MAX_ROUTE_SIGNAL_MODEL_LEN (256)
        let body = format!(r#"{{"model":"{huge_model}","stream":true}}"#);
        let signal = BodyRouteSignal::from_json_body(body.as_bytes());
        assert!(
            signal.model.is_none(),
            "超长 model (>256 字节) 必须被丢弃, 实际: {:?}",
            signal.model.as_deref().map(str::len)
        );
        assert_eq!(signal.stream, Some(true), "其他字段不应受 model 超限影响");
    }

    #[test]
    fn body_route_signal_accepts_model_at_boundary_length() {
        let boundary_model = "a".repeat(256); // exactly MAX_ROUTE_SIGNAL_MODEL_LEN
        let body = format!(r#"{{"model":"{boundary_model}"}}"#);
        let signal = BodyRouteSignal::from_json_body(body.as_bytes());
        assert_eq!(
            signal.model.as_deref().map(str::len),
            Some(256),
            "刚好 256 字节 model 应通过 (边界 inclusive)"
        );
    }
}
