// drain_mode 入站闸门。
// 只用于业务路由: 排空期间拒绝新请求, 已进入 handler 的流式请求不受影响。

use axum::{
    body::Body,
    http::{
        HeaderValue, Request, Response, StatusCode,
        header::{CONNECTION, CONTENT_TYPE},
    },
    middleware::Next,
};

use crate::heartbeat;

const DRAINING_BODY: &str = r#"{"error":"draining"}"#;

/// 业务请求进入 handler 前检查 drain_mode。
pub async fn drain_guard(request: Request<Body>, next: Next) -> Response<Body> {
    if heartbeat::is_drain_mode() {
        let mut response = Response::new(Body::from(DRAINING_BODY));
        *response.status_mut() = StatusCode::SERVICE_UNAVAILABLE;
        response
            .headers_mut()
            .insert(CONNECTION, HeaderValue::from_static("close"));
        response
            .headers_mut()
            .insert(CONTENT_TYPE, HeaderValue::from_static("application/json"));
        return response;
    }

    next.run(request).await
}
