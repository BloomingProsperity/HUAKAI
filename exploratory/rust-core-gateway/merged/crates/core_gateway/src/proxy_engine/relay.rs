use std::{fmt, io, time::Duration};

use axum::body::Body;
use bytes::Bytes;
use http::{Response, header::CONTENT_TYPE};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use tokio::{
    sync::{
        mpsc,
        mpsc::{Sender, error::TrySendError},
    },
    task, time,
};
use tracing::{debug, warn};

use crate::{
    attempt_reporter::{AttemptReportStats, AttemptStatus, AttemptTerminalReporter},
    request_id::RequestId,
    resource_limits::InFlightRequestGuard,
    stream_pipeline::{StreamEvent, StreamPipeline, StreamProtocol},
};

use super::{
    BodyChunk, ProxyTimeouts, ReceiverByteStream, STREAM_CHANNEL_DEPTH, default_content_type,
    headers::{remove_hop_by_hop_response_headers, set_request_id},
    is_json_response, is_sse_response,
};

/// W12-B D-5: 非流式 2xx body 缓冲 cap, 防恶意巨大 body 占内存。
/// 1 MiB 覆盖正常 chat completion 响应 (实测 ~10-50 KiB), 超出 → 标记 unparsable。
const NON_STREAM_USAGE_PARSE_CAP: usize = 1024 * 1024;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StreamObservation {
    pub request_id: RequestId,
    pub attempt_id: Option<String>,
    pub route_plan_id: Option<String>,
    pub vendor: String,
    pub event: StreamEvent,
}

#[derive(Clone)]
pub(super) struct StreamTapConfig {
    pub(super) sender: Option<mpsc::Sender<StreamObservation>>,
    pub(super) request_id: RequestId,
    pub(super) attempt_id: Option<String>,
    pub(super) route_plan_id: Option<String>,
    pub(super) vendor: String,
    pub(super) protocol: StreamProtocol,
    pub(super) max_frame_bytes: usize,
}

pub(super) fn upstream_response_to_client(
    response: Response<Incoming>,
    request_id: &RequestId,
    stream_tap: Option<StreamTapConfig>,
    terminal: RelayTerminal,
    in_flight_guard: Option<InFlightRequestGuard>,
    timeouts: ProxyTimeouts,
) -> Response<Body> {
    let (mut parts, body) = response.into_parts();
    remove_hop_by_hop_response_headers(&mut parts.headers);
    set_request_id(&mut parts.headers, request_id);
    if !parts.headers.contains_key(CONTENT_TYPE) {
        parts.headers.insert(CONTENT_TYPE, default_content_type());
    }
    let is_sse = is_sse_response(&parts.headers);
    let is_json = is_json_response(&parts.headers);
    let is_2xx_success = terminal.status == AttemptStatus::Success
        && terminal
            .http_status
            .map(|s| (200..300).contains(&s))
            .unwrap_or(false);
    // W12-B D-5: 非 SSE + JSON + 2xx Success 时, 拉出 protocol 准备做 body 缓冲 + usage 解析。
    // 保留与 stream_tap 同源的 protocol, 这样 SSE 路径与非 SSE 路径共用 vendor → protocol 映射。
    let non_stream_protocol = stream_tap
        .as_ref()
        .filter(|_| !is_sse && is_json && is_2xx_success)
        .map(|tap| tap.protocol);
    let stream_tap = stream_tap.filter(|_| is_sse);
    Response::from_parts(
        parts,
        relay_body(
            body,
            request_id.clone(),
            "upstream_response",
            stream_tap,
            non_stream_protocol,
            terminal,
            in_flight_guard,
            timeouts,
        ),
    )
}

pub(super) struct RelayTerminal {
    reporter: Option<AttemptTerminalReporter>,
    status: AttemptStatus,
    http_status: Option<u16>,
}

impl RelayTerminal {
    pub(super) fn new(
        reporter: Option<AttemptTerminalReporter>,
        status: AttemptStatus,
        http_status: Option<u16>,
    ) -> Self {
        Self {
            reporter,
            status,
            http_status,
        }
    }
}

// W12-B D-5: 8 个参数 — 内聚的 relay 状态; 拆成 builder struct 不显著降低耦合, 显式
// allow 比 boxing 更易读。
#[allow(clippy::too_many_arguments)]
fn relay_body<B>(
    mut body: B,
    request_id: RequestId,
    direction: &'static str,
    stream_tap: Option<StreamTapConfig>,
    non_stream_protocol: Option<StreamProtocol>,
    terminal: RelayTerminal,
    in_flight_guard: Option<InFlightRequestGuard>,
    timeouts: ProxyTimeouts,
) -> Body
where
    B: http_body::Body<Data = Bytes> + Send + Unpin + 'static,
    B::Error: fmt::Display + Send + Sync + 'static,
{
    let (sender, receiver) = mpsc::channel::<BodyChunk>(STREAM_CHANNEL_DEPTH);
    let drop_reporter = terminal.reporter.clone();
    // Owner item 4 fix 2026-05-24: 上游分类快照透传给 ReceiverByteStream Drop, 防误报 ClientCancel。
    let upstream_terminal_status_snapshot = terminal.status;
    let upstream_terminal_http_status_snapshot = terminal.http_status;

    let task = task::spawn(async move {
        let mut stream_pipeline = stream_tap
            .as_ref()
            .map(|tap| StreamPipeline::new(tap.protocol, tap.max_frame_bytes));
        let mut stats = AttemptReportStats::default();
        let mut stream_seen_done = false;
        let stream_requires_done =
            stream_pipeline.is_some() && terminal.status == AttemptStatus::Success;
        // W12-B D-5: 非流式 + JSON + 2xx 路径上累积 body 字节, body 完成后解析 usage。
        // exceeded 一旦置 true (累计超 cap), 终态报 pending_reconciliation 不再继续 buffer。
        let mut non_stream_buffer: Option<Vec<u8>> =
            non_stream_protocol.map(|_| Vec::with_capacity(8 * 1024));
        let mut non_stream_buffer_exceeded = false;

        loop {
            let frame =
                read_body_frame_with_idle_timeout(&mut body, timeouts.upstream_body_idle_timeout)
                    .await;
            let frame = match frame {
                Ok(frame) => frame,
                Err(BodyIdleElapsed) => {
                    let err = io::Error::new(io::ErrorKind::TimedOut, "body stream idle timeout");
                    report_terminal(
                        terminal.reporter.as_ref(),
                        AttemptStatus::Timeout,
                        terminal.http_status,
                        &stats,
                        Some("timeout"),
                        Some("body stream idle timeout"),
                    );
                    emit_stream_observation(
                        stream_tap.as_ref(),
                        StreamEvent::UpstreamError("body stream idle timeout".to_owned()),
                    );
                    let _ =
                        send_downstream(&sender, Err(err), timeouts.downstream_write_idle_timeout)
                            .await;
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
                                terminal.reporter.as_ref(),
                                terminal.http_status,
                                &mut stats,
                                &mut stream_seen_done,
                            );
                        }
                        // W12-B D-5: 非流式路径累积 body 至 cap; 超出标记 exceeded 停止累积。
                        if let Some(buf) = non_stream_buffer.as_mut() {
                            if buf.len().saturating_add(data.len()) > NON_STREAM_USAGE_PARSE_CAP {
                                non_stream_buffer_exceeded = true;
                                non_stream_buffer = None;
                            } else {
                                buf.extend_from_slice(&data);
                            }
                        }
                        match send_downstream(
                            &sender,
                            Ok(data),
                            timeouts.downstream_write_idle_timeout,
                        )
                        .await
                        {
                            DownstreamSend::Sent => {}
                            DownstreamSend::Closed => {
                                // 第三方 P2 finding 2026-05-24: upstream 已经报 4xx/5xx 但 client
                                // 在 relay 期间断开时, 旧实现硬报 ClientCancel 抢先盖掉 upstream
                                // 终态 — Drop 后无法纠正 (relay 比 Drop 先报)。改: 保留 upstream
                                // terminal status, 让 upstream 故障归 upstream 而非客户取消。
                                let (status, class, msg) =
                                    classify_downstream_failure_terminal(
                                        terminal.status,
                                        "client_cancel",
                                        "client disconnected while relaying upstream response",
                                    );
                                report_terminal(
                                    terminal.reporter.as_ref(),
                                    status,
                                    terminal.http_status,
                                    &stats,
                                    Some(class),
                                    Some(msg),
                                );
                                debug!(request_id = %request_id, direction, "client disconnected, abort relay");
                                break;
                            }
                            DownstreamSend::TimedOut => {
                                let (status, class, msg) =
                                    classify_downstream_failure_terminal(
                                        terminal.status,
                                        "client_slow_or_disconnected",
                                        "client stopped reading relayed upstream response",
                                    );
                                report_terminal(
                                    terminal.reporter.as_ref(),
                                    status,
                                    terminal.http_status,
                                    &stats,
                                    Some(class),
                                    Some(msg),
                                );
                                warn!(
                                    request_id = %request_id,
                                    direction,
                                    "downstream write idle timeout"
                                );
                                break;
                            }
                        }
                    }
                    Err(_) => {
                        debug!(request_id = %request_id, direction, "body trailer ignored");
                    }
                },
                Some(Err(err)) => {
                    let msg = format!("body stream error: {err}");
                    report_terminal(
                        terminal.reporter.as_ref(),
                        AttemptStatus::NetworkError,
                        terminal.http_status,
                        &stats,
                        Some("network_error"),
                        Some(&msg),
                    );
                    emit_stream_observation(
                        stream_tap.as_ref(),
                        StreamEvent::UpstreamError(msg.clone()),
                    );
                    let _ = send_downstream(
                        &sender,
                        Err(io::Error::new(io::ErrorKind::BrokenPipe, msg)),
                        timeouts.downstream_write_idle_timeout,
                    )
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
                            terminal.reporter.as_ref(),
                            terminal.http_status,
                            &mut stats,
                            &mut stream_seen_done,
                        );
                    }
                    // W12-B D-5: 非流式 body 完成 → 尝试解析 usage 并写权威 source。
                    // mutation: 删本块 → 非流式 attempt 的 tokens_used 永远 missing 而非
                    // response_body / pending_reconciliation, 控制面对账失去 reconciliation 信号。
                    if let Some(protocol) = non_stream_protocol {
                        if non_stream_buffer_exceeded {
                            stats.record_response_body_usage_unparsable();
                        } else if let Some(buf) = non_stream_buffer.as_ref() {
                            parse_non_stream_usage(protocol, buf, &mut stats);
                        }
                    }
                    if stream_requires_done && !stream_seen_done {
                        report_terminal(
                            terminal.reporter.as_ref(),
                            AttemptStatus::ProtocolError,
                            terminal.http_status,
                            &stats,
                            Some("protocol_error"),
                            Some("stream ended without DONE/message_stop"),
                        );
                    } else {
                        report_terminal(
                            terminal.reporter.as_ref(),
                            terminal.status,
                            terminal.http_status,
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
        in_flight_guard,
        upstream_terminal_status: upstream_terminal_status_snapshot,
        upstream_terminal_http_status: upstream_terminal_http_status_snapshot,
    })
}

struct BodyIdleElapsed;

async fn read_body_frame_with_idle_timeout<B>(
    body: &mut B,
    idle_timeout: Option<Duration>,
) -> Result<Option<Result<http_body::Frame<Bytes>, B::Error>>, BodyIdleElapsed>
where
    B: http_body::Body<Data = Bytes> + Unpin,
{
    let Some(idle_timeout) = idle_timeout else {
        return Ok(body.frame().await);
    };

    tokio::select! {
        frame = body.frame() => Ok(frame),
        () = time::sleep(idle_timeout) => Err(BodyIdleElapsed),
    }
}

enum DownstreamSend {
    Sent,
    Closed,
    TimedOut,
}

async fn send_downstream(
    sender: &Sender<BodyChunk>,
    chunk: BodyChunk,
    write_idle_timeout: Option<Duration>,
) -> DownstreamSend {
    let Some(write_idle_timeout) = write_idle_timeout else {
        return match sender.send(chunk).await {
            Ok(()) => DownstreamSend::Sent,
            Err(_) => DownstreamSend::Closed,
        };
    };

    match time::timeout(write_idle_timeout, sender.send(chunk)).await {
        Ok(Ok(())) => DownstreamSend::Sent,
        Ok(Err(_)) => DownstreamSend::Closed,
        Err(_) => DownstreamSend::TimedOut,
    }
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

/// 第三方 P2 finding 2026-05-24: relay 期间下游 send 失败 / write idle 时, 必须
/// 保留 upstream terminal 分类 — 不能用 ClientCancel 抢先覆盖 upstream 4xx/5xx 实情。
///
/// 旧实现 (mod.rs ReceiverByteStream::Drop 已修但只覆盖 Drop 路径): relay task
/// 仍硬报 ClientCancel; relay 比 Drop 先 fire (它是 producer), 所以 upstream
/// 5xx + client 断开场景 -> ClientCancel 抢先报 -> upstream 故障算客户责任 = 反账务。
///
/// 修复: upstream 是 Success 时才报 ClientCancel; 否则透传 upstream 分类 (Upstream4xx /
/// Upstream5xx / Timeout / NetworkError ...) + 在 error_class/message 里标 + downstream
/// 失败上下文, 让 audit 同时能看到 "上游真因 + 下游断开" 两份信息。
///
/// 返回: (terminal_status, error_class, error_message)
fn classify_downstream_failure_terminal(
    upstream_status: AttemptStatus,
    downstream_class: &'static str,
    downstream_msg: &'static str,
) -> (AttemptStatus, &'static str, &'static str) {
    if upstream_status == AttemptStatus::Success {
        // upstream 一切正常, 下游真断开 — 客户责任
        (AttemptStatus::ClientCancel, downstream_class, downstream_msg)
    } else {
        // upstream 已经报 4xx/5xx/Timeout/NetworkError — 保留 upstream 分类避免
        // 反账务。class 用 upstream 的 "upstream_terminal_*" 让 audit 一眼能看出
        // 是上游先坏 + 下游也断开 (两个事件叠加, 但归因仍是 upstream)。
        (
            upstream_status,
            "upstream_terminal_then_client_cancel",
            "upstream returned non-success and client disconnected during relay; preserving upstream classification",
        )
    }
}

pub(super) fn report_terminal(
    terminal_reporter: Option<&AttemptTerminalReporter>,
    status: AttemptStatus,
    http_status: Option<u16>,
    stats: &AttemptReportStats,
    error_class: Option<&str>,
    error_message_redacted: Option<&str>,
) {
    if let Some(reporter) = terminal_reporter {
        // W12-A D-4 Slice 3 AC-4-post: 流式 body 终态 = 响应头已送出 → HTTP 不可改, 失败 loud。
        // 调用方: relay.rs 内 9 处全是 streaming body 完成路径 = post-commit;
        // 注意 proxy_engine/mod.rs::report_proxy_error 路径 (forward_inner err 前 response 未送) 用 report_terminal_pre_commit。
        let _ = reporter.report_post_commit(
            status,
            http_status,
            stats.clone(),
            error_class,
            error_message_redacted,
        );
    }
}

/// W12-A D-4 Slice 3 (Codex P2-1 fix 2026-05-24): pre-commit 版本 - forward_inner 失败前
/// response headers 未送, HTTP 仍可改 (caller 返 5xx/4xx) - 不能算 post-commit billable loss。
pub(super) fn report_terminal_pre_commit(
    terminal_reporter: Option<&AttemptTerminalReporter>,
    status: AttemptStatus,
    http_status: Option<u16>,
    stats: &AttemptReportStats,
    error_class: Option<&str>,
    error_message_redacted: Option<&str>,
) {
    if let Some(reporter) = terminal_reporter {
        let _ = reporter.report_pre_commit(
            status,
            http_status,
            stats.clone(),
            error_class,
            error_message_redacted,
        );
    }
}

/// W12-B D-5: 非流式 2xx body 解析 usage; 解析失败 / 不完整 → pending_reconciliation,
/// 让控制面对账时知道"已检查过 body, 仅 vendor 字段不可读 / 不全"区别于"从未检查"。
/// mutation: 把成功分支改成 record_response_body_usage_unparsable →
/// non_stream_openai_usage_parses_response_body_source 测试断言红 (source != "response_body")。
fn parse_non_stream_usage(
    protocol: StreamProtocol,
    body: &[u8],
    stats: &mut AttemptReportStats,
) {
    let parsed = match protocol {
        StreamProtocol::OpenAi => {
            crate::stream_pipeline::openai::extract_usage_from_json_bytes(body)
        }
        StreamProtocol::Anthropic => {
            crate::stream_pipeline::anthropic::extract_usage_from_json_bytes(body)
        }
    };

    match parsed {
        Ok(Some(delta)) if is_complete_usage(&delta) => stats.record_response_body_usage(&delta),
        // P2 codex 2026-05-24: 不完整 usage (input/output 任一为 0) 不能当 authoritative,
        // 会让控制面把不全数据当真账务。降级 pending_reconciliation 让对账兜底。
        Ok(_) | Err(_) => stats.record_response_body_usage_unparsable(),
    }
}

/// W12-B D-5 + P2 codex: 一份 usage delta 只有 input+output 同时 > 0 才算 authoritative,
/// 否则按 pending_reconciliation 处理 (vendor 返了部分字段 / 缺关键字段)。
fn is_complete_usage(delta: &crate::stream_pipeline::UsageDelta) -> bool {
    delta.input_tokens > 0 && delta.output_tokens > 0
}

#[cfg(test)]
mod d5_parse_tests {
    use super::*;

    /// W12-B D-5 判别: OpenAI 非流式 200 body 含 usage → source="response_body" + 真实计数。
    /// mutation: 删 parse_non_stream_usage OpenAI 分支或改 record_response_body_usage_unparsable →
    /// source 不再是 "response_body", 断言红。
    #[test]
    fn parse_non_stream_usage_openai_writes_response_body_source_with_real_tokens() {
        let body = br#"{"id":"x","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}"#;
        let mut stats = AttemptReportStats::default();

        parse_non_stream_usage(StreamProtocol::OpenAi, body, &mut stats);

        let tokens = stats.tokens_used.expect("应有 tokens_used");
        assert_eq!(tokens.source, "response_body", "OpenAI 解析成功必须 source=response_body");
        assert_eq!(tokens.input_tokens, 100);
        assert_eq!(tokens.output_tokens, 50);
        assert_eq!(tokens.total_tokens, 150);
    }

    /// W12-B D-5 判别: malformed JSON → pending_reconciliation (区别于 missing)。
    /// mutation: 把失败分支改回不调用 record_response_body_usage_unparsable → tokens_used.is_none()
    /// → unwrap panic → 测试红。
    #[test]
    fn parse_non_stream_usage_malformed_json_writes_pending_reconciliation() {
        let body = b"not a valid json {";
        let mut stats = AttemptReportStats::default();

        parse_non_stream_usage(StreamProtocol::OpenAi, body, &mut stats);

        let tokens = stats.tokens_used.expect("应有 tokens_used 即使解析失败");
        assert_eq!(
            tokens.source, "pending_reconciliation",
            "malformed body 必须 source=pending_reconciliation 区别 missing"
        );
        // pending_reconciliation 不携带真实 token, 控制面不能误用为账务依据
        assert_eq!(tokens.input_tokens, 0);
        assert_eq!(tokens.output_tokens, 0);
        assert_eq!(tokens.total_tokens, 0);
    }

    /// W12-B D-5: usage 字段缺失但 JSON 合法 → 也归 pending_reconciliation
    /// (源已检查过 body, 仅 vendor 没返 usage)。
    #[test]
    fn parse_non_stream_usage_json_without_usage_field_writes_pending_reconciliation() {
        let body = br#"{"id":"x","choices":[]}"#;
        let mut stats = AttemptReportStats::default();

        parse_non_stream_usage(StreamProtocol::OpenAi, body, &mut stats);

        let tokens = stats.tokens_used.expect("应有 tokens_used");
        assert_eq!(tokens.source, "pending_reconciliation");
    }

    /// W12-B D-5: Anthropic 非流式 body 解析 usage 成功 → response_body + 真实计数。
    /// mutation: 把 Anthropic 分支改 record_response_body_usage_unparsable → 此测试断言红。
    #[test]
    fn parse_non_stream_usage_anthropic_writes_response_body_source_with_real_tokens() {
        let body = br#"{"id":"x","type":"message","usage":{"input_tokens":12,"output_tokens":34}}"#;
        let mut stats = AttemptReportStats::default();

        parse_non_stream_usage(StreamProtocol::Anthropic, body, &mut stats);

        let tokens = stats.tokens_used.expect("应有 tokens_used");
        assert_eq!(tokens.source, "response_body");
        assert_eq!(tokens.input_tokens, 12);
        assert_eq!(tokens.output_tokens, 34);
        // Anthropic 不提供 total → input + output 推导
        assert_eq!(tokens.total_tokens, 46);
    }

    /// P2 codex 2026-05-24: 不完整 usage (input/output 任一为 0) 不能标 authoritative
    /// 否则控制面把不全数据当真账务。降 pending_reconciliation。
    /// mutation: 删 is_complete_usage filter → tokens.source 变成 "response_body" 红。
    #[test]
    fn parse_non_stream_usage_incomplete_openai_falls_to_pending_reconciliation() {
        // 仅 total_tokens, 缺 prompt/completion → 不完整, 不能当真值
        let body = br#"{"usage":{"total_tokens":150}}"#;
        let mut stats = AttemptReportStats::default();

        parse_non_stream_usage(StreamProtocol::OpenAi, body, &mut stats);

        let tokens = stats.tokens_used.expect("应有 tokens_used");
        assert_eq!(
            tokens.source, "pending_reconciliation",
            "input/output 任一为 0 必须按不完整处理, 实际: source={}",
            tokens.source
        );
        // 不完整数据不能携带 token 值, 控制面对账无法误用
        assert_eq!(tokens.input_tokens, 0);
        assert_eq!(tokens.output_tokens, 0);
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

#[cfg(test)]
mod tests {
    use std::{
        io,
        sync::{
            Arc,
            atomic::{AtomicUsize, Ordering},
        },
        time::Duration,
    };

    use axum::body;
    use futures_util::stream;
    use http_body_util::BodyExt;

    use super::*;
    use crate::{proxy_engine::ProxyTimeouts, request_id::RequestId};

    fn test_terminal() -> RelayTerminal {
        RelayTerminal {
            reporter: None,
            status: AttemptStatus::Success,
            http_status: Some(200),
        }
    }

    fn test_timeouts(upstream_idle_ms: u64, downstream_write_idle_ms: u64) -> ProxyTimeouts {
        ProxyTimeouts {
            upstream_body_idle_timeout: Some(Duration::from_millis(upstream_idle_ms)),
            downstream_write_idle_timeout: Some(Duration::from_millis(downstream_write_idle_ms)),
        }
    }

    #[tokio::test]
    async fn relay_body_uses_configured_upstream_body_idle_timeout() {
        let source = stream::unfold(0usize, |idx| async move {
            match idx {
                0 => Some((Ok::<Bytes, io::Error>(Bytes::from_static(b"first")), 1)),
                1 => {
                    tokio::time::sleep(Duration::from_millis(80)).await;
                    Some((Ok(Bytes::from_static(b"second")), 2))
                }
                _ => None,
            }
        });
        let mut relayed = relay_body(
            Body::from_stream(source),
            RequestId::from_candidate(Some("relay-idle-red")),
            "test",
            None,
            None,
            test_terminal(),
            None,
            test_timeouts(20, 500),
        );

        let first = relayed
            .frame()
            .await
            .expect("首帧应存在")
            .expect("首帧应成功")
            .into_data()
            .expect("首帧应为 data");
        assert_eq!(first, Bytes::from_static(b"first"));

        let second = tokio::time::timeout(Duration::from_millis(200), relayed.frame())
            .await
            .expect("超时错误帧应及时返回")
            .expect("应返回一个错误帧");
        let err = second.expect_err("超过 upstream idle 后应返回 body error");
        assert!(
            err.to_string().contains("body stream idle timeout"),
            "err={err}"
        );
    }

    #[tokio::test]
    async fn relay_body_allows_longer_configured_upstream_idle_gap() {
        let source = stream::unfold(0usize, |idx| async move {
            match idx {
                0 => Some((Ok::<Bytes, io::Error>(Bytes::from_static(b"first")), 1)),
                1 => {
                    tokio::time::sleep(Duration::from_millis(40)).await;
                    Some((Ok(Bytes::from_static(b"second")), 2))
                }
                _ => None,
            }
        });
        let relayed = relay_body(
            Body::from_stream(source),
            RequestId::from_candidate(Some("relay-idle-green")),
            "test",
            None,
            None,
            test_terminal(),
            None,
            test_timeouts(200, 500),
        );

        let body = body::to_bytes(relayed, 64)
            .await
            .expect("较长 upstream idle 配置应允许慢帧");
        assert_eq!(body, Bytes::from_static(b"firstsecond"));
    }

    /// 第三方 P2 finding 2026-05-24: classify_downstream_failure_terminal 必须保留
    /// upstream 非 Success 终态; 只在 upstream 是 Success 时报 ClientCancel。
    /// 否则 upstream 5xx + relay 期间 client drop 场景 -> ClientCancel 抢先盖 upstream 5xx
    /// = 反账务 (upstream 故障算客户责任)。
    ///
    /// 判别性 + mutation:
    /// 1) upstream=Success -> 返 ClientCancel + 原 downstream_class
    /// 2) upstream=Upstream5xx -> 返 Upstream5xx + "upstream_terminal_then_client_cancel"
    /// 3) upstream=Upstream4xx -> 返 Upstream4xx + "upstream_terminal_then_client_cancel"
    /// 4) upstream=Timeout -> 返 Timeout (不归 ClientCancel)
    /// 5) upstream=NetworkError -> 返 NetworkError
    ///
    /// mutation:
    /// - 把 fn body 改回 `(AttemptStatus::ClientCancel, ...)` 无条件 -> 2/3/4/5 红。
    /// - 删 upstream_status == Success 分支 -> 1 不再返 ClientCancel -> 红。
    /// - 把 error_class 改回 "client_cancel" -> 2/3/4/5 error_class 检查红。
    #[test]
    fn classify_downstream_failure_terminal_preserves_upstream_non_success_status() {
        // Case 1: upstream Success -> 正常 ClientCancel 路径
        let (status, class, msg) = classify_downstream_failure_terminal(
            AttemptStatus::Success,
            "client_cancel",
            "client disconnected while relaying upstream response",
        );
        assert_eq!(
            status,
            AttemptStatus::ClientCancel,
            "upstream Success + 下游断开 = 真客户取消, 应报 ClientCancel"
        );
        assert_eq!(class, "client_cancel");
        assert!(msg.contains("client disconnected"));

        // Case 2: upstream Upstream5xx -> 保留 Upstream5xx
        let (status, class, msg) = classify_downstream_failure_terminal(
            AttemptStatus::Upstream5xx,
            "client_cancel",
            "ignored downstream msg",
        );
        assert_eq!(
            status,
            AttemptStatus::Upstream5xx,
            "upstream 5xx + 下游断开 -> 必须保留 Upstream5xx 而非 ClientCancel (反账务防护)"
        );
        assert_eq!(
            class, "upstream_terminal_then_client_cancel",
            "error_class 应标 upstream-then-client 让 audit 一眼看出归因"
        );
        assert!(
            msg.contains("preserving upstream classification"),
            "msg 应解释为何保留 upstream 分类, 实际: {msg}"
        );

        // Case 3: upstream Upstream4xx -> 保留 Upstream4xx
        let (status, _, _) = classify_downstream_failure_terminal(
            AttemptStatus::Upstream4xx,
            "client_slow_or_disconnected",
            "ignored",
        );
        assert_eq!(
            status,
            AttemptStatus::Upstream4xx,
            "upstream 4xx + 下游断开 -> 必须保留 Upstream4xx"
        );

        // Case 4: upstream Timeout -> 保留 Timeout
        let (status, _, _) = classify_downstream_failure_terminal(
            AttemptStatus::Timeout,
            "client_cancel",
            "ignored",
        );
        assert_eq!(
            status,
            AttemptStatus::Timeout,
            "upstream Timeout + 下游断开 -> 必须保留 Timeout"
        );

        // Case 5: upstream NetworkError -> 保留 NetworkError
        let (status, _, _) = classify_downstream_failure_terminal(
            AttemptStatus::NetworkError,
            "client_slow_or_disconnected",
            "ignored",
        );
        assert_eq!(
            status,
            AttemptStatus::NetworkError,
            "upstream NetworkError + 下游断开 -> 必须保留 NetworkError"
        );
    }

    #[tokio::test]
    async fn relay_body_stops_when_downstream_write_idle_timeout_elapses() {
        let produced = Arc::new(AtomicUsize::new(0));
        let produced_for_stream = produced.clone();
        let source = stream::unfold(0usize, move |idx| {
            let produced = produced_for_stream.clone();
            async move {
                if idx >= 128 {
                    return None;
                }
                produced.fetch_add(1, Ordering::SeqCst);
                Some((
                    Ok::<Bytes, io::Error>(Bytes::from(vec![b'x'; 1024])),
                    idx + 1,
                ))
            }
        });
        let relayed = relay_body(
            Body::from_stream(source),
            RequestId::from_candidate(Some("relay-downstream-stall")),
            "test",
            None,
            None,
            test_terminal(),
            None,
            test_timeouts(1_000, 30),
        );

        tokio::time::sleep(Duration::from_millis(150)).await;
        let produced_after_timeout = produced.load(Ordering::SeqCst);
        drop(relayed);

        assert!(
            produced_after_timeout < 128,
            "下游不读时 relay 应在 write idle 超时后停止继续拉上游, produced={produced_after_timeout}"
        );
    }
}
