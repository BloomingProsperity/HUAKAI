// M-rust-5 listener glue
// 职责: 接收 vendor API 请求, 规划 account route, 调用 proxy engine。

use std::sync::atomic::{AtomicU64, Ordering};

use axum::{
    Router,
    body::Body,
    extract::State,
    http::{
        HeaderMap, HeaderName, HeaderValue, Request, Response, StatusCode,
        header::{CONTENT_LENGTH, CONTENT_TYPE},
    },
    routing::post,
};
use bytes::Bytes;
use tracing::{Instrument, info_span, warn};

use crate::{
    GatewayState,
    account_planner::{BodyRouteSignal, GatewayProtocol, PlanningError},
    attempt_reporter::{AttemptReportContext, AttemptReportStats, AttemptStatus, now_unix_ms_i64},
    proxy_engine::{ProxyError, validate_vendor_endpoint},
    redaction::{SENSITIVE_REQUEST_CREDENTIAL_HEADERS, redact_untrusted_text},
    request_id::RequestId,
};

const DEFAULT_CONTENT_TYPE: &str = "application/json";
const LISTENER_ERROR_LIMIT: usize = 256;

/// P1-7 (W11 同源) 2026-05-24: mock 上游分支主动剥除客户端凭据头计数器。
///
/// 背景:
/// - 生产模式下 config validate() 拒绝 mock_upstream_endpoint, 因此本分支只在
///   dev/test 触发, 但仍然要防客户端把真实 vendor 凭据 (Authorization / x-api-key /
///   cookie) 误发到 HUAKAI mock 路径 -> mock 上游可能 log / 落盘 / 泄露。
/// - 下游 proxy_engine/headers.rs `should_forward_request_header` 白名单已不在
///   forward 时透传这些头 (defense in depth 第一层), 但 listener 层显式 strip +
///   warn log + counter 让审计能看到"mock 分支有几次请求带了真实凭据被我们清掉",
///   下游白名单漂移 (例如有人把 authorization 加回白名单) 时也能从指标上发现。
///
/// 计数语义: **按 header value 数累加** — 一个请求带 4 个凭据 key 且 cookie 出现 2
/// 次时 +5。这样测试既能验证 "全部 key 都剥" 又能用 mutation (删任一 key 或把
/// 计数改回按 key 累加) 触发严格失败。Codex round 1 fix: 旧实现按 key +1, 漏报
/// 同名头多行场景, 已改为按 value 累加。
static MOCK_CREDENTIAL_STRIP_COUNT: AtomicU64 = AtomicU64::new(0);

/// 客户端凭据头名单 — 进入 mock 分支前必须 strip。
///
/// Codex round 2 fix 2026-05-24: 直接复用 redaction::SENSITIVE_REQUEST_CREDENTIAL_HEADERS,
/// 自动包含 x-auth-token / x-access-token 等 redaction 已识别的 token 头, 避免两份
/// 名单漂移 (审计 / log redaction 与 listener strip 必须同步)。
/// 注: 不能用 `const &[HeaderName]` (HeaderName 内含 interior-mutable 字段, E0492);
/// 用字符串名 + runtime HeaderName::from_static 等价。
const MOCK_STRIPPABLE_CLIENT_CREDENTIAL_HEADER_NAMES: &[&str] =
    SENSITIVE_REQUEST_CREDENTIAL_HEADERS;

/// 累计被 mock 分支 strip 的客户端凭据头出现次数 (测试 + 审计可读)。
pub fn mock_credential_strip_count() -> u64 {
    MOCK_CREDENTIAL_STRIP_COUNT.load(Ordering::Relaxed)
}

/// P1-7 mutation: 删本 fn body 或不调用 -> mock_upstream_strips_*_with_counter
/// 测试中 counter 不增 -> 断言红。也用于 dev/test 之外的回归保护。
///
/// Codex Round 1 P2 fix 2026-05-24: `HeaderMap::remove(&name)` 实际一次性移除
/// 同名头的所有 value 并只返回第一个, 旧 while-let 循环只能 +1 而非按 value
/// 数累加, 与 "按 header 出现次数累加" 的文档语义矛盾。改用 `get_all().iter().count()`
/// 先数清楚再一次性 remove, 保证 cookie 多值 / Authorization 多值场景计数正确。
fn strip_client_credentials_for_mock(headers: &mut HeaderMap) -> Vec<&'static str> {
    let mut stripped: Vec<&'static str> = Vec::new();
    for &name_str in MOCK_STRIPPABLE_CLIENT_CREDENTIAL_HEADER_NAMES {
        // HeaderName::from_static 等价于 http crate AUTHORIZATION / COOKIE /
        // PROXY_AUTHORIZATION 常量 (x-api-key 在 http crate 中无常量)。
        let name = HeaderName::from_static(name_str);
        // 先统计同名头 value 数量 (含重复行), 再一次性移除整个 key。
        // 这样客户端发 `Cookie: a` + `Cookie: b` 两行时 counter 增 2 而非 1。
        let value_count = headers.get_all(&name).iter().count();
        if value_count > 0 {
            headers.remove(&name);
            MOCK_CREDENTIAL_STRIP_COUNT.fetch_add(value_count as u64, Ordering::Relaxed);
            stripped.push(name_str);
        }
    }
    stripped
}

/// 构建业务 endpoint router; `/healthz` 由 lib.rs 保留。
pub fn build_router() -> Router<GatewayState> {
    Router::new()
        .route("/v1/messages", post(anthropic_messages))
        .route("/v1/chat/completions", post(openai_chat_completions))
}

async fn anthropic_messages(
    State(state): State<GatewayState>,
    request: Request<Body>,
) -> Response<Body> {
    handle_gateway_request(state, request, GatewayProtocol::AnthropicMessages).await
}

async fn openai_chat_completions(
    State(state): State<GatewayState>,
    request: Request<Body>,
) -> Response<Body> {
    handle_gateway_request(state, request, GatewayProtocol::OpenAiChatCompletions).await
}

async fn handle_gateway_request(
    state: GatewayState,
    request: Request<Body>,
    protocol: GatewayProtocol,
) -> Response<Body> {
    let request_id = RequestId::from_headers(request.headers());
    let span = info_span!(
        "listener_request",
        request_id = %request_id,
        protocol = protocol.as_str(),
        mock_upstream = state.mock_upstream_endpoint().is_some()
    );

    async move {
        if content_length_exceeds(request.headers(), state.max_body_bytes()) {
            return json_error_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                &request_id,
                "payload_too_large",
            );
        }

        if let Some(upstream) = state.mock_upstream_endpoint() {
            // W11-D2 B2: dev/test mock 上游必须发显式 mock attempt event 让审计可见。
            // mutation marker: 删除下面 reporter.report 两次调用 →
            // mock_upstream_emits_explicit_mock_attempt_event 测试变绿 (应红)。
            let context = AttemptReportContext::synthetic_mock_attempt(&request_id);
            let reporter = state.attempt_reporter().terminal_reporter(context);

            // P1-7 (W11 同源) 2026-05-24: mock 分支 forward 前显式 strip
            // 客户端凭据头 (Authorization / x-api-key / cookie / proxy-authorization)。
            // 下游 proxy_engine headers 白名单已天然不透传, 这里 listener 层多一道防御
            // (defense in depth) 同时 emit warn log + counter, 让审计能看到此次 mock
            // 流量曾带真实凭据被我们清掉, 也能及时发现下游白名单漂移。
            //
            // mutation: 删 strip_client_credentials_for_mock 调用 ->
            // mock_upstream_strips_authorization_x_api_key_and_cookie_with_counter
            // 测试中 counter 期望增量为 3 实际增 0 -> 红。
            let (mut parts, body) = request.into_parts();
            let stripped = strip_client_credentials_for_mock(&mut parts.headers);
            if !stripped.is_empty() {
                warn!(
                    stripped_headers = ?stripped,
                    "mock upstream branch stripped client credential headers before forward (W11/P1-7)",
                );
            }
            let request = Request::from_parts(parts, body);
            return match state
                .proxy_engine()
                .forward_endpoint(request, upstream, request_id.clone())
                .await
            {
                Ok(response) => {
                    let http_status = response.status();
                    // P2 codex review 2026-05-23: 分类 mock upstream 返回码,
                    // 4xx/5xx 不能与 2xx 同标 Success, 否则审计与客户端可见结果分歧。
                    let attempt_status = if http_status.is_server_error() {
                        AttemptStatus::Upstream5xx
                    } else if http_status.is_client_error() {
                        AttemptStatus::Upstream4xx
                    } else {
                        AttemptStatus::Success
                    };
                    // W12-A D-4 Slice 3: mock 演练分支 = pre-commit (axum 尚未送出 response),
                    // attempt 上报失败 warn 不阻塞 mock 测试。
                    let _ = reporter.report_pre_commit(
                        attempt_status,
                        Some(http_status.as_u16()),
                        AttemptReportStats::default(),
                        Some("mock_upstream_drill"),
                        None,
                    );
                    response
                }
                Err(err) => {
                    let err_redacted =
                        redact_untrusted_text(&err.to_string(), LISTENER_ERROR_LIMIT);
                    // P2 codex review 2026-05-23: 保留 proxy 错误的 HTTP status,
                    // 避免审计上的 mock attempt http_status 字段恒为 0。
                    // W12-A D-4 Slice 3: mock 演练分支 proxy 错 = pre-commit。
                    let _ = reporter.report_pre_commit(
                        AttemptStatus::InternalError,
                        Some(err.status_code().as_u16()),
                        AttemptReportStats::default(),
                        Some("mock_upstream_error"),
                        Some(&err_redacted),
                    );
                    proxy_error_response(err, &request_id)
                }
            };
        }

        // W11-A D-1a: bounded body buffer + routing signal 抽取必须先于 plan(),
        // 让 model / stream 以 body 为权威, 防止 header 篡改路由。
        // mutation: 删 BodyRouteSignal::from_json_body 调用并改回 BodyRouteSignal::default() →
        // listener_mock_body_model_drives_route_query 集成测试在 control-plane 侧断言红 (TODO P0-2 后续 slice)。
        let (parts, body) = request.into_parts();
        let body_bytes = match axum::body::to_bytes(body, state.max_body_bytes()).await {
            Ok(b) => b,
            Err(err) => {
                let err_redacted = redact_untrusted_text(&err.to_string(), LISTENER_ERROR_LIMIT);
                // P2 codex 2026-05-23: 区分大小超限 vs 其他 IO 错误; 大小超限要 413 与上游
                // content-length 早期 check 行为一致, 不能误归 400 / InternalError。
                let limit_exceeded = err_redacted.contains("length limit")
                    || err_redacted.contains("body length")
                    || err_redacted.contains("limit exceeded");
                if limit_exceeded {
                    warn!(error = %err_redacted, "request body exceeded size limit");
                    return json_error_response(
                        StatusCode::PAYLOAD_TOO_LARGE,
                        &request_id,
                        "payload_too_large",
                    );
                }
                warn!(error = %err_redacted, "request body read failed before planning");
                report_listener_planning_error(
                    &state,
                    &request_id,
                    AttemptStatus::InternalError,
                    StatusCode::BAD_REQUEST,
                    "request_body_read_failed",
                    &err_redacted,
                );
                return json_error_response(
                    StatusCode::BAD_REQUEST,
                    &request_id,
                    "request_body_read_failed",
                );
            }
        };
        let body_signal = BodyRouteSignal::from_json_body(&body_bytes);

        let planned = match state
            .account_planner()
            .plan(&parts.headers, protocol, &request_id, &body_signal)
            .await
        {
            Ok(planned) => planned,
            Err(PlanningError::ControlPlane(err)) => {
                let err_redacted = redact_untrusted_text(&err.to_string(), LISTENER_ERROR_LIMIT);
                warn!(error = %err_redacted, "control plane unavailable, failing closed");
                report_listener_planning_error(
                    &state,
                    &request_id,
                    AttemptStatus::ControlPlaneError,
                    StatusCode::SERVICE_UNAVAILABLE,
                    "control_plane_error",
                    &err_redacted,
                );
                return json_error_response(
                    StatusCode::SERVICE_UNAVAILABLE,
                    &request_id,
                    "control_plane_unavailable",
                );
            }
            Err(PlanningError::InvalidRoutePlan(err)) => {
                let err_redacted = redact_untrusted_text(&err, LISTENER_ERROR_LIMIT);
                warn!(error = %err_redacted, "route plan invalid");
                report_listener_planning_error(
                    &state,
                    &request_id,
                    AttemptStatus::ControlPlaneError,
                    StatusCode::BAD_GATEWAY,
                    "bad_route_plan",
                    &err_redacted,
                );
                return json_error_response(StatusCode::BAD_GATEWAY, &request_id, "bad_route_plan");
            }
        };

        // W11-C D-3: 控制面下发的 vendor endpoint 在 production 必须 https + 公网,
        // 防控制面被攻陷把流量打到 internal/metadata/loopback 服务。dev/test 模式 warn 不阻断。
        // mutation: 删本块 → control plane 返 http://attacker.internal 不再被拒 →
        // (待集成测试上线后) production_listener_rejects_non_https_vendor_endpoint 红。
        if let Err(err) =
            validate_vendor_endpoint(&planned.vendor_endpoint, state.runtime_mode())
        {
            let err_redacted = redact_untrusted_text(&err.to_string(), LISTENER_ERROR_LIMIT);
            // P2 codex 2026-05-23: 控制面下发的 endpoint 是 untrusted, log 前必须 redact,
            // 防 attacker control plane 通过控制 endpoint 字段注入日志。
            let endpoint_redacted = redact_untrusted_text(
                &planned.vendor_endpoint.to_string(),
                LISTENER_ERROR_LIMIT,
            );
            warn!(
                error = %err_redacted,
                vendor_endpoint = %endpoint_redacted,
                "vendor endpoint rejected before forward",
            );
            // P1 codex 2026-05-23: 用 leased attempt context 上报终态, 而不是
            // synthetic — 否则控制面侧 lease/account 出账不完整, 账本与 attempt_id 失联。
            let context = AttemptReportContext::from_planned(
                &request_id,
                &planned,
                body_bytes.len() as u64,
                now_unix_ms_i64(),
            );
            let reporter = state.attempt_reporter().terminal_reporter(context);
            // W12-A D-4 Slice 3: bad_vendor_endpoint = pre-commit (返 502, 未进上游)。
            let _ = reporter.report_pre_commit(
                AttemptStatus::ControlPlaneError,
                Some(StatusCode::BAD_GATEWAY.as_u16()),
                AttemptReportStats::default(),
                Some("bad_vendor_endpoint"),
                Some(&err_redacted),
            );
            return json_error_response(
                StatusCode::BAD_GATEWAY,
                &request_id,
                "bad_vendor_endpoint",
            );
        }

        // D-1a: body 已 buffer, 用实际字节数对 planned.route_plan.max_body_bytes 校验,
        // 比 content-length header 更准 (防 chunked / 缺 header 绕过)。
        if planned.route_plan.max_body_bytes > 0
            && (body_bytes.len() as u64) > planned.route_plan.max_body_bytes
        {
            // W12-A D-4 Slice 3 (Owner item 1 fix 2026-05-24): 旧实现 413 路径漏报 attempt,
            // billing 空洞 (上面 bad_vendor_endpoint 路径有 reporter.report, 413 路径没有)。
            // 现补 attempt report (pre-commit, 因未进上游, 503 / 413 都未送 response body)。
            let context = AttemptReportContext::from_planned(
                &request_id,
                &planned,
                body_bytes.len() as u64,
                now_unix_ms_i64(),
            );
            let reporter = state.attempt_reporter().terminal_reporter(context);
            let _ = reporter.report_pre_commit(
                AttemptStatus::ProtocolError,
                Some(StatusCode::PAYLOAD_TOO_LARGE.as_u16()),
                AttemptReportStats::default(),
                Some("payload_too_large"),
                Some("request body exceeded planned.route_plan.max_body_bytes"),
            );
            return json_error_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                &request_id,
                "payload_too_large",
            );
        }

        let request = Request::from_parts(parts, Body::from(body_bytes));

        match state
            .proxy_engine()
            .forward_planned(request, planned, request_id.clone())
            .await
        {
            Ok(response) => response,
            Err(err) => proxy_error_response(err, &request_id),
        }
    }
    .instrument(span)
    .await
}

fn proxy_error_response(err: ProxyError, request_id: &RequestId) -> Response<Body> {
    let err_redacted = redact_untrusted_text(&err.to_string(), LISTENER_ERROR_LIMIT);
    warn!(error = %err_redacted, "proxy request failed");
    json_error_response(err.status_code(), request_id, err.code())
}

fn report_listener_planning_error(
    state: &GatewayState,
    request_id: &RequestId,
    status: AttemptStatus,
    http_status: StatusCode,
    error_class: &str,
    error_message_redacted: &str,
) {
    let context = AttemptReportContext::synthetic_control_plane_error(request_id);
    let reporter = state.attempt_reporter().terminal_reporter(context);
    // W12-A D-4 Slice 3: planning_error 是 pre-commit (control_plane / route_plan / body 读失败前 4xx/5xx)。
    let _ = reporter.report_pre_commit(
        status,
        Some(http_status.as_u16()),
        AttemptReportStats::default(),
        Some(error_class),
        Some(error_message_redacted),
    );
}

fn content_length_exceeds(headers: &HeaderMap, max_body_bytes: usize) -> bool {
    headers
        .get(CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<usize>().ok())
        .is_some_and(|len| len > max_body_bytes)
}

fn json_error_response(status: StatusCode, request_id: &RequestId, code: &str) -> Response<Body> {
    // 用序列化器构造, request_id 来自客户端 header, 直接内插会被 " / \ 注入或破坏 JSON
    let payload = Bytes::from(
        serde_json::json!({ "error": code, "request_id": request_id.as_str() }).to_string(),
    );
    let mut response = Response::new(Body::from(payload));
    *response.status_mut() = status;
    response
        .headers_mut()
        .insert(CONTENT_TYPE, HeaderValue::from_static(DEFAULT_CONTENT_TYPE));
    response.headers_mut().insert(
        crate::request_id::REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );
    response
}
