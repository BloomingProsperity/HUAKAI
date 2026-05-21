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
mod error;
mod headers;
mod http_client;
mod relay;
pub mod sse_parser;

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
    request_id::RequestId,
    stream_pipeline::{StreamProtocol, sse::DEFAULT_MAX_FRAME_BYTES},
};

use headers::{build_upstream_uri, normalize_upstream_headers};
use relay::{StreamTapConfig, report_terminal, upstream_response_to_client};

const STREAM_CHANNEL_DEPTH: usize = 16;
const BODY_IDLE_TIMEOUT: Duration = Duration::from_secs(30);
const DEFAULT_UPSTREAM_RESPONSE_TIMEOUT: Duration = Duration::from_secs(30);
const LOCAL_MAX_STREAM_FRAME_BYTES: usize = DEFAULT_MAX_FRAME_BYTES;
const DEFAULT_CONTENT_TYPE: &str = "application/json";
const ANTHROPIC_VERSION: &str = "anthropic-version";
const ANTHROPIC_BETA: &str = "anthropic-beta";
const OPENAI_ORGANIZATION: &str = "openai-organization";
const OPENAI_PROJECT: &str = "openai-project";
const GEMINI_API_CLIENT: &str = "x-goog-api-client";

type BodyChunk = Result<Bytes, io::Error>;

#[derive(Clone)]
pub struct ProxyEngine {
    client: GatewayHttpClient,
    stream_observation_sender: Option<mpsc::Sender<StreamObservation>>,
    attempt_reporter: Option<AttemptReporter>,
}

pin_project! {
    struct ReceiverByteStream {
        receiver: mpsc::Receiver<BodyChunk>,
        abort_handle: Option<AbortHandle>,
        terminal_reporter: Option<AttemptTerminalReporter>,
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
        }
    }
}

impl Stream for ReceiverByteStream {
    type Item = BodyChunk;

    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        let this = self.project();
        this.receiver.poll_recv(cx)
    }
}

impl ProxyEngine {
    pub fn new(client: GatewayHttpClient) -> Self {
        Self {
            client,
            stream_observation_sender: None,
            attempt_reporter: None,
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
        let request_bytes_in = content_length_bytes(request.headers());
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
        let (parts, body) = request.into_parts();
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
        Ok(upstream_response_to_client(
            response,
            request_id,
            stream_tap,
            terminal_reporter,
            terminal_status,
            terminal_http_status,
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

fn is_sse_response(headers: &HeaderMap) -> bool {
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
