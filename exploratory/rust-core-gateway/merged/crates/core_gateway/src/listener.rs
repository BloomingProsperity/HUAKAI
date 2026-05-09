// M-rust-5 listener glue
// 职责: 接收 vendor API 请求, 规划 account route, 调用 proxy engine, 并保留 M-rust-2 fallback。

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
    account_planner::{GatewayProtocol, PlanningError},
    attempt_reporter::{AttemptReportContext, AttemptReportStats, AttemptStatus},
    proxy_engine::{ProxyError, echo_response},
    request_id::RequestId,
};

const DEFAULT_CONTENT_TYPE: &str = "application/json";

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
            return match state
                .proxy_engine()
                .forward_endpoint(request, upstream, request_id.clone())
                .await
            {
                Ok(response) => response,
                Err(err) => proxy_error_response(err, &request_id),
            };
        }

        let planned = match state
            .account_planner()
            .plan(request.headers(), protocol, &request_id)
            .await
        {
            Ok(planned) => planned,
            Err(PlanningError::ControlPlane(err)) => {
                warn!(error = %err, "control plane unavailable, using local echo fallback");
                report_listener_planning_error(
                    &state,
                    &request_id,
                    AttemptStatus::ControlPlaneError,
                    "control_plane_error",
                    &err.to_string(),
                );
                return echo_response(request, &request_id);
            }
            Err(PlanningError::InvalidRoutePlan(err)) => {
                warn!(error = %err, "route plan invalid");
                report_listener_planning_error(
                    &state,
                    &request_id,
                    AttemptStatus::ControlPlaneError,
                    "control_plane_error",
                    &err,
                );
                return json_error_response(StatusCode::BAD_GATEWAY, &request_id, "bad_route_plan");
            }
        };

        if planned.route_plan.max_body_bytes > 0
            && content_length_exceeds_u64(request.headers(), planned.route_plan.max_body_bytes)
        {
            return json_error_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                &request_id,
                "payload_too_large",
            );
        }

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
    warn!(error = %err, "proxy request failed");
    json_error_response(err.status_code(), request_id, err.code())
}

fn report_listener_planning_error(
    state: &GatewayState,
    request_id: &RequestId,
    status: AttemptStatus,
    error_class: &str,
    error_message_redacted: &str,
) {
    let context = AttemptReportContext::synthetic_control_plane_error(request_id);
    let reporter = state.attempt_reporter().terminal_reporter(context);
    let _ = reporter.report(
        status,
        Some(StatusCode::BAD_GATEWAY.as_u16()),
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

fn content_length_exceeds_u64(headers: &HeaderMap, max_body_bytes: u64) -> bool {
    headers
        .get(CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<u64>().ok())
        .is_some_and(|len| len > max_body_bytes)
}

fn json_error_response(status: StatusCode, request_id: &RequestId, code: &str) -> Response<Body> {
    let payload = Bytes::from(format!(r#"{{"error":"{code}"}}"#));
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
