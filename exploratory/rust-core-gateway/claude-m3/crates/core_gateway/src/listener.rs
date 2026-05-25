// M-rust-2 listener 主体
// 目标: 原始 body 流式透传、request_id 归一化、body limit、client cancel 后停止读取。

use std::{
    fmt, io,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use axum::{
    Router,
    body::Body,
    extract::State,
    http::{
        HeaderMap, HeaderValue, Request, Response, StatusCode, Uri,
        header::{ACCEPT, CONTENT_LENGTH, CONTENT_TYPE},
        uri::PathAndQuery,
    },
    routing::post,
};
use bytes::Bytes;
use futures_core::Stream;
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper_util::client::legacy::{Client, connect::HttpConnector};
use pin_project_lite::pin_project;
use tokio::{sync::mpsc, time};
use tracing::{Instrument, debug, info_span, warn};

use crate::{GatewayState, request_id::REQUEST_ID_HEADER, request_id::RequestId};

/// response streaming 队列深度: 小而有界, 让慢 client 通过 backpressure 反压 upstream 读取。
const STREAM_CHANNEL_DEPTH: usize = 16;
/// M-rust-2 固定 idle timeout; M-rust-3+ 由 route plan 下发。
const BODY_IDLE_TIMEOUT: Duration = Duration::from_secs(30);
/// mock upstream 首包等待上限。
const UPSTREAM_RESPONSE_TIMEOUT: Duration = Duration::from_secs(30);
const DEFAULT_CONTENT_TYPE: &str = "application/json";

pub type GatewayHttpClient = Client<HttpConnector, Body>;

#[derive(Clone, Copy, Debug)]
enum GatewayProtocol {
    AnthropicMessages,
    OpenAiChatCompletions,
}

impl GatewayProtocol {
    fn as_str(self) -> &'static str {
        match self {
            Self::AnthropicMessages => "anthropic_messages",
            Self::OpenAiChatCompletions => "openai_chat_completions",
        }
    }
}

type BodyChunk = Result<Bytes, io::Error>;

pin_project! {
    /// tokio mpsc receiver 的 Stream 包装, 避免在 hot path 里装箱 stream。
    struct ReceiverByteStream {
        receiver: mpsc::Receiver<BodyChunk>,
    }
}

impl Stream for ReceiverByteStream {
    type Item = BodyChunk;

    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        let this = self.project();
        this.receiver.poll_recv(cx)
    }
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

        match state.mock_upstream_endpoint() {
            Some(upstream) => proxy_to_mock_upstream(state, request, upstream, request_id).await,
            None => local_echo_response(request, request_id),
        }
    }
    .instrument(span)
    .await
}

async fn proxy_to_mock_upstream(
    state: GatewayState,
    request: Request<Body>,
    upstream: Uri,
    request_id: RequestId,
) -> Response<Body> {
    let (parts, body) = request.into_parts();
    let uri = match build_upstream_uri(&upstream, parts.uri.path_and_query()) {
        Ok(uri) => uri,
        Err(err) => {
            warn!(error = %err, "mock upstream uri 构建失败");
            return json_error_response(StatusCode::BAD_GATEWAY, &request_id, "bad_upstream_uri");
        }
    };

    let mut upstream_request = match Request::builder()
        .method(parts.method)
        .uri(uri)
        .version(parts.version)
        .body(body)
    {
        Ok(req) => req,
        Err(err) => {
            warn!(error = %err, "mock upstream request 构建失败");
            return json_error_response(
                StatusCode::BAD_GATEWAY,
                &request_id,
                "bad_upstream_request",
            );
        }
    };
    normalize_upstream_headers(&parts.headers, upstream_request.headers_mut(), &request_id);

    let response = match time::timeout(
        UPSTREAM_RESPONSE_TIMEOUT,
        state.http_client().request(upstream_request),
    )
    .await
    {
        Ok(Ok(response)) => response,
        Ok(Err(err)) => {
            warn!(error = %err, "mock upstream 请求失败");
            return json_error_response(StatusCode::BAD_GATEWAY, &request_id, "upstream_error");
        }
        Err(_) => {
            warn!("mock upstream 首包超时");
            return json_error_response(
                StatusCode::GATEWAY_TIMEOUT,
                &request_id,
                "upstream_timeout",
            );
        }
    };

    upstream_response_to_client(response, request_id)
}

fn local_echo_response(request: Request<Body>, request_id: RequestId) -> Response<Body> {
    let content_type = request
        .headers()
        .get(CONTENT_TYPE)
        .cloned()
        .unwrap_or_else(default_content_type);
    let body = relay_body(request.into_body(), request_id.clone(), "client_request");
    let mut response = Response::new(body);
    *response.status_mut() = StatusCode::OK;
    set_common_response_headers(response.headers_mut(), &request_id, Some(content_type));
    response
}

fn upstream_response_to_client(
    response: Response<Incoming>,
    request_id: RequestId,
) -> Response<Body> {
    let (mut parts, body) = response.into_parts();
    parts.headers.remove(CONTENT_LENGTH);
    set_common_response_headers(&mut parts.headers, &request_id, None);
    Response::from_parts(parts, relay_body(body, request_id, "upstream_response"))
}

fn relay_body<B>(mut body: B, request_id: RequestId, direction: &'static str) -> Body
where
    B: http_body::Body<Data = Bytes> + Send + Unpin + 'static,
    B::Error: fmt::Display + Send + Sync + 'static,
{
    let (sender, receiver) = mpsc::channel::<BodyChunk>(STREAM_CHANNEL_DEPTH);

    tokio::spawn(async move {
        loop {
            let frame = tokio::select! {
                frame = body.frame() => frame,
                () = time::sleep(BODY_IDLE_TIMEOUT) => {
                    let err = io::Error::new(io::ErrorKind::TimedOut, "body stream idle timeout");
                    let _ = sender.send(Err(err)).await;
                    warn!(request_id = %request_id, direction, "body stream idle timeout");
                    break;
                }
            };

            match frame {
                Some(Ok(frame)) => match frame.into_data() {
                    Ok(data) if data.is_empty() => {}
                    Ok(data) => {
                        if sender.send(Ok(data)).await.is_err() {
                            // client 断开后 axum 会 drop response body; sender 失败即停止读 upstream。
                            debug!(request_id = %request_id, direction, "client disconnected, stop relay");
                            break;
                        }
                    }
                    Err(_) => {
                        // M-rust-2 不解析 trailers; 后续 attempt reporter 再记录 trailer 指标。
                        debug!(request_id = %request_id, direction, "body trailer ignored");
                    }
                },
                Some(Err(err)) => {
                    let msg = format!("body stream error: {err}");
                    let _ = sender
                        .send(Err(io::Error::new(io::ErrorKind::BrokenPipe, msg)))
                        .await;
                    break;
                }
                None => break,
            }
        }
    });

    Body::from_stream(ReceiverByteStream { receiver })
}

fn normalize_upstream_headers(source: &HeaderMap, target: &mut HeaderMap, request_id: &RequestId) {
    if let Some(value) = source.get(CONTENT_TYPE) {
        target.insert(CONTENT_TYPE, value.clone());
    } else {
        target.insert(CONTENT_TYPE, default_content_type());
    }

    if let Some(value) = source.get(ACCEPT) {
        target.insert(ACCEPT, value.clone());
    }

    target.insert(
        REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );
}

fn set_common_response_headers(
    headers: &mut HeaderMap,
    request_id: &RequestId,
    content_type: Option<HeaderValue>,
) {
    headers.insert(
        REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );

    if !headers.contains_key(CONTENT_TYPE) {
        headers.insert(
            CONTENT_TYPE,
            content_type.unwrap_or_else(default_content_type),
        );
    }
}

fn content_length_exceeds(headers: &HeaderMap, max_body_bytes: usize) -> bool {
    headers
        .get(CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<usize>().ok())
        .is_some_and(|len| len > max_body_bytes)
}

fn json_error_response(status: StatusCode, request_id: &RequestId, code: &str) -> Response<Body> {
    let payload = Bytes::from(format!(r#"{{"error":"{code}"}}"#));
    let mut response = Response::new(Body::from(payload));
    *response.status_mut() = status;
    set_common_response_headers(
        response.headers_mut(),
        request_id,
        Some(default_content_type()),
    );
    response
}

fn build_upstream_uri(base: &Uri, request_path: Option<&PathAndQuery>) -> Result<Uri, String> {
    let scheme = base
        .scheme_str()
        .ok_or_else(|| "upstream uri missing scheme".to_owned())?;
    let authority = base
        .authority()
        .ok_or_else(|| "upstream uri missing authority".to_owned())?;
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
        .map_err(|err| err.to_string())
}

fn default_content_type() -> HeaderValue {
    HeaderValue::from_static(DEFAULT_CONTENT_TYPE)
}
