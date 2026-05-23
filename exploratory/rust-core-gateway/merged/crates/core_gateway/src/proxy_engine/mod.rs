// M-rust-5 proxy engine
// 职责: 连接 vendor endpoint, 透传请求/响应 body, 并处理取消与 timeout。

use std::{
    io,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use axum::body::Body;
use bytes::Bytes;
use futures_core::Stream;
use http::{HeaderMap, HeaderValue, Request, Response, StatusCode, Uri, header::CONTENT_LENGTH};
use pin_project_lite::pin_project;
use tokio::{sync::mpsc, task::AbortHandle, time};
use tracing::warn;

mod auth;
#[cfg(feature = "mimicry-boring")]
pub mod boring_tls_connector;
pub mod endpoint_guard;
mod error;
mod headers;
mod http_client;
mod relay;
pub mod sse_parser;

pub use endpoint_guard::{EndpointGuardError, validate_vendor_endpoint};
pub use error::ProxyError;
#[cfg(feature = "mimicry-boring")]
pub use http_client::build_http_client_with_profile;
pub use http_client::{GatewayHttpClient, GatewayHttpConnector, build_http_client};
pub use relay::StreamObservation;

use crate::{
    account_planner::PlannedAttempt,
    attempt_reporter::{
        AttemptReportContext, AttemptReportStats, AttemptReporter, AttemptStatus,
        AttemptTerminalReporter, now_unix_ms_i64,
    },
    config::StartupConfig,
    request_id::RequestId,
    resource_limits::InFlightPermitSlot,
    stream_pipeline::{StreamProtocol, sse::DEFAULT_MAX_FRAME_BYTES},
};

use headers::{build_upstream_uri, normalize_upstream_headers};
use relay::{RelayTerminal, StreamTapConfig, report_terminal, upstream_response_to_client};

const STREAM_CHANNEL_DEPTH: usize = 16;
const DEFAULT_UPSTREAM_RESPONSE_TIMEOUT: Duration = Duration::from_secs(30);
const LOCAL_MAX_STREAM_FRAME_BYTES: usize = DEFAULT_MAX_FRAME_BYTES;
const DEFAULT_CONTENT_TYPE: &str = "application/json";
const ANTHROPIC_VERSION: &str = "anthropic-version";
const ANTHROPIC_BETA: &str = "anthropic-beta";
// W11-D D-6: OPENAI_ORGANIZATION / OPENAI_PROJECT 客户端值已 strip (见 headers.rs);
// 待 route_plan 注入合同上线后再加回作为 route-plan-owned 注入键。
const GEMINI_API_CLIENT: &str = "x-goog-api-client";

type BodyChunk = Result<Bytes, io::Error>;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ProxyTimeouts {
    pub upstream_body_idle_timeout: Option<Duration>,
    pub downstream_write_idle_timeout: Option<Duration>,
}

impl ProxyTimeouts {
    pub fn from_config(config: &StartupConfig) -> Self {
        Self {
            upstream_body_idle_timeout: duration_from_millis(config.upstream_body_idle_timeout_ms),
            downstream_write_idle_timeout: duration_from_millis(
                config.downstream_write_idle_timeout_ms,
            ),
        }
    }
}

impl Default for ProxyTimeouts {
    fn default() -> Self {
        Self {
            upstream_body_idle_timeout: Some(Duration::from_millis(300_000)),
            downstream_write_idle_timeout: Some(Duration::from_millis(60_000)),
        }
    }
}

fn duration_from_millis(value: u64) -> Option<Duration> {
    (value > 0).then(|| Duration::from_millis(value))
}

#[derive(Clone)]
pub struct ProxyEngine {
    client: GatewayHttpClient,
    stream_observation_sender: Option<mpsc::Sender<StreamObservation>>,
    attempt_reporter: Option<AttemptReporter>,
    timeouts: ProxyTimeouts,
}

pin_project! {
    struct ReceiverByteStream {
        receiver: mpsc::Receiver<BodyChunk>,
        abort_handle: Option<AbortHandle>,
        terminal_reporter: Option<AttemptTerminalReporter>,
        in_flight_guard: Option<crate::resource_limits::InFlightRequestGuard>,
    }

    // pin_project 要求通过 #[pinned_drop] 声明 Drop, 不能同时有独立的 impl Drop
    impl PinnedDrop for ReceiverByteStream {
        fn drop(this: Pin<&mut Self>) {
            let this = this.project();
            if let Some(reporter) = this.terminal_reporter.take() {
                let _ = reporter.report(
                    AttemptStatus::ClientCancel,
                    None,
                    AttemptReportStats::default(),
                    Some("client_cancel"),
                    Some("client response body dropped before terminal frame"),
                );
            }
            if let Some(handle) = this.abort_handle.take().filter(|h| !h.is_finished()) {
                handle.abort();
            }
            let _ = this.in_flight_guard.take();
        }
    }
}

impl Stream for ReceiverByteStream {
    type Item = BodyChunk;

    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        let this = self.project();
        let poll = this.receiver.poll_recv(cx);
        if matches!(poll, Poll::Ready(None)) {
            let _ = this.in_flight_guard.take();
        }
        poll
    }
}

impl ProxyEngine {
    pub fn new(client: GatewayHttpClient) -> Self {
        Self {
            client,
            stream_observation_sender: None,
            attempt_reporter: None,
            timeouts: ProxyTimeouts::default(),
        }
    }

    pub fn new_with_timeouts(client: GatewayHttpClient, timeouts: ProxyTimeouts) -> Self {
        Self {
            client,
            stream_observation_sender: None,
            attempt_reporter: None,
            timeouts,
        }
    }

    pub fn new_with_attempt_reporter(
        client: GatewayHttpClient,
        attempt_reporter: AttemptReporter,
    ) -> Self {
        Self {
            client,
            stream_observation_sender: None,
            attempt_reporter: Some(attempt_reporter),
            timeouts: ProxyTimeouts::default(),
        }
    }

    pub fn new_with_attempt_reporter_and_timeouts(
        client: GatewayHttpClient,
        attempt_reporter: AttemptReporter,
        timeouts: ProxyTimeouts,
    ) -> Self {
        Self {
            client,
            stream_observation_sender: None,
            attempt_reporter: Some(attempt_reporter),
            timeouts,
        }
    }

    pub fn new_with_stream_observation_sender(
        client: GatewayHttpClient,
        stream_observation_sender: mpsc::Sender<StreamObservation>,
    ) -> Self {
        Self {
            client,
            stream_observation_sender: Some(stream_observation_sender),
            attempt_reporter: None,
            timeouts: ProxyTimeouts::default(),
        }
    }

    pub fn new_with_stream_observation_sender_and_attempt_reporter(
        client: GatewayHttpClient,
        stream_observation_sender: mpsc::Sender<StreamObservation>,
        attempt_reporter: AttemptReporter,
    ) -> Self {
        Self {
            client,
            stream_observation_sender: Some(stream_observation_sender),
            attempt_reporter: Some(attempt_reporter),
            timeouts: ProxyTimeouts::default(),
        }
    }

    pub fn http_client(&self) -> &GatewayHttpClient {
        &self.client
    }

    pub async fn forward_planned(
        &self,
        request: Request<Body>,
        mut planned: PlannedAttempt,
        request_id: RequestId,
    ) -> Result<Response<Body>, ProxyError> {
        planned.attempt.mark_forwarding()?;

        let started_at_ms = now_unix_ms_i64();
        // W12-E D-9: 取真实 inbound body 字节, 不依赖 Content-Length header。
        // 见 compute_request_bytes_in + 该函数 #[cfg(test)] 单元测试。
        let request_bytes_in = compute_request_bytes_in(&request);
        let terminal_reporter = self.attempt_reporter.as_ref().map(|reporter| {
            reporter.terminal_reporter(AttemptReportContext::from_planned(
                &request_id,
                &planned,
                request_bytes_in,
                started_at_ms,
            ))
        });
        let deadline = route_attempt_timeout(planned.route_plan.attempt_deadline_ms);
        let result = self
            .forward_inner(
                request,
                &planned.vendor_endpoint,
                Some(&planned),
                &request_id,
                deadline,
                terminal_reporter.clone(),
            )
            .await;

        planned.attempt.mark_reporting()?;
        match result {
            Ok(response) => {
                planned.attempt.mark_done()?;
                Ok(response)
            }
            Err(err) => {
                report_proxy_error(terminal_reporter.as_ref(), &err);
                let _ = planned.attempt.mark_failed();
                Err(err)
            }
        }
    }

    pub async fn forward_endpoint(
        &self,
        request: Request<Body>,
        upstream: Uri,
        request_id: RequestId,
    ) -> Result<Response<Body>, ProxyError> {
        self.forward_inner(
            request,
            &upstream,
            None,
            &request_id,
            DEFAULT_UPSTREAM_RESPONSE_TIMEOUT,
            None,
        )
        .await
    }

    async fn forward_inner(
        &self,
        request: Request<Body>,
        upstream: &Uri,
        planned: Option<&PlannedAttempt>,
        request_id: &RequestId,
        upstream_response_timeout: Duration,
        terminal_reporter: Option<AttemptTerminalReporter>,
    ) -> Result<Response<Body>, ProxyError> {
        let (mut parts, body) = request.into_parts();
        let uri = build_upstream_uri(upstream, parts.uri.path_and_query())?;

        let mut upstream_request = Request::builder()
            .method(parts.method)
            .uri(uri)
            .version(parts.version)
            .body(body)
            .map_err(|err| ProxyError::BadUpstreamRequest(err.to_string()))?;

        normalize_upstream_headers(
            &parts.headers,
            upstream_request.headers_mut(),
            request_id,
            planned,
        )?;

        let response = match time::timeout(
            upstream_response_timeout,
            self.client.request(upstream_request),
        )
        .await
        {
            Ok(Ok(response)) => response,
            Ok(Err(err)) => {
                warn!(error = %err, "upstream request failed");
                return Err(ProxyError::Upstream(err.to_string()));
            }
            Err(_) => {
                warn!("upstream response timeout");
                return Err(ProxyError::Timeout);
            }
        };

        let terminal_status = classify_http_status(response.status());
        let terminal_http_status = Some(response.status().as_u16());
        let report_enabled = terminal_reporter.is_some();
        let stream_tap =
            planned.and_then(|planned| self.stream_tap_config(planned, request_id, report_enabled));
        let in_flight_guard = parts
            .extensions
            .remove::<InFlightPermitSlot>()
            .and_then(|slot| slot.take());
        Ok(upstream_response_to_client(
            response,
            request_id,
            stream_tap,
            RelayTerminal::new(terminal_reporter, terminal_status, terminal_http_status),
            in_flight_guard,
            self.timeouts,
        ))
    }

    fn stream_tap_config(
        &self,
        planned: &PlannedAttempt,
        request_id: &RequestId,
        report_enabled: bool,
    ) -> Option<StreamTapConfig> {
        let sender = self.stream_observation_sender.clone();
        if sender.is_none() && !report_enabled {
            return None;
        }
        let protocol = StreamProtocol::from_vendor(&planned.route_plan.vendor)?;
        let max_frame_bytes = route_stream_frame_limit(planned.route_plan.max_stream_frame_bytes);

        Some(StreamTapConfig {
            sender,
            request_id: request_id.clone(),
            attempt_id: Some(planned.attempt.attempt_id().to_owned()),
            route_plan_id: Some(planned.route_plan.route_plan_id.clone()),
            vendor: planned.route_plan.vendor.clone(),
            protocol,
            max_frame_bytes,
        })
    }
}

pub fn route_attempt_timeout(attempt_deadline_ms: u64) -> Duration {
    if attempt_deadline_ms == 0 {
        DEFAULT_UPSTREAM_RESPONSE_TIMEOUT
    } else {
        Duration::from_millis(attempt_deadline_ms)
    }
}

fn classify_http_status(status: StatusCode) -> AttemptStatus {
    if status.is_client_error() {
        AttemptStatus::Upstream4xx
    } else if status.is_server_error() {
        AttemptStatus::Upstream5xx
    } else {
        AttemptStatus::Success
    }
}

fn content_length_bytes(headers: &HeaderMap) -> u64 {
    headers
        .get(CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or_default()
}

/// W12-E D-9: 推导 inbound 请求体真实字节数 — 先用 body.size_hint().exact()
/// (buffered body 给出权威值), 否则 fallback Content-Length header。
/// 防 chunked / H2 上传等无 Content-Length 场景把 bytes_in 计成 0。
pub(super) fn compute_request_bytes_in(request: &Request<Body>) -> u64 {
    use http_body::Body as _;
    request
        .body()
        .size_hint()
        .exact()
        .unwrap_or_else(|| content_length_bytes(request.headers()))
}

#[cfg(test)]
mod request_bytes_tests {
    use super::*;

    /// W12-E D-9 判别: body 50 字节 + header 假说 10 → 必须返回 50 (body 权威)。
    /// mutation: 把 compute_request_bytes_in 改回 content_length_bytes(headers) → 红。
    #[test]
    fn compute_request_bytes_in_prefers_body_size_hint_over_content_length() {
        let body = Body::from(Bytes::from(vec![0u8; 50]));
        let request = Request::builder()
            .header(CONTENT_LENGTH, "10")
            .body(body)
            .expect("test request 应可构建");
        let count = compute_request_bytes_in(&request);
        assert_eq!(
            count, 50,
            "buffered body 字节数必须权威, 不被 header 欺骗 (W12-E D-9), 实际: {count}"
        );
    }

    #[test]
    fn compute_request_bytes_in_handles_empty_body() {
        let request = Request::builder()
            .body(Body::empty())
            .expect("test request 应可构建");
        let count = compute_request_bytes_in(&request);
        assert_eq!(count, 0, "空 body → 0");
    }
}

fn report_proxy_error(terminal_reporter: Option<&AttemptTerminalReporter>, err: &ProxyError) {
    let (status, error_class) = match err {
        ProxyError::Timeout => (AttemptStatus::Timeout, "timeout"),
        ProxyError::Upstream(_) => (AttemptStatus::NetworkError, "network_error"),
        ProxyError::BadUpstreamUri(_)
        | ProxyError::BadUpstreamRequest(_)
        | ProxyError::BadRoutePlan(_)
        | ProxyError::AttemptState(_) => (AttemptStatus::InternalError, "internal_error"),
    };
    report_terminal(
        terminal_reporter,
        status,
        Some(err.status_code().as_u16()),
        &AttemptReportStats::default(),
        Some(error_class),
        Some(&err.to_string()),
    );
}

fn route_stream_frame_limit(max_stream_frame_bytes: u64) -> usize {
    if max_stream_frame_bytes == 0 {
        DEFAULT_MAX_FRAME_BYTES
    } else {
        if max_stream_frame_bytes > LOCAL_MAX_STREAM_FRAME_BYTES as u64 {
            warn!(
                local_max_stream_frame_bytes = LOCAL_MAX_STREAM_FRAME_BYTES,
                "route stream frame limit clamped to local hard cap"
            );
        }
        usize::try_from(max_stream_frame_bytes)
            .unwrap_or(LOCAL_MAX_STREAM_FRAME_BYTES)
            .min(LOCAL_MAX_STREAM_FRAME_BYTES)
    }
}

pub(super) fn is_sse_response(headers: &HeaderMap) -> bool {
    headers
        .get(http::header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .is_some_and(|content_type| {
            content_type
                .trim()
                .eq_ignore_ascii_case("text/event-stream")
        })
}

/// W12-B D-5: 非 SSE 但 JSON 响应触发 body 缓冲 + usage 解析。
/// 支持 application/json 和 application/json; charset=utf-8 等。
pub(super) fn is_json_response(headers: &HeaderMap) -> bool {
    headers
        .get(http::header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .is_some_and(|content_type| {
            let ct = content_type.trim().to_ascii_lowercase();
            ct == "application/json" || ct.ends_with("+json")
        })
}

fn default_content_type() -> HeaderValue {
    HeaderValue::from_static(DEFAULT_CONTENT_TYPE)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn route_stream_frame_limit_uses_default_for_zero() {
        assert_eq!(route_stream_frame_limit(0), DEFAULT_MAX_FRAME_BYTES);
    }

    #[test]
    fn route_stream_frame_limit_preserves_smaller_route_value() {
        assert_eq!(route_stream_frame_limit(1024), 1024);
    }

    #[test]
    fn route_stream_frame_limit_clamps_u64_max_to_local_cap() {
        assert_eq!(
            route_stream_frame_limit(u64::MAX),
            LOCAL_MAX_STREAM_FRAME_BYTES
        );
    }

    #[test]
    fn route_stream_frame_limit_clamps_above_local_cap() {
        assert_eq!(
            route_stream_frame_limit(LOCAL_MAX_STREAM_FRAME_BYTES as u64 + 1),
            LOCAL_MAX_STREAM_FRAME_BYTES
        );
    }
}
