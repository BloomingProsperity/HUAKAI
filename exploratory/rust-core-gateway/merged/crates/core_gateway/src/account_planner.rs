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
    client_auth::{ClientCredentialKind, RouteIdentity},
    config::ReconcilePolicy,
    error::GatewayError,
    metrics::client_credential_tenant_reconcile_total,
    redaction::redact_acquisition_token,
    request_id::RequestId,
    route_client::RouteClient,
    route_proto::v1::{RoutePlan, RouteQueryRequest},
};
use tracing::warn;

const DEFAULT_CLIENT_DEADLINE_MS: u64 = 30_000;
// W11-A D-1b Phase 1 A3 acceptance gate (synthesis §7-H, 2026-05-24):
// `x-tenant-id` 在 Rust 数据面所有路径下**永不被信任** — tenant 一律由控制面
// 派生 (Phase 2+) 或 Manual First 静态 hash 兜底 (Phase 1, identity.manual_first_tenant_id)。
// 旧 TENANT_ID_HEADER 读取已删 — 任何在本文件或 listener 添加 `x-tenant-id` 读取
// 的 PR 必被 reviewer 拒。mutation: 重新引入 const + header_str(headers, "x-tenant-id")
// 写入 tenant_id → x_tenant_id_header_never_trusted_in_d1b 测试红。
const REQUESTED_MODEL_HEADER: &str = "x-huakai-model";
const SESSION_HASH_HEADER: &str = "x-huakai-session-hash";
const STREAM_HEADER: &str = "x-huakai-stream";

#[derive(Clone)]
pub struct AccountPlanner {
    inner: Arc<AccountPlannerInner>,
}

struct AccountPlannerInner {
    route_client: RouteClient,
    /// W11-A D-1b Phase 2A.4 (D-14 (a) Owner-approved, 2026-05-24): how to
    /// resolve disagreement between Manual First tenant (Phase 1 兜底) and
    /// Go control plane derived tenant (Phase 2A authoritative). Default
    /// FailClosed (synthesis §4 D-14 a); LogOnly only for staged rollout.
    reconcile_policy: ReconcilePolicy,
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
    /// W11-A D-1b Phase 2A.4 (D-14 (a) FailClosed, 2026-05-24): Manual First
    /// tenant disagrees with the Go control plane derived tenant. listener.rs
    /// maps this to 401 + `tenant_id_mismatch` error envelope rather than the
    /// 502 InvalidRoutePlan path — the disagreement is an identity-level event
    /// (one of the two parties is wrong about who the client is), not a
    /// malformed plan from a healthy control plane.
    ///
    /// PII discipline: kind / both tenant_id values are routing metadata
    /// (not credential material), so embedding them in the message is safe.
    /// The raw credential never appears here — credential is referenced by
    /// fingerprint in tracing warn() at the call site.
    #[error("tenant_id mismatch: kind={kind} manual_first={manual_first:?} go_derived={go_derived:?}")]
    TenantIdMismatch {
        kind: String,
        manual_first: String,
        go_derived: String,
    },
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
    /// Construct AccountPlanner with explicit reconciliation policy.
    /// Production wiring (lib.rs) reads policy from StartupConfig.
    /// Test code passes `ReconcilePolicy::FailClosed` to mirror prod default
    /// unless the test specifically exercises the LogOnly arm.
    pub fn new(route_client: RouteClient, reconcile_policy: ReconcilePolicy) -> Self {
        Self {
            inner: Arc::new(AccountPlannerInner {
                route_client,
                reconcile_policy,
            }),
        }
    }

    pub fn route_client(&self) -> &RouteClient {
        &self.inner.route_client
    }

    /// W11-A D-1b Phase 2A.4: expose policy so tests + listener telemetry can
    /// branch on it (e.g., warn-only counter label vs reject path).
    pub fn reconcile_policy(&self) -> ReconcilePolicy {
        self.inner.reconcile_policy
    }

    pub async fn plan(
        &self,
        headers: &HeaderMap,
        protocol: GatewayProtocol,
        request_id: &RequestId,
        body_signal: &BodyRouteSignal,
        identity: &RouteIdentity,
    ) -> Result<PlannedAttempt, PlanningError> {
        let query = build_route_query(headers, protocol, request_id, body_signal, identity);
        let plan = self.inner.route_client.query_route(query).await?;

        let attempt = planned_attempt(plan)?;

        // W11-A D-1b Phase 2A.4 (D-14 (a) FailClosed default, 2026-05-24):
        // reconcile Manual First (Phase 1 兜底) against Go control plane
        // derived tenant. counter inc 在 reconcile_identity 内 (含 match /
        // mismatch / sole-Go / sole-Manual / none 五种 source label).
        reconcile_identity(
            &attempt.route_plan,
            identity,
            self.inner.reconcile_policy,
        )?;

        Ok(attempt)
    }
}

/// W11-A D-1b Phase 2A.4 reconciliation outcome — used as the `source` label
/// dimension on `huakai_client_credential_tenant_reconcile_total`. Five values
/// keep cardinality bounded (kind * source = 3 * 5 = 15 series).
fn reconcile_source_label(manual_first: &str, go_derived: &str, matches: bool) -> &'static str {
    match (manual_first.is_empty(), go_derived.is_empty(), matches) {
        (true, true, _) => "none",                 // anonymous (dev/test require_credential=false)
        (true, false, _) => "go_only",             // Phase 1 OFF + Phase 2 emits
        (false, true, _) => "manual_only",         // Phase 2 mock 不 emit (legacy)
        (false, false, true) => "both_match",      // 双写期 happy path
        (false, false, false) => "both_mismatch", // 双写期 fail-closed trigger
    }
}

/// Maps a [ClientCredentialKind] (or anonymous) into the `kind` label dimension.
/// Stable string — changing values would break Prometheus dashboards.
fn reconcile_kind_label(identity: &RouteIdentity) -> &'static str {
    match identity.client_credential.as_ref().map(|c| c.kind()) {
        Some(ClientCredentialKind::Bearer) => "bearer",
        Some(ClientCredentialKind::XApiKey) => "x-api-key",
        None => "none",
    }
}

/// W11-A D-1b Phase 2A.4 (D-14 (a) FailClosed default): reconcile Manual First
/// tenant against Go-derived tenant.
///
/// Outcomes:
///   - both empty → Ok (anonymous mode, counter source=none)
///   - manual only → Ok (Phase 2 mock period, counter source=manual_only)
///   - Go only → Ok (Phase 1 OFF + Phase 2 ON, counter source=go_only, Go authoritative)
///   - both, equal → Ok (counter source=both_match)
///   - both, unequal:
///       * policy=FailClosed (Owner default D-14 a) → Err(TenantIdMismatch), counter source=both_mismatch
///       * policy=LogOnly (staging only) → counter source=both_mismatch + warn + Ok (信 Go)
///
/// Mutation: any of (deleting counter inc, swapping label values, dropping
/// the FailClosed branch's Err, accepting empty derived as match) is caught
/// by one of the 5 tests in mod tests::reconcile_*.
fn reconcile_identity(
    plan: &RoutePlan,
    identity: &RouteIdentity,
    policy: ReconcilePolicy,
) -> Result<(), PlanningError> {
    // Ensure metrics registry init before incrementing — mirrors the pattern
    // metrics::set_inflight_requests uses; defensive against tests / early-
    // startup paths that reach plan() before any other metric write.
    let _ = crate::metrics::registry();

    let manual_first = identity.manual_first_tenant_id.as_deref().unwrap_or("");
    let go_derived = plan.derived_tenant_id.as_str();
    let matches = manual_first == go_derived;
    let source = reconcile_source_label(manual_first, go_derived, matches);
    let kind = reconcile_kind_label(identity);

    client_credential_tenant_reconcile_total()
        .with_label_values(&[kind, source])
        .inc();

    if source != "both_mismatch" {
        return Ok(());
    }

    // both non-empty + disagree: fail-closed by default, LogOnly only for staged rollout.
    let cred_fingerprint = identity
        .client_credential
        .as_ref()
        .map(|c| c.fingerprint().to_string())
        .unwrap_or_else(|| "[no-cred]".to_owned());

    warn!(
        kind = kind,
        manual_first = manual_first,
        go_derived = go_derived,
        cred_fingerprint = cred_fingerprint.as_str(),
        policy = ?policy,
        "tenant reconciliation mismatch (D-14 dual-write); \
         FailClosed → reject, LogOnly → trust Go derived"
    );

    if policy.fails_closed_on_mismatch() {
        return Err(PlanningError::TenantIdMismatch {
            kind: kind.to_owned(),
            manual_first: manual_first.to_owned(),
            go_derived: go_derived.to_owned(),
        });
    }

    // LogOnly path: counter already incremented, warn emitted, Go authoritative
    // assumed (listener observability records the kept identity from
    // identity.manual_first_tenant_id but plan.derived_tenant_id is what
    // downstream attempt_report will carry — Phase 3 dual-write removal target).
    Ok(())
}

/// 构造发给 control plane 的 RouteQueryRequest。
///
/// **W11-A D-1b Phase 1 acceptance gates** (synthesis §3):
/// - A3 `x-tenant-id` 永不被信任 — `tenant_id` 只有两个来源:
///   (a) `identity.manual_first_tenant_id` (Manual First ON + 命中) → 写 Some(value);
///   (b) 否则空字符串 `""` → 强制 Go control plane (Phase 2+) 派生 authoritative tenant。
///   绝对不读 `x-tenant-id` header。
/// - A4 raw credential 永不入 log — 直接写入 proto.client_credential (透传 control plane);
///   `Debug` impl 在 redacting_debug.rs 渲染为 fingerprint, A4 守门。
/// - A5 Manual First ON 双写 (新 client_credential + 旧 tenant_id) / OFF 强制 Go 派生
///   (新 client_credential + 空 tenant_id)。
pub fn build_route_query(
    headers: &HeaderMap,
    protocol: GatewayProtocol,
    request_id: &RequestId,
    body_signal: &BodyRouteSignal,
    identity: &RouteIdentity,
) -> RouteQueryRequest {
    RouteQueryRequest {
        request_id: request_id.as_str().to_owned(),
        // A3 mutation marker: 把下行改回 `header_str(headers, "x-tenant-id").unwrap_or("default-tenant").to_owned()`
        // → x_tenant_id_header_never_trusted_in_d1b 测试红 (应红, 守门生效)。
        // A5: Manual First ON + 命中 → 写已派 tenant; OFF / 未命中 → 空字符串强制 Go 派。
        tenant_id: identity
            .manual_first_tenant_id
            .clone()
            .unwrap_or_default(),
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
        // A4 + A5: raw credential 透传 control plane; canonical "bearer:<token>" / "x-api-key:<key>"。
        // None → 空字符串 (anonymous, dev/test 兼容); Some → 加 prefix 透传。
        // mutation: 把下行改 `String::new()` → A5 测试红 (control plane 收不到 client_credential)。
        client_credential: identity.client_credential_proto_value(),
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
    // ClientCredential import is at line 900 within test-helpers section (pre-existing).

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
            // W11-A D-1b Phase 2A.3 (D-13 (a)): empty mock-default; pre-Phase 2A.5 only.
            derived_tenant_id: String::new(),
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

    /// W11-A D-1b Phase 2A.3 (D-13 (a), 2026-05-24): Go control plane 派的权威
    /// tenant_id 必须原样透过 planned_attempt(), Phase 2A.4 双写对账 (account_planner
    /// 调用方) 才能拿来与 Manual First 派的 legacy tenant 做比对。
    ///
    /// MUTATION CHECK: 若未来 refactor 把 route_plan 从 PlannedAttempt 拆掉只留
    /// 子字段, derived_tenant_id 就不可达 → 此测试红 = 守门生效。
    #[test]
    fn planned_attempt_carries_derived_tenant_id_through_unchanged() {
        let mut plan = valid_route_plan_for_auth();
        plan.derived_tenant_id = "tenant-go-authoritative-phase2a3".to_owned();
        let attempt = planned_attempt(plan).expect("planner 应接受合法 RoutePlan");
        assert_eq!(
            attempt.route_plan.derived_tenant_id, "tenant-go-authoritative-phase2a3",
            "derived_tenant_id 必须原样透过, Phase 2A.4 双写对账才能读"
        );
    }

    /// W11-A D-1b Phase 2A.3: legacy mock RoutePlan 默认 derived_tenant_id 空字符串,
    /// 让 Phase 1 / 2A.3 阶段所有现存测试零行为变化; Phase 2A.4 reconciliation 用
    /// IsEmpty 区分"Go 未派" vs "Go 派出空字符串"。
    ///
    /// MUTATION CHECK: 若 valid_route_plan_for_auth 默认改 derived 为非空字符串 →
    /// Phase 1 守门测试 (build_route_query_writes_canonical_credential_value 等)
    /// 行为意外变化 → 此测试红 = 守门生效。
    #[test]
    fn legacy_mock_route_plan_defaults_derived_tenant_id_to_empty() {
        let plan = valid_route_plan_for_auth();
        let attempt = planned_attempt(plan).expect("planner 应接受合法 RoutePlan");
        assert!(
            attempt.route_plan.derived_tenant_id.is_empty(),
            "legacy mock 必须默认 derived_tenant_id 空, 保现存 Phase 1 测试零回归"
        );
    }

    // ─── W11-A D-1b Phase 2A.4 reconciliation 5 scenario tests (D-14 a + B-R1) ───
    //
    // 每个 test 都按 CLAUDE.md #14 写 mutation 注释:
    //  - 把 reconcile_identity 中 source/policy 任一分支删掉或改逻辑必有 ≥ 1 test 红.
    //  - source label 拼错 / counter 漏 inc → 各 source 测试断 metric 增量 红.
    //
    // 测试用 reconcile_identity 而非 planner.plan() (后者要 mock control plane RPC).

    /// Helper: 构造 RouteIdentity 含/不含 Manual First tenant. 默认 kind=none.
    fn make_identity_with_manual(tenant: Option<&str>) -> RouteIdentity {
        RouteIdentity {
            client_credential: None,
            manual_first_tenant_id: tenant.map(str::to_owned),
        }
    }

    /// Helper: 构造 RouteIdentity 含真实 ClientCredential — 用于 label 隔离
    /// (TC-RC-2 / TC-RC-3 都 trigger both_mismatch source, 必须用不同 kind 避免
    /// counter 竞态破坏 mutation 断言). bearer 走 TC-RC-2, x-api-key 走 TC-RC-3.
    fn make_identity_with_kind(
        kind: ClientCredentialKind,
        secret_marker: &str,
        manual_tenant: &str,
    ) -> RouteIdentity {
        use http::HeaderMap;
        let mut headers = HeaderMap::new();
        match kind {
            ClientCredentialKind::Bearer => {
                headers.insert(
                    "authorization",
                    format!("Bearer hk_test_RC_{}", secret_marker)
                        .parse()
                        .unwrap(),
                );
            }
            ClientCredentialKind::XApiKey => {
                headers.insert(
                    "x-api-key",
                    format!("hk_test_RC_{}", secret_marker).parse().unwrap(),
                );
            }
        }
        let cred = ClientCredential::from_headers(&headers)
            .expect("from_headers should accept canonical helper input")
            .expect("from_headers should return Some when header set");
        RouteIdentity {
            client_credential: Some(cred),
            manual_first_tenant_id: Some(manual_tenant.to_owned()),
        }
    }

    /// Helper: 构造 RoutePlan 含/不含 derived_tenant_id.
    fn make_plan_with_derived(derived: &str) -> RoutePlan {
        let mut plan = valid_route_plan_for_auth();
        plan.derived_tenant_id = derived.to_owned();
        plan
    }

    /// Helper: 抓 reconcile counter 当前值 (label = kind+source).
    /// 先 ping registry 以确保 lazy init 完成 — 测试可能在 reconcile_identity
    /// 之前先读 before-counter, registry 还没 init 就会 panic.
    fn reconcile_counter(kind: &str, source: &str) -> u64 {
        let _ = crate::metrics::registry();
        crate::metrics::client_credential_tenant_reconcile_total()
            .with_label_values(&[kind, source])
            .get()
    }

    /// TC-RC-1: both_match — Manual First t1 + Go derived t1 → Ok + counter both_match.
    /// MUTATION: 把 matches 判定改为 != → 红 (source 错为 both_mismatch).
    #[test]
    fn reconcile_identity_both_match_passes_and_increments_counter() {
        let identity = make_identity_with_manual(Some("tenant-rc1"));
        let plan = make_plan_with_derived("tenant-rc1");
        let before = reconcile_counter("none", "both_match");

        let result = reconcile_identity(&plan, &identity, ReconcilePolicy::FailClosed);

        assert!(result.is_ok(), "match 必通过: {:?}", result);
        let after = reconcile_counter("none", "both_match");
        assert_eq!(
            after,
            before + 1,
            "both_match counter 必须 +1 (mutation: 漏 inc 此处红)"
        );
    }

    /// TC-RC-2: both_mismatch FailClosed — Manual First t1 + Go derived t2 →
    /// Err TenantIdMismatch + counter both_mismatch. 用 bearer kind 隔离 TC-RC-3 的
    /// LogOnly 测试 (后者用 x-api-key kind), 防 cargo test 并行下 (kind=bearer, source=both_mismatch)
    /// counter 被两个测试同时 inc 破坏 assert_eq.
    /// MUTATION: 把 fails_closed_on_mismatch 返 false (LogOnly-by-default) → 红.
    #[test]
    fn reconcile_identity_both_mismatch_failclosed_returns_err_and_increments_counter() {
        let identity = make_identity_with_kind(
            ClientCredentialKind::Bearer,
            "RC2_FAILCLOSED_TOKEN_001",
            "tenant-rc2-mf",
        );
        let plan = make_plan_with_derived("tenant-rc2-go");
        let before = reconcile_counter("bearer", "both_mismatch");

        let result = reconcile_identity(&plan, &identity, ReconcilePolicy::FailClosed);

        match result {
            Err(PlanningError::TenantIdMismatch {
                kind,
                manual_first,
                go_derived,
            }) => {
                assert_eq!(kind, "bearer");
                assert_eq!(manual_first, "tenant-rc2-mf");
                assert_eq!(go_derived, "tenant-rc2-go");
            }
            other => panic!("FailClosed 必返 TenantIdMismatch, 实际 {:?}", other),
        }
        let after = reconcile_counter("bearer", "both_mismatch");
        assert_eq!(after, before + 1, "both_mismatch counter 必须 +1");
    }

    /// TC-RC-3: both_mismatch LogOnly — Manual First t1 + Go derived t2 →
    /// Ok (counter +1 but pass-through). 用 x-api-key kind 隔离 TC-RC-2 (bearer).
    /// MUTATION: LogOnly 误 return Err → 红.
    #[test]
    fn reconcile_identity_both_mismatch_logonly_passes_with_counter_increment() {
        let identity = make_identity_with_kind(
            ClientCredentialKind::XApiKey,
            "RC3_LOGONLY_TOKEN_001",
            "tenant-rc3-mf",
        );
        let plan = make_plan_with_derived("tenant-rc3-go");
        let before = reconcile_counter("x-api-key", "both_mismatch");

        let result = reconcile_identity(&plan, &identity, ReconcilePolicy::LogOnly);

        assert!(
            result.is_ok(),
            "LogOnly 必透过 (warn + 信 Go), 不阻断: {:?}",
            result
        );
        let after = reconcile_counter("x-api-key", "both_mismatch");
        assert_eq!(
            after,
            before + 1,
            "both_mismatch counter 必 +1 (即使 LogOnly)"
        );
    }

    /// TC-RC-4: manual_only — Manual First t1 + Go empty → Ok + counter manual_only.
    /// MUTATION: source label 改为 both_match 当 Go 空 → 红 (拒识别 manual_only 维度).
    #[test]
    fn reconcile_identity_manual_only_passes_and_increments_counter() {
        let identity = make_identity_with_manual(Some("tenant-rc4"));
        let plan = make_plan_with_derived("");
        let before = reconcile_counter("none", "manual_only");

        let result = reconcile_identity(&plan, &identity, ReconcilePolicy::FailClosed);

        assert!(result.is_ok(), "Go 未派, Manual First 兜底 必通过");
        let after = reconcile_counter("none", "manual_only");
        assert_eq!(after, before + 1);
    }

    /// TC-RC-5: go_only + none — Go derived t2 + Manual First None → Ok + counter go_only;
    /// 二者全空 → Ok + counter none. 一个 test 覆盖两个 source label 以省 setup.
    /// MUTATION: source label 算法把 (empty, non-empty) 当 manual_only → 红.
    #[test]
    fn reconcile_identity_go_only_and_none_paths() {
        // go_only: Manual 无, Go t1
        let identity_none = make_identity_with_manual(None);
        let plan_go = make_plan_with_derived("tenant-rc5-go");
        let before_go = reconcile_counter("none", "go_only");

        let result = reconcile_identity(&plan_go, &identity_none, ReconcilePolicy::FailClosed);
        assert!(result.is_ok(), "Go 派 + 无 Manual 必通过 (Go 权威)");
        assert_eq!(reconcile_counter("none", "go_only"), before_go + 1);

        // none: 二者都空 (anonymous, dev/test require_credential=false)
        let plan_empty = make_plan_with_derived("");
        let before_none = reconcile_counter("none", "none");
        let result = reconcile_identity(&plan_empty, &identity_none, ReconcilePolicy::FailClosed);
        assert!(result.is_ok(), "anonymous (二者空) 必通过");
        assert_eq!(reconcile_counter("none", "none"), before_none + 1);
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
            // W11-A D-1b Phase 2A.3: legacy auth-test fixture predates Phase 2A
            // dual-write; reconciliation behavior is tested via dedicated 2A.4 e2e.
            derived_tenant_id: String::new(),
        }
    }

    // ---------- W11-A D-1b test helpers ----------

    use crate::client_auth::ClientCredential;
    use http::{HeaderValue, header::AUTHORIZATION};

    /// W11-A D-1b test fixture: 构造一个 Bearer credential 让 build_route_query 可调用。
    /// 单元测试 default 用 anonymous tenant (Manual First OFF / 未命中); A5 测试显式 Some。
    fn test_identity_bearer() -> RouteIdentity {
        let mut h = HeaderMap::new();
        h.insert(
            AUTHORIZATION,
            HeaderValue::from_static("Bearer FAKE-d1a-test-bearer-do-not-log"),
        );
        let cred = ClientCredential::from_headers(&h).unwrap().unwrap();
        RouteIdentity {
            client_credential: Some(cred),
            manual_first_tenant_id: None,
        }
    }

    /// W11-A D-1b test fixture: anonymous identity (无 credential, dev 模式 listener
    /// 在 require_credential=false + 缺 Authorization/x-api-key 时构造此变体)。
    fn test_identity_anonymous() -> RouteIdentity {
        RouteIdentity {
            client_credential: None,
            manual_first_tenant_id: None,
        }
    }

    // ---------- W11-A D-1a tests (now also exercising RouteIdentity wiring) ----------

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
            &test_identity_bearer(),
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
            &test_identity_bearer(),
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
            &test_identity_bearer(),
        );
        assert_eq!(q.requested_model, "unknown");
        assert!(!q.stream);
    }

    // ---------- W11-A D-1b A3 + A5 tests ----------

    /// A3 acceptance gate: `x-tenant-id` 永不被信任。
    ///
    /// 请求带 `x-tenant-id: attacker-claimed-tenant` + 有效 Bearer 凭据 (Manual First OFF)
    /// → 构造 RouteQueryRequest.tenant_id 必须为空 (强制 Go 派, A5 OFF), 永远不是
    /// header 注入的 "attacker-claimed-tenant"。
    ///
    /// mutation: 删 const TENANT_ID_HEADER 上方注释 + 在 build_route_query tenant_id 改回
    /// `header_str(headers, "x-tenant-id").unwrap_or(...).to_owned()` → 此测试红, 守门生效。
    #[test]
    fn x_tenant_id_header_never_trusted_in_d1b() {
        let mut headers = HeaderMap::new();
        headers.insert("x-tenant-id", "attacker-claimed-tenant".parse().unwrap());
        let body_signal = BodyRouteSignal::default();

        // Manual First OFF (manual_first_tenant_id = None) → A5 OFF 路径
        let identity = test_identity_bearer();
        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
            &identity,
        );

        assert_ne!(
            q.tenant_id, "attacker-claimed-tenant",
            "A3 守门: x-tenant-id 永不可被信任成为权威 tenant; 实际 tenant_id = {:?}",
            q.tenant_id
        );
        assert_eq!(
            q.tenant_id, "",
            "A5 Manual First OFF: tenant_id 必须为空字符串强制 Go control plane 派"
        );
    }

    /// A5 ON: Manual First ON + 命中 → tenant_id 写已派值 + client_credential 双写。
    /// mutation: 在 build_route_query 把 client_credential 改 String::new() → 此测试红。
    #[test]
    fn manual_first_on_dual_writes_new_and_old_field() {
        let headers = HeaderMap::new();
        let body_signal = BodyRouteSignal::default();
        let mut identity = test_identity_bearer();
        identity.manual_first_tenant_id = Some("tenant-from-static-map".to_owned());

        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
            &identity,
        );

        assert_eq!(
            q.tenant_id, "tenant-from-static-map",
            "A5 ON: Manual First 命中 tenant 必须进 tenant_id 字段"
        );
        assert!(
            q.client_credential.starts_with("bearer:"),
            "A5 dual-write: client_credential 必须 same time 写, 实际 = {:?}",
            q.client_credential
        );
        assert!(
            !q.client_credential.is_empty(),
            "A5 dual-write: client_credential 不能为空"
        );
    }

    /// A5 OFF: Manual First OFF / 未命中 → tenant_id 为空 + client_credential 仍写。
    /// mutation: 在 build_route_query OFF 路径补 "default-tenant" → 此测试红。
    #[test]
    fn manual_first_off_writes_empty_tenant_to_force_control_plane_derivation() {
        let headers = HeaderMap::new();
        let body_signal = BodyRouteSignal::default();
        let identity = test_identity_bearer(); // manual_first_tenant_id = None

        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
            &identity,
        );

        assert_eq!(
            q.tenant_id, "",
            "A5 OFF: 必须写空字符串以强制 Go control plane 派权威 tenant"
        );
        assert!(
            q.client_credential.starts_with("bearer:"),
            "A5 OFF: client_credential 仍必须写"
        );
    }

    /// A1 边界 / A5 anonymous: client_credential=None → RouteQueryRequest.client_credential
    /// 必须是空字符串 (Phase 1 anonymous, dev/test 兼容)。
    /// mutation: 把 RouteIdentity::client_credential_proto_value() 改成 unwrap()
    /// → 此测试 panic → 红 (anonymous 通路被破坏)。
    #[test]
    fn anonymous_identity_writes_empty_credential_field() {
        let headers = HeaderMap::new();
        let body_signal = BodyRouteSignal::default();
        let identity = test_identity_anonymous();

        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
            &identity,
        );

        assert_eq!(
            q.client_credential, "",
            "anonymous 必须写空字符串 (Phase 1 兼容 dev/test 无凭据路径); 实际 = {:?}",
            q.client_credential
        );
        assert_eq!(q.tenant_id, "", "anonymous 也走 Go 派 (manual_first None)");
    }

    /// A4 acceptance gate: 整个 RouteQueryRequest 的 Debug 渲染不能含 raw secret。
    /// mutation: 删 redacting_debug.rs 中 RouteQueryRequest 手写 Debug impl + 改 build.rs
    /// skip_debug → 退回 derive(Debug) 输出 raw client_credential 字符 → 此测试红。
    #[test]
    fn route_query_debug_does_not_leak_client_credential() {
        let headers = HeaderMap::new();
        let body_signal = BodyRouteSignal::default();
        let identity = test_identity_bearer(); // FAKE-d1a-test-bearer-do-not-log

        let q = build_route_query(
            &headers,
            GatewayProtocol::AnthropicMessages,
            &RequestId::generate(),
            &body_signal,
            &identity,
        );

        let debug = format!("{:?}", q);
        assert!(
            !debug.contains("FAKE-d1a-test-bearer-do-not-log"),
            "A4: Debug 渲染不能含 raw client credential; 实际 = {debug:?}"
        );
        assert!(
            debug.contains("[CLIENT_CREDENTIAL_REDACTED"),
            "A4: Debug 必须含 [CLIENT_CREDENTIAL_REDACTED 占位; 实际 = {debug:?}"
        );
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
