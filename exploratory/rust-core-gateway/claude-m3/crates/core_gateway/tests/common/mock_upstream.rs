// M-rust-2 测试 mock upstream
// 只服务本机测试流量, 不连接外网, fixture 命名全部 HUAKAI 自有。

use std::{
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    time::{Duration, Instant},
};

use axum::{
    Router,
    body::Body,
    extract::State,
    http::{
        HeaderMap, Response, StatusCode,
        header::{CONTENT_TYPE, HeaderValue},
    },
    routing::post,
};
use bytes::{Bytes, BytesMut};
use futures_util::stream;
use http_body_util::BodyExt;
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};

use core_gateway::request_id::REQUEST_ID_HEADER;

#[derive(Clone, Debug)]
pub enum MockBehavior {
    EchoBody,
    Json { status: StatusCode, body: Bytes },
    Sse { chunks: Vec<Bytes>, delay: Duration },
    SlowJson { delay: Duration, body: Bytes },
    Error5xx,
    CountOnly { per_frame_delay: Duration },
}

#[derive(Debug)]
struct MockState {
    behavior: MockBehavior,
    bytes_seen: AtomicUsize,
    requests_seen: AtomicUsize,
    last_request_id: Mutex<Option<String>>,
}

#[derive(Debug)]
pub struct MockUpstream {
    addr: SocketAddr,
    state: Arc<MockState>,
    task: JoinHandle<()>,
}

impl MockUpstream {
    pub async fn spawn(behavior: MockBehavior) -> Self {
        let state = Arc::new(MockState {
            behavior,
            bytes_seen: AtomicUsize::new(0),
            requests_seen: AtomicUsize::new(0),
            last_request_id: Mutex::new(None),
        });
        let app = Router::new()
            .route("/v1/messages", post(handle_mock_request))
            .route("/v1/chat/completions", post(handle_mock_request))
            .with_state(state.clone());
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("mock upstream bind 应成功");
        let addr = listener.local_addr().expect("mock upstream addr 应存在");
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        Self { addr, state, task }
    }

    pub fn endpoint(&self) -> String {
        format!("http://{}", self.addr)
    }

    pub fn bytes_seen(&self) -> usize {
        self.state.bytes_seen.load(Ordering::SeqCst)
    }

    pub fn requests_seen(&self) -> usize {
        self.state.requests_seen.load(Ordering::SeqCst)
    }

    pub async fn last_request_id(&self) -> Option<String> {
        self.state.last_request_id.lock().await.clone()
    }

    pub async fn wait_for_bytes_at_least(&self, min: usize, timeout: Duration) -> usize {
        let started = Instant::now();
        loop {
            let current = self.bytes_seen();
            if current >= min || started.elapsed() >= timeout {
                return current;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    }
}

impl Drop for MockUpstream {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn handle_mock_request(
    State(state): State<Arc<MockState>>,
    headers: HeaderMap,
    body: Body,
) -> Response<Body> {
    state.requests_seen.fetch_add(1, Ordering::SeqCst);
    let request_id = headers
        .get(REQUEST_ID_HEADER)
        .and_then(|value| value.to_str().ok())
        .map(ToOwned::to_owned);
    *state.last_request_id.lock().await = request_id.clone();

    match state.behavior.clone() {
        MockBehavior::EchoBody => echo_body(state, body, request_id).await,
        MockBehavior::Json { status, body } => json_response(status, body, request_id),
        MockBehavior::Sse { chunks, delay } => sse_response(chunks, delay, request_id),
        MockBehavior::SlowJson { delay, body } => {
            tokio::time::sleep(delay).await;
            json_response(StatusCode::OK, body, request_id)
        }
        MockBehavior::Error5xx => json_response(
            StatusCode::BAD_GATEWAY,
            Bytes::from_static(br#"{"error":"mock_5xx"}"#),
            request_id,
        ),
        MockBehavior::CountOnly { per_frame_delay } => {
            drain_body(state, body, per_frame_delay).await;
            json_response(
                StatusCode::OK,
                Bytes::from_static(br#"{"status":"counted"}"#),
                request_id,
            )
        }
    }
}

async fn echo_body(
    state: Arc<MockState>,
    body: Body,
    request_id: Option<String>,
) -> Response<Body> {
    let bytes = drain_body(state, body, Duration::ZERO).await;
    let mut response = Response::new(Body::from(bytes.freeze()));
    *response.status_mut() = StatusCode::OK;
    set_headers(response.headers_mut(), DEFAULT_JSON, request_id);
    response
}

async fn drain_body(state: Arc<MockState>, mut body: Body, delay: Duration) -> BytesMut {
    let mut bytes = BytesMut::new();

    while let Some(frame) = body.frame().await {
        match frame {
            Ok(frame) => {
                if let Ok(data) = frame.into_data() {
                    state.bytes_seen.fetch_add(data.len(), Ordering::SeqCst);
                    bytes.extend_from_slice(&data);
                    if !delay.is_zero() {
                        tokio::time::sleep(delay).await;
                    }
                }
            }
            Err(_) => break,
        }
    }

    bytes
}

fn json_response(status: StatusCode, body: Bytes, request_id: Option<String>) -> Response<Body> {
    let mut response = Response::new(Body::from(body));
    *response.status_mut() = status;
    set_headers(response.headers_mut(), DEFAULT_JSON, request_id);
    response
}

fn sse_response(chunks: Vec<Bytes>, delay: Duration, request_id: Option<String>) -> Response<Body> {
    let stream = stream::unfold(chunks.into_iter(), move |mut chunks| async move {
        let chunk = chunks.next()?;
        if !delay.is_zero() {
            tokio::time::sleep(delay).await;
        }
        Some((Ok::<Bytes, std::io::Error>(chunk), chunks))
    });

    let mut response = Response::new(Body::from_stream(stream));
    *response.status_mut() = StatusCode::OK;
    set_headers(response.headers_mut(), "text/event-stream", request_id);
    response
}

fn set_headers(headers: &mut HeaderMap, content_type: &'static str, request_id: Option<String>) {
    headers.insert(CONTENT_TYPE, HeaderValue::from_static(content_type));
    if let Some(request_id) = request_id {
        if let Ok(value) = HeaderValue::from_str(&request_id) {
            headers.insert(REQUEST_ID_HEADER, value);
        }
    }
}

const DEFAULT_JSON: &str = "application/json";
