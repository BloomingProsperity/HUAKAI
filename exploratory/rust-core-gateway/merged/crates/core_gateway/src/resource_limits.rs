// P4 资源保护: 业务 in-flight 观测/卸载与连接级上限。

use std::{
    io,
    pin::Pin,
    sync::{
        Arc, Mutex,
        atomic::{AtomicI64, Ordering},
    },
    task::{Context, Poll},
};

use axum::{
    body::Body,
    extract::State,
    http::{
        HeaderValue, Request, Response, StatusCode,
        header::{CONNECTION, CONTENT_TYPE, RETRY_AFTER},
    },
    middleware::Next,
    serve::Listener,
};
use bytes::Bytes;
use http_body::Frame;
use pin_project_lite::pin_project;
use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    sync::{OwnedSemaphorePermit, Semaphore},
};

use crate::{
    GatewayState,
    config::StartupConfig,
    metrics,
    request_id::{REQUEST_ID_HEADER, RequestId},
};

#[derive(Debug)]
pub struct ResourceLimits {
    max_in_flight_requests: usize,
    max_connections: usize,
    overload_retry_after_secs: u64,
    in_flight_requests: AtomicI64,
    in_flight_semaphore: Option<Arc<Semaphore>>,
}

impl ResourceLimits {
    pub fn new(config: &StartupConfig) -> Self {
        let in_flight_semaphore = (config.max_in_flight_requests > 0)
            .then(|| Arc::new(Semaphore::new(config.max_in_flight_requests)));
        metrics::set_inflight_requests(0);
        metrics::set_inflight_limit(config.max_in_flight_requests as i64);

        Self {
            max_in_flight_requests: config.max_in_flight_requests,
            max_connections: config.max_connections,
            overload_retry_after_secs: config.overload_retry_after_secs,
            in_flight_requests: AtomicI64::new(0),
            in_flight_semaphore,
        }
    }

    pub fn max_in_flight_requests(&self) -> usize {
        self.max_in_flight_requests
    }

    pub fn max_connections(&self) -> usize {
        self.max_connections
    }

    pub fn overload_retry_after_secs(&self) -> u64 {
        self.overload_retry_after_secs
    }

    fn begin_request(self: &Arc<Self>) -> Result<InFlightPermitSlot, Overloaded> {
        self.increment_in_flight();

        let permit = if let Some(semaphore) = self.in_flight_semaphore.as_ref() {
            match semaphore.clone().try_acquire_owned() {
                Ok(permit) => Some(permit),
                Err(_) => {
                    self.decrement_in_flight();
                    return Err(Overloaded);
                }
            }
        } else {
            None
        };

        Ok(InFlightPermitSlot::new(InFlightRequestGuard {
            limits: self.clone(),
            _permit: permit,
        }))
    }

    fn increment_in_flight(&self) {
        let current = self.in_flight_requests.fetch_add(1, Ordering::AcqRel) + 1;
        metrics::set_inflight_requests(current);
    }

    fn decrement_in_flight(&self) {
        let current = self.in_flight_requests.fetch_sub(1, Ordering::AcqRel) - 1;
        metrics::set_inflight_requests(current.max(0));
    }
}

#[derive(Clone)]
pub(crate) struct InFlightPermitSlot {
    guard: Arc<Mutex<Option<InFlightRequestGuard>>>,
}

impl InFlightPermitSlot {
    fn new(guard: InFlightRequestGuard) -> Self {
        Self {
            guard: Arc::new(Mutex::new(Some(guard))),
        }
    }

    pub(crate) fn take(&self) -> Option<InFlightRequestGuard> {
        self.guard.lock().expect("in-flight guard poisoned").take()
    }
}

pub(crate) struct InFlightRequestGuard {
    limits: Arc<ResourceLimits>,
    _permit: Option<OwnedSemaphorePermit>,
}

impl Drop for InFlightRequestGuard {
    fn drop(&mut self) {
        self.limits.decrement_in_flight();
    }
}

#[derive(Debug)]
struct Overloaded;

/// 业务请求级资源保护 middleware。
///
/// 配置为 0 时仍做轻量计数并把 guard 交给响应 body; 配置大于 0 时先用信号量 fail-fast。
pub async fn overload_guard(
    State(state): State<GatewayState>,
    mut request: Request<Body>,
    next: Next,
) -> Response<Body> {
    let request_id = RequestId::from_headers(request.headers());
    let slot = match state.resource_limits().begin_request() {
        Ok(slot) => slot,
        Err(Overloaded) => {
            return overloaded_response(
                &request_id,
                state.resource_limits().overload_retry_after_secs(),
            );
        }
    };

    request.extensions_mut().insert(slot.clone());
    let response = next.run(request).await;

    if let Some(guard) = slot.take() {
        wrap_response_body(response, guard)
    } else {
        response
    }
}

pub(crate) fn wrap_response_body(
    response: Response<Body>,
    guard: InFlightRequestGuard,
) -> Response<Body> {
    response.map(|body| {
        Body::new(GuardedBody {
            inner: body,
            guard: Some(guard),
        })
    })
}

fn overloaded_response(request_id: &RequestId, retry_after_secs: u64) -> Response<Body> {
    // 用序列化器构造, request_id 来自客户端 header, 直接内插会被 " / \ 注入或破坏 JSON
    let payload = Bytes::from(
        serde_json::json!({ "error": "overloaded", "request_id": request_id.as_str() }).to_string(),
    );
    let mut response = Response::new(Body::from(payload));
    *response.status_mut() = StatusCode::SERVICE_UNAVAILABLE;
    response
        .headers_mut()
        .insert(CONTENT_TYPE, HeaderValue::from_static("application/json"));
    response
        .headers_mut()
        .insert(CONNECTION, HeaderValue::from_static("close"));
    response.headers_mut().insert(
        RETRY_AFTER,
        HeaderValue::from_str(&retry_after_secs.to_string())
            .expect("Retry-After 秒数应为合法 header value"),
    );
    response.headers_mut().insert(
        REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );
    response
}

pin_project! {
    struct GuardedBody {
        #[pin]
        inner: Body,
        guard: Option<InFlightRequestGuard>,
    }
}

impl http_body::Body for GuardedBody {
    type Data = Bytes;
    type Error = axum::Error;

    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Self::Data>, Self::Error>>> {
        let mut this = self.project();
        let poll = this.inner.as_mut().poll_frame(cx);
        if matches!(poll, Poll::Ready(None)) {
            let _ = this.guard.take();
        }
        poll
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> http_body::SizeHint {
        self.inner.size_hint()
    }
}

pub struct LimitedListener<L> {
    inner: L,
    connection_semaphore: Arc<Semaphore>,
}

impl<L> LimitedListener<L> {
    pub fn new(inner: L, max_connections: usize) -> Self {
        Self {
            inner,
            connection_semaphore: Arc::new(Semaphore::new(max_connections)),
        }
    }
}

impl<L> Listener for LimitedListener<L>
where
    L: Listener,
{
    type Io = TrackedIo<L::Io>;
    type Addr = L::Addr;

    async fn accept(&mut self) -> (Self::Io, Self::Addr) {
        let permit = self
            .connection_semaphore
            .clone()
            .acquire_owned()
            .await
            .expect("connection semaphore 不应被关闭");
        let (inner, addr) = self.inner.accept().await;
        (
            TrackedIo {
                inner,
                _permit: permit,
            },
            addr,
        )
    }

    fn local_addr(&self) -> io::Result<Self::Addr> {
        self.inner.local_addr()
    }
}

pub struct TrackedIo<I> {
    inner: I,
    _permit: OwnedSemaphorePermit,
}

impl<I> AsyncRead for TrackedIo<I>
where
    I: AsyncRead + Unpin,
{
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_read(cx, buf)
    }
}

impl<I> AsyncWrite for TrackedIo<I>
where
    I: AsyncWrite + Unpin,
{
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        Pin::new(&mut self.inner).poll_write(cx, buf)
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

#[cfg(test)]
mod tests {
    use std::{sync::Arc, time::Duration};

    use axum::serve::Listener;
    use tokio::{
        io::{DuplexStream, duplex},
        sync::mpsc,
        time,
    };

    use super::*;

    fn test_config(max_in_flight_requests: usize) -> StartupConfig {
        StartupConfig::from_env_iter([
            ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
            (
                "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
                "http://127.0.0.1:48080".to_owned(),
            ),
            ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
            ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
            ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
            ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
            (
                "HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(),
                max_in_flight_requests.to_string(),
            ),
            ("HUAKAI_MAX_CONNECTIONS".to_owned(), "0".to_owned()),
            (
                "HUAKAI_OVERLOAD_RETRY_AFTER_SECS".to_owned(),
                "1".to_owned(),
            ),
            (
                "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
                "300000".to_owned(),
            ),
            (
                "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
                "60000".to_owned(),
            ),
            (
                "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
                "30000".to_owned(),
            ),
            (
                "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
                "30000".to_owned(),
            ),
        ])
        .expect("resource limit test config 应解析成功")
    }

    #[test]
    fn in_flight_limit_zero_counts_without_rejecting() {
        let limits = Arc::new(ResourceLimits::new(&test_config(0)));

        let first = limits.begin_request().expect("0 上限不应拒绝首请求");
        let second = limits.begin_request().expect("0 上限不应拒绝并发请求");

        drop(first);
        drop(second);
    }

    #[test]
    fn in_flight_limit_rejects_when_permit_unavailable() {
        let limits = Arc::new(ResourceLimits::new(&test_config(1)));
        let first = limits.begin_request().expect("首请求应拿到 permit");

        assert!(
            limits.begin_request().is_err(),
            "唯一 permit 被占用时第二个请求应 fail-fast"
        );

        drop(first);
        assert!(limits.begin_request().is_ok(), "首请求释放后新请求应可进入");
    }

    #[test]
    fn in_flight_slot_take_transfers_guard_until_drop() {
        let limits = Arc::new(ResourceLimits::new(&test_config(1)));
        let slot = limits.begin_request().expect("首请求应拿到 permit");
        let guard = slot.take().expect("slot 应能交出 guard");

        assert!(slot.take().is_none(), "guard 只能被转交一次");
        assert!(
            limits.begin_request().is_err(),
            "guard 转交后未 drop 前仍应占用 permit"
        );

        drop(guard);
        assert!(
            limits.begin_request().is_ok(),
            "guard drop 后 permit 应释放"
        );
    }

    struct FakeListener {
        receiver: mpsc::Receiver<(DuplexStream, usize)>,
    }

    impl Listener for FakeListener {
        type Io = DuplexStream;
        type Addr = usize;

        async fn accept(&mut self) -> (Self::Io, Self::Addr) {
            self.receiver.recv().await.expect("fake listener 应有连接")
        }

        fn local_addr(&self) -> io::Result<Self::Addr> {
            Ok(0)
        }
    }

    #[tokio::test]
    async fn limited_listener_holds_connection_permit_until_io_drop() {
        let (sender, receiver) = mpsc::channel(2);
        let (first_io, _first_peer) = duplex(64);
        let (second_io, _second_peer) = duplex(64);
        sender.send((first_io, 1)).await.expect("首连接应可入队");
        sender.send((second_io, 2)).await.expect("第二连接应可入队");

        let mut listener = LimitedListener::new(FakeListener { receiver }, 1);
        let (first, first_addr) = listener.accept().await;
        assert_eq!(first_addr, 1);

        let second_accept = listener.accept();
        tokio::pin!(second_accept);
        assert!(
            time::timeout(Duration::from_millis(50), &mut second_accept)
                .await
                .is_err(),
            "首连接未 drop 前第二个 accept 应等待 permit"
        );

        drop(first);
        let (second, second_addr) = time::timeout(Duration::from_secs(1), &mut second_accept)
            .await
            .expect("首连接 drop 后第二个 accept 应放行");
        assert_eq!(second_addr, 2);
        drop(second);
    }
}
