use std::{io::Cursor, str};

use http::{HeaderMap, HeaderName, HeaderValue, Method, Request, StatusCode, Uri, Version};
use thiserror::Error;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX_HTTP11_HEAD_LEN: usize = 64 * 1024;
const MAX_STAGE1_BODY_LEN: usize = 1024 * 1024;

// Stage-1 只处理单个 HTTP/1.1 请求和单个 H2 响应；Stage-2 待办包括 keep-alive 多请求复用、
// chunked request body 流式解析、trailers、大 body 背压、GOAWAY 和连接级错误传播。
pub(crate) async fn bridge_single_request<S>(
    ipc: &mut S,
    send_request: &mut h2::client::SendRequest<Cursor<Vec<u8>>>,
) -> Result<(), H2BridgeError>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let inbound = read_http11_request(ipc).await?;
    let outbound = build_h2_request(&inbound)?;
    let end_stream = inbound.body.is_empty();
    let (response, mut stream) = send_request.send_request(outbound, end_stream)?;
    if !inbound.body.is_empty() {
        stream.send_data(Cursor::new(inbound.body), true)?;
    }

    let response = response.await?;
    let status = response.status();
    let headers = response.headers().clone();
    let mut body_stream = response.into_body();
    let mut body = Vec::new();
    while let Some(chunk) = body_stream.data().await {
        body.extend_from_slice(&chunk?);
        if body.len() > MAX_STAGE1_BODY_LEN {
            return Err(H2BridgeError::Unsupported(
                "Stage-2 待办: 大响应 body 背压与流式转发".to_owned(),
            ));
        }
    }
    if body_stream.trailers().await?.is_some() {
        return Err(H2BridgeError::Unsupported(
            "Stage-2 待办: H2 trailers 转回 HTTP/1.1".to_owned(),
        ));
    }

    write_http11_response(ipc, status, &headers, &body).await?;
    Ok(())
}

struct Http11Request {
    method: Method,
    path_and_query: String,
    authority: String,
    headers: Vec<(HeaderName, HeaderValue)>,
    body: Vec<u8>,
}

async fn read_http11_request<S>(ipc: &mut S) -> Result<Http11Request, H2BridgeError>
where
    S: AsyncRead + Unpin,
{
    let mut buf = Vec::new();
    let header_end = loop {
        if let Some(offset) = find_header_end(&buf) {
            break offset;
        }
        if buf.len() >= MAX_HTTP11_HEAD_LEN {
            return Err(H2BridgeError::InvalidRequest(
                "HTTP/1.1 request headers exceed Stage-1 limit".to_owned(),
            ));
        }
        let mut chunk = [0u8; 1024];
        let n = ipc.read(&mut chunk).await?;
        if n == 0 {
            return Err(H2BridgeError::InvalidRequest(
                "HTTP/1.1 request ended before headers completed".to_owned(),
            ));
        }
        buf.extend_from_slice(&chunk[..n]);
    };

    let header_text = str::from_utf8(&buf[..header_end])?;
    let mut lines = header_text.split("\r\n");
    let request_line = lines
        .next()
        .ok_or_else(|| H2BridgeError::InvalidRequest("missing request line".to_owned()))?;
    let mut parts = request_line.splitn(3, ' ');
    let method_text = parts
        .next()
        .ok_or_else(|| H2BridgeError::InvalidRequest("missing request method".to_owned()))?;
    let path_and_query_text = parts
        .next()
        .ok_or_else(|| H2BridgeError::InvalidRequest("missing request path".to_owned()))?;
    let version = parts
        .next()
        .ok_or_else(|| H2BridgeError::InvalidRequest("missing request version".to_owned()))?;
    if version != "HTTP/1.1" {
        return Err(H2BridgeError::Unsupported(format!(
            "Stage-1 only supports HTTP/1.1 over IPC, got {version}"
        )));
    }
    if !path_and_query_text.starts_with('/') {
        return Err(H2BridgeError::InvalidRequest(
            "HTTP/1.1 request path must be origin-form".to_owned(),
        ));
    }
    let method = Method::from_bytes(method_text.as_bytes())
        .map_err(|error| H2BridgeError::InvalidRequest(error.to_string()))?;
    let path_and_query = path_and_query_text.to_owned();

    let mut authority = None;
    let mut content_length = 0usize;
    let mut headers = Vec::new();
    let mut connection_tokens = Vec::new();
    for line in lines {
        if line.is_empty() {
            continue;
        }
        let (raw_name, raw_value) = line
            .split_once(':')
            .ok_or_else(|| H2BridgeError::InvalidRequest(format!("invalid header line {line}")))?;
        let name = HeaderName::from_bytes(raw_name.trim().as_bytes())
            .map_err(|error| H2BridgeError::InvalidRequest(error.to_string()))?;
        let value_text = raw_value.trim();
        let lower_name = name.as_str();
        if lower_name.eq_ignore_ascii_case("host") {
            authority = Some(value_text.to_owned());
            continue;
        }
        if lower_name.eq_ignore_ascii_case("connection") {
            connection_tokens.extend(parse_connection_tokens(value_text));
            continue;
        }
        if lower_name.eq_ignore_ascii_case("transfer-encoding") {
            return Err(H2BridgeError::Unsupported(
                "Stage-2 待办: chunked request body 流式解析".to_owned(),
            ));
        }
        if lower_name.eq_ignore_ascii_case("te") {
            return Err(H2BridgeError::Unsupported(
                "Stage-2 待办: trailers 转发".to_owned(),
            ));
        }
        if lower_name.eq_ignore_ascii_case("content-length") {
            content_length = value_text.parse::<usize>().map_err(|error| {
                H2BridgeError::InvalidRequest(format!("invalid Content-Length: {error}"))
            })?;
        }
        if is_hop_by_hop(lower_name) {
            continue;
        }
        let value = HeaderValue::from_str(value_text)
            .map_err(|error| H2BridgeError::InvalidRequest(error.to_string()))?;
        headers.push((name, value));
    }
    headers.retain(|(name, _)| !is_connection_named_hop_by_hop(name.as_str(), &connection_tokens));

    if content_length > MAX_STAGE1_BODY_LEN {
        return Err(H2BridgeError::Unsupported(
            "Stage-2 待办: 大 request body 背压与流式转发".to_owned(),
        ));
    }
    let body_start = header_end + 4;
    let body_end = body_start + content_length;
    while buf.len() < body_end {
        let mut chunk = vec![0u8; body_end - buf.len()];
        ipc.read_exact(&mut chunk).await?;
        buf.extend_from_slice(&chunk);
    }
    if buf.len() > body_end {
        return Err(H2BridgeError::Unsupported(
            "Stage-2 待办: HTTP/1.1 keep-alive 多请求复用同一 H2 连接".to_owned(),
        ));
    }

    Ok(Http11Request {
        method,
        path_and_query,
        authority: authority.ok_or_else(|| {
            H2BridgeError::InvalidRequest("HTTP/1.1 request missing Host header".to_owned())
        })?,
        headers,
        body: buf[body_start..body_end].to_vec(),
    })
}

fn build_h2_request(inbound: &Http11Request) -> Result<Request<()>, H2BridgeError> {
    let uri = format!("https://{}{}", inbound.authority, inbound.path_and_query)
        .parse::<Uri>()
        .map_err(|error| H2BridgeError::InvalidRequest(error.to_string()))?;
    let mut builder = Request::builder()
        .version(Version::HTTP_2)
        .method(inbound.method.clone())
        .uri(uri)
        .header("host", inbound.authority.as_str());
    for (name, value) in &inbound.headers {
        builder = builder.header(name, value);
    }
    Ok(builder.body(())?)
}

async fn write_http11_response<W>(
    ipc: &mut W,
    status: StatusCode,
    headers: &HeaderMap,
    body: &[u8],
) -> Result<(), H2BridgeError>
where
    W: AsyncWrite + Unpin,
{
    let reason = status.canonical_reason().unwrap_or("");
    ipc.write_all(format!("HTTP/1.1 {} {}\r\n", status.as_u16(), reason).as_bytes())
        .await?;
    for (name, value) in headers {
        let name_text = name.as_str();
        if name_text.eq_ignore_ascii_case("content-length") || is_hop_by_hop(name_text) {
            continue;
        }
        ipc.write_all(name_text.as_bytes()).await?;
        ipc.write_all(b": ").await?;
        ipc.write_all(value.as_bytes()).await?;
        ipc.write_all(b"\r\n").await?;
    }
    ipc.write_all(b"connection: close\r\n").await?;
    ipc.write_all(format!("content-length: {}\r\n\r\n", body.len()).as_bytes())
        .await?;
    ipc.write_all(body).await?;
    ipc.flush().await?;
    Ok(())
}

fn find_header_end(buf: &[u8]) -> Option<usize> {
    buf.windows(4).position(|window| window == b"\r\n\r\n")
}

fn is_hop_by_hop(name: &str) -> bool {
    matches!(
        name.to_ascii_lowercase().as_str(),
        "connection" | "upgrade" | "keep-alive" | "proxy-connection"
    )
}

fn parse_connection_tokens(value: &str) -> impl Iterator<Item = String> + '_ {
    value
        .split(',')
        .map(str::trim)
        .filter(|token| !token.is_empty())
        .map(str::to_ascii_lowercase)
}

fn is_connection_named_hop_by_hop(name: &str, connection_tokens: &[String]) -> bool {
    connection_tokens
        .iter()
        .any(|token| token.eq_ignore_ascii_case(name))
}

#[derive(Debug, Error)]
pub enum H2BridgeError {
    #[error("H2 bridge ipc io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("H2 bridge protocol error: {0}")]
    H2(#[from] h2::Error),
    #[error("H2 bridge HTTP build error: {0}")]
    Http(#[from] http::Error),
    #[error("H2 bridge UTF-8 error: {0}")]
    Utf8(#[from] str::Utf8Error),
    #[error("H2 bridge invalid HTTP/1.1 request: {0}")]
    InvalidRequest(String),
    #[error("H2 bridge unsupported Stage-1 boundary: {0}")]
    Unsupported(String),
}

#[cfg(test)]
mod tests {
    use std::{collections::BTreeMap, io::Cursor, time::Duration};

    use http::Response;
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        time::timeout,
    };

    #[tokio::test]
    async fn bridge_translates_single_http11_get_to_h2_and_back() {
        let request = b"GET /v1/messages?limit=1 HTTP/1.1\r\nHost: api.example.test\r\nX-Trace: abc123\r\nContent-Length: 0\r\n\r\n";

        let response = round_trip_request(
            request,
            ExpectedRequest {
                method: "GET",
                path_and_query: "/v1/messages?limit=1",
                host: "api.example.test",
                header_name: "x-trace",
                header_value: "abc123",
                absent_header_name: None,
                body: b"",
            },
            StubResponse {
                status: 200,
                header_name: "x-upstream",
                header_value: "h2-stub",
                body: b"get-ok",
            },
        )
        .await;

        assert_http11_response(
            &response,
            "HTTP/1.1 200 OK",
            &[
                ("x-upstream", "h2-stub"),
                ("connection", "close"),
                ("content-length", "6"),
            ],
            b"get-ok",
        );
    }

    #[tokio::test]
    async fn bridge_translates_single_http11_post_body_to_h2_and_back() {
        let request = b"POST /v1/messages HTTP/1.1\r\nHost: api.example.test\r\nContent-Type: application/json\r\nContent-Length: 17\r\n\r\n{\"hello\":\"world\"}";

        let response = round_trip_request(
            request,
            ExpectedRequest {
                method: "POST",
                path_and_query: "/v1/messages",
                host: "api.example.test",
                header_name: "content-type",
                header_value: "application/json",
                absent_header_name: None,
                body: b"{\"hello\":\"world\"}",
            },
            StubResponse {
                status: 200,
                header_name: "x-upstream",
                header_value: "posted",
                body: b"post-ok",
            },
        )
        .await;

        assert_http11_response(
            &response,
            "HTTP/1.1 200 OK",
            &[
                ("x-upstream", "posted"),
                ("connection", "close"),
                ("content-length", "7"),
            ],
            b"post-ok",
        );
    }

    #[tokio::test]
    async fn bridge_strips_connection_named_hop_by_hop_headers_before_h2() {
        let request = b"GET /v1/messages HTTP/1.1\r\nHost: api.example.test\r\nConnection: X-Hop-Only, keep-alive\r\nX-Hop-Only: do-not-forward\r\nX-Trace: keep-me\r\nContent-Length: 0\r\n\r\n";

        let response = round_trip_request(
            request,
            ExpectedRequest {
                method: "GET",
                path_and_query: "/v1/messages",
                host: "api.example.test",
                header_name: "x-trace",
                header_value: "keep-me",
                absent_header_name: Some("x-hop-only"),
                body: b"",
            },
            StubResponse {
                status: 200,
                header_name: "x-upstream",
                header_value: "clean",
                body: b"clean-ok",
            },
        )
        .await;

        assert_http11_response(
            &response,
            "HTTP/1.1 200 OK",
            &[
                ("x-upstream", "clean"),
                ("connection", "close"),
                ("content-length", "8"),
            ],
            b"clean-ok",
        );
    }

    #[tokio::test]
    async fn bridge_rejects_pipelined_second_request_in_stage_1() {
        let request = b"GET /one HTTP/1.1\r\nHost: api.example.test\r\nContent-Length: 0\r\n\r\nGET /two HTTP/1.1\r\nHost: api.example.test\r\nContent-Length: 0\r\n\r\n";
        let (mut send_request, driver, server) = h2_pair(
            ExpectedRequest {
                method: "GET",
                path_and_query: "/one",
                host: "api.example.test",
                header_name: "content-length",
                header_value: "0",
                absent_header_name: None,
                body: b"",
            },
            StubResponse {
                status: 200,
                header_name: "x-upstream",
                header_value: "unused",
                body: b"unused",
            },
        )
        .await;
        let (mut go_side, mut rust_side) = tokio::io::duplex(64 * 1024);

        go_side.write_all(request).await.unwrap();
        let err = super::bridge_single_request(&mut rust_side, &mut send_request)
            .await
            .expect_err("Stage-1 must fail-loud when a second HTTP/1.1 request is already queued");

        assert!(
            err.to_string().contains("Stage-2"),
            "error must identify the deferred multi-request boundary, got {err}"
        );
        driver.abort();
        let _ = server.await;
    }

    async fn round_trip_request(
        request: &[u8],
        expected: ExpectedRequest,
        stub: StubResponse,
    ) -> Vec<u8> {
        let (mut send_request, driver, server) = h2_pair(expected, stub).await;
        let (mut go_side, mut rust_side) = tokio::io::duplex(64 * 1024);

        let bridge = tokio::spawn(async move {
            super::bridge_single_request(&mut rust_side, &mut send_request)
                .await
                .unwrap();
        });
        go_side.write_all(request).await.unwrap();
        let response = read_http11_response_bytes(&mut go_side).await;
        bridge.await.unwrap();
        driver.abort();
        server.await.unwrap();
        response
    }

    async fn read_http11_response_bytes(stream: &mut tokio::io::DuplexStream) -> Vec<u8> {
        let mut buf = Vec::new();
        let header_end = loop {
            if let Some(offset) = super::find_header_end(&buf) {
                break offset;
            }
            let mut chunk = [0u8; 1024];
            let n = timeout(Duration::from_secs(1), stream.read(&mut chunk))
                .await
                .expect("response read must not hang")
                .unwrap();
            assert_ne!(n, 0, "response ended before headers completed");
            buf.extend_from_slice(&chunk[..n]);
        };
        let head = std::str::from_utf8(&buf[..header_end]).unwrap();
        let content_length = head
            .lines()
            .find_map(|line| {
                let (name, value) = line.split_once(':')?;
                name.eq_ignore_ascii_case("content-length")
                    .then(|| value.trim().parse::<usize>().unwrap())
            })
            .expect("test response must include Content-Length");
        let body_end = header_end + 4 + content_length;
        while buf.len() < body_end {
            let mut chunk = vec![0u8; body_end - buf.len()];
            timeout(Duration::from_secs(1), stream.read_exact(&mut chunk))
                .await
                .expect("response body read must not hang")
                .unwrap();
            buf.extend_from_slice(&chunk);
        }
        buf.truncate(body_end);
        buf
    }

    async fn h2_pair(
        expected: ExpectedRequest,
        stub: StubResponse,
    ) -> (
        h2::client::SendRequest<Cursor<Vec<u8>>>,
        tokio::task::JoinHandle<()>,
        tokio::task::JoinHandle<()>,
    ) {
        let (client_io, server_io) = tokio::io::duplex(64 * 1024);
        let server = tokio::spawn(async move {
            let mut server_h2 = h2::server::Builder::new()
                .handshake::<_, Cursor<Vec<u8>>>(server_io)
                .await
                .unwrap();
            let (request, mut respond) = timeout(Duration::from_secs(1), server_h2.accept())
                .await
                .expect("server must receive one H2 request")
                .expect("server stream must be present")
                .expect("server stream must be valid");
            assert_eq!(request.method().as_str(), expected.method);
            assert_eq!(
                request
                    .uri()
                    .path_and_query()
                    .map(|value| value.as_str())
                    .unwrap_or(""),
                expected.path_and_query
            );
            assert_eq!(
                request
                    .headers()
                    .get("host")
                    .and_then(|value| value.to_str().ok()),
                Some(expected.host)
            );
            assert_eq!(
                request
                    .headers()
                    .get(expected.header_name)
                    .and_then(|value| value.to_str().ok()),
                Some(expected.header_value)
            );
            if let Some(name) = expected.absent_header_name {
                assert_eq!(
                    request.headers().get(name),
                    None,
                    "HTTP/1.1 Connection-named hop-by-hop header must not reach H2"
                );
            }
            let mut body = request.into_body();
            let mut actual_body = Vec::new();
            while let Some(chunk) = timeout(Duration::from_secs(1), body.data())
                .await
                .expect("request body DATA must not hang")
            {
                actual_body.extend_from_slice(&chunk.unwrap());
            }
            assert_eq!(actual_body, expected.body);

            let response = Response::builder()
                .status(stub.status)
                .header(stub.header_name, stub.header_value)
                .body(())
                .unwrap();
            let mut stream = respond.send_response(response, false).unwrap();
            stream
                .send_data(Cursor::new(stub.body.to_vec()), true)
                .unwrap();
            let _ = timeout(Duration::from_millis(50), server_h2.accept()).await;
        });

        let (send_request, connection) =
            crate::h2_settings::client_handshake(client_io, &BTreeMap::new(), None)
                .await
                .unwrap();
        let driver = tokio::spawn(async move {
            let _ = connection.await;
        });
        (send_request, driver, server)
    }

    fn assert_http11_response(
        response: &[u8],
        status_line: &str,
        expected_headers: &[(&str, &str)],
        expected_body: &[u8],
    ) {
        let text = String::from_utf8(response.to_vec()).unwrap();
        let (head, body) = text
            .split_once("\r\n\r\n")
            .expect("HTTP/1.1 response must contain header terminator");
        assert!(
            head.starts_with(status_line),
            "unexpected response head: {head:?}"
        );
        let lower_head = head.to_ascii_lowercase();
        for (name, value) in expected_headers {
            let needle = format!("{name}: {value}");
            assert!(
                lower_head.contains(&needle),
                "missing response header {needle:?}; head={head:?}"
            );
        }
        assert_eq!(body.as_bytes(), expected_body);
    }

    #[derive(Clone, Copy)]
    struct ExpectedRequest {
        method: &'static str,
        path_and_query: &'static str,
        host: &'static str,
        header_name: &'static str,
        header_value: &'static str,
        absent_header_name: Option<&'static str>,
        body: &'static [u8],
    }

    #[derive(Clone, Copy)]
    struct StubResponse {
        status: u16,
        header_name: &'static str,
        header_value: &'static str,
        body: &'static [u8],
    }
}
