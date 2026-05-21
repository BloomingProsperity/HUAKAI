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
    is_sse_response,
};

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
    let stream_tap = stream_tap.filter(|_| is_sse_response(&parts.headers));
    Response::from_parts(
        parts,
        relay_body(
            body,
            request_id.clone(),
            "upstream_response",
            stream_tap,
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

fn relay_body<B>(
    mut body: B,
    request_id: RequestId,
    direction: &'static str,
    stream_tap: Option<StreamTapConfig>,
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

    let task = task::spawn(async move {
        let mut stream_pipeline = stream_tap
            .as_ref()
            .map(|tap| StreamPipeline::new(tap.protocol, tap.max_frame_bytes));
        let mut stats = AttemptReportStats::default();
        let mut stream_seen_done = false;
        let stream_requires_done =
            stream_pipeline.is_some() && terminal.status == AttemptStatus::Success;

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
                        match send_downstream(
                            &sender,
                            Ok(data),
                            timeouts.downstream_write_idle_timeout,
                        )
                        .await
                        {
                            DownstreamSend::Sent => {}
                            DownstreamSend::Closed => {
                                report_terminal(
                                    terminal.reporter.as_ref(),
                                    AttemptStatus::ClientCancel,
                                    terminal.http_status,
                                    &stats,
                                    Some("client_cancel"),
                                    Some("client disconnected while relaying upstream response"),
                                );
                                debug!(request_id = %request_id, direction, "client disconnected, abort relay");
                                break;
                            }
                            DownstreamSend::TimedOut => {
                                report_terminal(
                                    terminal.reporter.as_ref(),
                                    AttemptStatus::ClientCancel,
                                    terminal.http_status,
                                    &stats,
                                    Some("client_slow_or_disconnected"),
                                    Some("client stopped reading relayed upstream response"),
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

pub(super) fn report_terminal(
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
            test_terminal(),
            None,
            test_timeouts(200, 500),
        );

        let body = body::to_bytes(relayed, 64)
            .await
            .expect("较长 upstream idle 配置应允许慢帧");
        assert_eq!(body, Bytes::from_static(b"firstsecond"));
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
