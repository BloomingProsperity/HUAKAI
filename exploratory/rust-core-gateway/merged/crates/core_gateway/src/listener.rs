// M-rust-5 listener glue
// 职责: 接收 vendor API 请求, 规划 account route, 调用 proxy engine。

use axum::{
    Router,
    body::Body,
    extract::State,
    http::{
        HeaderMap, HeaderValue, Request, Response, StatusCode,
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
    redaction::redact_untrusted_text,
    request_id::RequestId,
};

const DEFAULT_CONTENT_TYPE: &str = "application/json";
const LISTENER_ERROR_LIMIT: usize = 256;

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
                    let _ = reporter.report(
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
                    let _ = reporter.report(
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
            let _ = reporter.report(
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
    let _ = reporter.report(
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
