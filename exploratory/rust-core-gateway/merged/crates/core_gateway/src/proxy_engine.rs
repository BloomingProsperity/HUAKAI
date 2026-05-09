// M-rust-5 proxy engine
// 职责: 使用 hyper-rustls 连接 vendor endpoint, 透传请求/响应 body, 并处理取消与 timeout。

use std::{
    fmt, io,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use axum::body::Body;
use bytes::Bytes;
use futures_core::Stream;
use http::{
    HeaderMap, HeaderName, HeaderValue, Request, Response, StatusCode, Uri,
    header::{
        AUTHORIZATION, CONNECTION, CONTENT_LENGTH, CONTENT_TYPE, HOST, PROXY_AUTHENTICATE,
        PROXY_AUTHORIZATION, TE, TRAILER, TRANSFER_ENCODING, UPGRADE,
    },
    uri::PathAndQuery,
};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper_rustls::{HttpsConnector, HttpsConnectorBuilder};
use hyper_util::{
    client::legacy::{Client, connect::HttpConnector},
    rt::TokioExecutor,
};
use pin_project_lite::pin_project;
use tokio::{
    sync::{mpsc, mpsc::error::TrySendError},
    task::{self, AbortHandle},
    time,
};
use tracing::{debug, warn};

use crate::{
    account_planner::{AuthMode, PlannedAttempt},
    attempt_reporter::{
        AttemptReportContext, AttemptReportStats, AttemptReporter, AttemptStatus,
        AttemptTerminalReporter, now_unix_ms_i64,
    },
    error::GatewayError,
    request_id::{REQUEST_ID_HEADER, RequestId},
    stream_pipeline::{StreamEvent, StreamPipeline, StreamProtocol, sse::DEFAULT_MAX_FRAME_BYTES},
};

pub type GatewayHttpConnector = HttpsConnector<HttpConnector>;
pub type GatewayHttpClient = Client<GatewayHttpConnector, Body>;

const STREAM_CHANNEL_DEPTH: usize = 16;
const BODY_IDLE_TIMEOUT: Duration = Duration::from_secs(30);
const DEFAULT_UPSTREAM_RESPONSE_TIMEOUT: Duration = Duration::from_secs(30);
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

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StreamObservation {
    pub request_id: RequestId,
    pub attempt_id: Option<String>,
    pub route_plan_id: Option<String>,
    pub vendor: String,
    pub event: StreamEvent,
}

#[derive(Clone)]
struct StreamTapConfig {
    sender: Option<mpsc::Sender<StreamObservation>>,
    request_id: RequestId,
    attempt_id: Option<String>,
    route_plan_id: Option<String>,
    vendor: String,
    protocol: StreamProtocol,
    max_frame_bytes: usize,
}

#[derive(Debug, thiserror::Error)]
pub enum ProxyError {
    #[error("bad upstream uri: {0}")]
    BadUpstreamUri(String),
    #[error("bad upstream request: {0}")]
    BadUpstreamRequest(String),
    #[error("bad route plan: {0}")]
    BadRoutePlan(String),
    #[error("upstream error: {0}")]
    Upstream(String),
    #[error("upstream timeout")]
    Timeout,
    #[error("attempt state error: {0}")]
    AttemptState(#[from] GatewayError),
}

impl ProxyError {
    pub fn status_code(&self) -> StatusCode {
        match self {
            Self::Timeout => StatusCode::GATEWAY_TIMEOUT,
            Self::BadUpstreamUri(_)
            | Self::BadUpstreamRequest(_)
            | Self::BadRoutePlan(_)
            | Self::Upstream(_)
            | Self::AttemptState(_) => StatusCode::BAD_GATEWAY,
        }
    }

    pub fn code(&self) -> &'static str {
        match self {
            Self::Timeout => "upstream_timeout",
            Self::BadUpstreamUri(_) => "bad_upstream_uri",
            Self::BadUpstreamRequest(_) => "bad_upstream_request",
            Self::BadRoutePlan(_) | Self::AttemptState(_) => "bad_route_plan",
            Self::Upstream(_) => "upstream_error",
        }
    }
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

pub fn build_http_client() -> GatewayHttpClient {
    let https = HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_or_http()
        .enable_http1()
        .enable_http2()
        .build();

    let mut builder = Client::builder(TokioExecutor::new());
    builder.pool_idle_timeout(Duration::from_secs(90));
    builder.pool_max_idle_per_host(128);
    builder.build(https)
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

pub fn echo_response(request: Request<Body>, request_id: &RequestId) -> Response<Body> {
    let (parts, body) = request.into_parts();
    let mut response = Response::new(body);
    if let Some(value) = parts.headers.get(CONTENT_TYPE) {
        response.headers_mut().insert(CONTENT_TYPE, value.clone());
    } else {
        response
            .headers_mut()
            .insert(CONTENT_TYPE, default_content_type());
    }
    set_request_id(response.headers_mut(), request_id);
    response
}

fn upstream_response_to_client(
    response: Response<Incoming>,
    request_id: &RequestId,
    stream_tap: Option<StreamTapConfig>,
    terminal_reporter: Option<AttemptTerminalReporter>,
    terminal_status: AttemptStatus,
    terminal_http_status: Option<u16>,
) -> Response<Body> {
    let (mut parts, body) = response.into_parts();
    remove_hop_by_hop_response_headers(&mut parts.headers);
    set_request_id(&mut parts.headers, request_id);
    if !parts.headers.contains_key(CONTENT_TYPE) {
        parts.headers.insert(CONTENT_TYPE, default_content_type());
    }
    let stream_tap = stream_tap.filter(|_| is_sse_response(&parts.headers));
    Response::from_parts(
        parts,
        relay_body(
            body,
            request_id.clone(),
            "upstream_response",
            stream_tap,
            terminal_reporter,
            terminal_status,
            terminal_http_status,
        ),
    )
}

fn relay_body<B>(
    mut body: B,
    request_id: RequestId,
    direction: &'static str,
    stream_tap: Option<StreamTapConfig>,
    terminal_reporter: Option<AttemptTerminalReporter>,
    terminal_status: AttemptStatus,
    terminal_http_status: Option<u16>,
) -> Body
where
    B: http_body::Body<Data = Bytes> + Send + Unpin + 'static,
    B::Error: fmt::Display + Send + Sync + 'static,
{
    let (sender, receiver) = mpsc::channel::<BodyChunk>(STREAM_CHANNEL_DEPTH);
    let drop_reporter = terminal_reporter.clone();

    let task = task::spawn(async move {
        let mut stream_pipeline = stream_tap
            .as_ref()
            .map(|tap| StreamPipeline::new(tap.protocol, tap.max_frame_bytes));
        let mut stats = AttemptReportStats::default();
        let mut stream_seen_done = false;
        let stream_requires_done =
            stream_pipeline.is_some() && terminal_status == AttemptStatus::Success;

        loop {
            let frame = tokio::select! {
                frame = body.frame() => frame,
                () = time::sleep(BODY_IDLE_TIMEOUT) => {
                    let err = io::Error::new(io::ErrorKind::TimedOut, "body stream idle timeout");
                    report_terminal(
                        terminal_reporter.as_ref(),
                        AttemptStatus::Timeout,
                        terminal_http_status,
                        &stats,
                        Some("timeout"),
                        Some("body stream idle timeout"),
                    );
                    emit_stream_observation(
                        stream_tap.as_ref(),
                        StreamEvent::UpstreamError("body stream idle timeout".to_owned()),
                    );
                    let _ = sender.send(Err(err)).await;
                    warn!(request_id = %request_id, direction, "body stream idle timeout");
                    break;
                }
            };

            match frame {
                Some(Ok(frame)) => match frame.into_data() {
                    Ok(data) if data.is_empty() => {}
                    Ok(data) => {
                        stats.record_body_chunk(data.len());
                        if let (Some(tap), Some(pipeline)) =
                            (stream_tap.as_ref(), stream_pipeline.as_mut())
                        {
                            handle_stream_events(
                                tap,
                                pipeline.push_bytes(&data),
                                terminal_reporter.as_ref(),
                                terminal_http_status,
                                &mut stats,
                                &mut stream_seen_done,
                            );
                        }
                        if sender.send(Ok(data)).await.is_err() {
                            report_terminal(
                                terminal_reporter.as_ref(),
                                AttemptStatus::ClientCancel,
                                terminal_http_status,
                                &stats,
                                Some("client_cancel"),
                                Some("client disconnected while relaying upstream response"),
                            );
                            debug!(request_id = %request_id, direction, "client disconnected, abort relay");
                            break;
                        }
                    }
                    Err(_) => {
                        debug!(request_id = %request_id, direction, "body trailer ignored");
                    }
                },
                Some(Err(err)) => {
                    let msg = format!("body stream error: {err}");
                    report_terminal(
                        terminal_reporter.as_ref(),
                        AttemptStatus::NetworkError,
                        terminal_http_status,
                        &stats,
                        Some("network_error"),
                        Some(&msg),
                    );
                    emit_stream_observation(
                        stream_tap.as_ref(),
                        StreamEvent::UpstreamError(msg.clone()),
                    );
                    let _ = sender
                        .send(Err(io::Error::new(io::ErrorKind::BrokenPipe, msg)))
                        .await;
                    break;
                }
                None => {
                    if let (Some(tap), Some(pipeline)) =
                        (stream_tap.as_ref(), stream_pipeline.as_mut())
                    {
                        handle_stream_events(
                            tap,
                            pipeline.finish(),
                            terminal_reporter.as_ref(),
                            terminal_http_status,
                            &mut stats,
                            &mut stream_seen_done,
                        );
                    }
                    if stream_requires_done && !stream_seen_done {
                        report_terminal(
                            terminal_reporter.as_ref(),
                            AttemptStatus::ProtocolError,
                            terminal_http_status,
                            &stats,
                            Some("protocol_error"),
                            Some("stream ended without DONE/message_stop"),
                        );
                    } else {
                        report_terminal(
                            terminal_reporter.as_ref(),
                            terminal_status,
                            terminal_http_status,
                            &stats,
                            None,
                            None,
                        );
                    }
                    break;
                }
            }
        }
    });
    let abort_handle = task.abort_handle();
    drop(task);

    Body::from_stream(ReceiverByteStream {
        receiver,
        abort_handle: Some(abort_handle),
        terminal_reporter: drop_reporter,
    })
}

fn handle_stream_events(
    tap: &StreamTapConfig,
    events: Vec<StreamEvent>,
    terminal_reporter: Option<&AttemptTerminalReporter>,
    terminal_http_status: Option<u16>,
    stats: &mut AttemptReportStats,
    stream_seen_done: &mut bool,
) {
    for event in events {
        stats.record_stream_event(&event);
        match &event {
            StreamEvent::Done => {
                *stream_seen_done = true;
                report_terminal(
                    terminal_reporter,
                    AttemptStatus::Success,
                    terminal_http_status,
                    stats,
                    None,
                    None,
                );
            }
            StreamEvent::ProtocolError(message) => {
                report_terminal(
                    terminal_reporter,
                    AttemptStatus::ProtocolError,
                    terminal_http_status,
                    stats,
                    Some("protocol_error"),
                    Some(message),
                );
            }
            StreamEvent::UpstreamError(message) => {
                report_terminal(
                    terminal_reporter,
                    AttemptStatus::Upstream5xx,
                    terminal_http_status,
                    stats,
                    Some("upstream_error"),
                    Some(message),
                );
            }
            StreamEvent::Data(_) | StreamEvent::Usage(_) | StreamEvent::CacheMetric(_) => {}
        }
        emit_stream_observation(Some(tap), event);
    }
}

fn report_terminal(
    terminal_reporter: Option<&AttemptTerminalReporter>,
    status: AttemptStatus,
    http_status: Option<u16>,
    stats: &AttemptReportStats,
    error_class: Option<&str>,
    error_message_redacted: Option<&str>,
) {
    if let Some(reporter) = terminal_reporter {
        let _ = reporter.report(
            status,
            http_status,
            stats.clone(),
            error_class,
            error_message_redacted,
        );
    }
}

fn emit_stream_observation(tap: Option<&StreamTapConfig>, event: StreamEvent) {
    let Some(tap) = tap else {
        return;
    };

    let observation = StreamObservation {
        request_id: tap.request_id.clone(),
        attempt_id: tap.attempt_id.clone(),
        route_plan_id: tap.route_plan_id.clone(),
        vendor: tap.vendor.clone(),
        event,
    };

    let Some(sender) = tap.sender.as_ref() else {
        return;
    };

    match sender.try_send(observation) {
        Ok(()) => {}
        Err(TrySendError::Full(_)) => {
            warn!(
                request_id = %tap.request_id,
                vendor = %tap.vendor,
                "stream observation channel full, dropping event"
            );
        }
        Err(TrySendError::Closed(_)) => {}
    }
}

fn normalize_upstream_headers(
    source: &HeaderMap,
    target: &mut HeaderMap,
    request_id: &RequestId,
    planned: Option<&PlannedAttempt>,
) -> Result<(), ProxyError> {
    for (name, value) in source {
        if should_forward_request_header(name) {
            target.insert(name, value.clone());
        }
    }

    if !target.contains_key(CONTENT_TYPE) {
        target.insert(CONTENT_TYPE, default_content_type());
    }
    set_request_id(target, request_id);

    if let Some(planned) = planned {
        apply_plan_auth(planned, target)?;
    }

    target.remove(HOST);
    Ok(())
}

fn should_forward_request_header(name: &http::HeaderName) -> bool {
    matches!(
        name.as_str(),
        "accept"
            | "content-length"
            | "content-type"
            | "user-agent"
            | ANTHROPIC_VERSION
            | ANTHROPIC_BETA
            | OPENAI_ORGANIZATION
            | OPENAI_PROJECT
            | GEMINI_API_CLIENT
    )
}

fn apply_plan_auth(planned: &PlannedAttempt, headers: &mut HeaderMap) -> Result<(), ProxyError> {
    if planned.auth_mode != AuthMode::Bearer {
        return Err(ProxyError::BadRoutePlan("unsupported auth mode".to_owned()));
    }
    if !vendor_supports_bearer(&planned.route_plan.vendor) {
        return Err(ProxyError::BadRoutePlan(format!(
            "unsupported bearer vendor {:?}",
            planned.route_plan.vendor
        )));
    }

    let token = std::str::from_utf8(planned.acquisition_token.as_ref())
        .map_err(|err| ProxyError::BadRoutePlan(format!("bearer token is not utf8: {err}")))?
        .trim();
    if token.is_empty() {
        return Err(ProxyError::BadRoutePlan("bearer token is empty".to_owned()));
    }

    let value = HeaderValue::from_str(&format!("Bearer {token}"))
        .map_err(|err| ProxyError::BadRoutePlan(format!("bad bearer token: {err}")))?;
    headers.insert(AUTHORIZATION, value);
    Ok(())
}

fn vendor_supports_bearer(vendor: &str) -> bool {
    matches!(
        vendor.to_ascii_lowercase().as_str(),
        "anthropic" | "openai" | "codex" | "gemini"
    )
}

fn route_stream_frame_limit(max_stream_frame_bytes: u64) -> usize {
    if max_stream_frame_bytes == 0 {
        DEFAULT_MAX_FRAME_BYTES
    } else {
        usize::try_from(max_stream_frame_bytes).unwrap_or(DEFAULT_MAX_FRAME_BYTES)
    }
}

fn is_sse_response(headers: &HeaderMap) -> bool {
    headers
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .is_some_and(|content_type| {
            content_type
                .trim()
                .eq_ignore_ascii_case("text/event-stream")
        })
}

fn remove_hop_by_hop_response_headers(headers: &mut HeaderMap) {
    // KEEP_ALIVE 在 http crate 中未导出为常量; 使用字面量名称
    static KEEP_ALIVE: HeaderName = HeaderName::from_static("keep-alive");
    headers.remove(CONNECTION);
    headers.remove(&KEEP_ALIVE);
    headers.remove(PROXY_AUTHENTICATE);
    headers.remove(PROXY_AUTHORIZATION);
    headers.remove(TE);
    headers.remove(TRAILER);
    headers.remove(TRANSFER_ENCODING);
    headers.remove(UPGRADE);
    headers.remove(CONTENT_LENGTH);
}

fn set_request_id(headers: &mut HeaderMap, request_id: &RequestId) {
    headers.insert(
        REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );
}

fn build_upstream_uri(base: &Uri, request_path: Option<&PathAndQuery>) -> Result<Uri, ProxyError> {
    let scheme = base
        .scheme_str()
        .ok_or_else(|| ProxyError::BadUpstreamUri("upstream uri missing scheme".to_owned()))?;
    let authority = base
        .authority()
        .ok_or_else(|| ProxyError::BadUpstreamUri("upstream uri missing authority".to_owned()))?;
    let target_path = request_path.map(PathAndQuery::as_str).unwrap_or("/");
    let base_path = base.path().trim_end_matches('/');
    let path_and_query = if base_path.is_empty() || base_path == "/" {
        target_path.to_owned()
    } else if target_path == "/" {
        base_path.to_owned()
    } else {
        format!("{base_path}{target_path}")
    };

    Uri::builder()
        .scheme(scheme)
        .authority(authority.as_str())
        .path_and_query(path_and_query)
        .build()
        .map_err(|err| ProxyError::BadUpstreamUri(err.to_string()))
}

fn default_content_type() -> HeaderValue {
    HeaderValue::from_static(DEFAULT_CONTENT_TYPE)
}
