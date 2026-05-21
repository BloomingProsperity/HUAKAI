// 入站请求 body idle 超时保护。

use std::{
    future::Future,
    io,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use axum::{
    body::Body,
    extract::State,
    http::{Request, Response},
    middleware::Next,
};
use bytes::Bytes;
use http_body::Frame;
use pin_project_lite::pin_project;
use tokio::time::{self, Instant, Sleep};
use tracing::warn;

use crate::GatewayState;

pub async fn request_body_idle_timeout_guard(
    State(state): State<GatewayState>,
    request: Request<Body>,
    next: Next,
) -> Response<Body> {
    let Some(timeout) = state.request_body_idle_timeout() else {
        return next.run(request).await;
    };

    let request = request.map(|body| Body::new(IdleTimeoutBody::new(body, timeout)));
    next.run(request).await
}

pin_project! {
    struct IdleTimeoutBody {
        #[pin]
        inner: Body,
        timeout: Duration,
        #[pin]
        sleep: Option<Sleep>,
    }
}

impl IdleTimeoutBody {
    fn new(inner: Body, timeout: Duration) -> Self {
        Self {
            inner,
            timeout,
            sleep: None,
        }
    }
}

impl http_body::Body for IdleTimeoutBody {
    type Data = Bytes;
    type Error = axum::Error;

    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Self::Data>, Self::Error>>> {
        let mut this = self.project();

        match this.inner.as_mut().poll_frame(cx) {
            Poll::Ready(Some(Ok(frame))) => {
                reset_idle_timer(this.sleep, *this.timeout);
                Poll::Ready(Some(Ok(frame)))
            }
            Poll::Ready(Some(Err(err))) => Poll::Ready(Some(Err(err))),
            Poll::Ready(None) => Poll::Ready(None),
            Poll::Pending => {
                // 计时从首次 poll 起算, 避免 wrap 后但尚未被消费的排队时间误伤 body idle 预算。
                if this.sleep.as_mut().as_pin_mut().is_none() {
                    this.sleep.set(Some(time::sleep(*this.timeout)));
                }

                if this
                    .sleep
                    .as_mut()
                    .as_pin_mut()
                    .expect("idle timer should be initialized before polling")
                    .poll(cx)
                    .is_ready()
                {
                    warn!(
                        timeout_ms = this.timeout.as_millis() as u64,
                        "request body idle timeout"
                    );
                    let err = io::Error::new(io::ErrorKind::TimedOut, "request body idle timeout");
                    Poll::Ready(Some(Err(axum::Error::new(err))))
                } else {
                    Poll::Pending
                }
            }
        }
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> http_body::SizeHint {
        self.inner.size_hint()
    }
}

fn reset_idle_timer(mut sleep: Pin<&mut Option<Sleep>>, timeout: Duration) {
    let deadline = Instant::now() + timeout;
    if let Some(timer) = sleep.as_mut().as_pin_mut() {
        timer.reset(deadline);
    } else {
        sleep.set(Some(time::sleep_until(deadline)));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    use futures_util::{FutureExt, future::poll_fn};
    use http_body::Body as HttpBody;
    use tokio_stream::wrappers::ReceiverStream;

    #[tokio::test]
    async fn request_body_idle_timer_starts_on_first_poll() {
        let (tx, rx) = tokio::sync::mpsc::channel::<Result<Bytes, io::Error>>(1);
        let mut body = Box::pin(IdleTimeoutBody::new(
            Body::from_stream(ReceiverStream::new(rx)),
            Duration::from_millis(20),
        ));

        tokio::time::sleep(Duration::from_millis(60)).await;

        assert!(
            poll_fn(|cx| body.as_mut().poll_frame(cx))
                .now_or_never()
                .is_none(),
            "首次 poll 前的等待不应消耗 request body idle 预算"
        );

        tx.send(Ok(Bytes::from_static(b"ok")))
            .await
            .expect("测试 body frame 应可发送");
        let frame = tokio::time::timeout(
            Duration::from_millis(200),
            poll_fn(|cx| body.as_mut().poll_frame(cx)),
        )
        .await
        .expect("frame 应在发送后可读")
        .expect("body 应产生 frame")
        .expect("frame 不应是 idle timeout 错误");
        assert_eq!(frame.into_data().expect("frame 应是 data"), b"ok"[..]);
    }
}
