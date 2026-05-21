use std::{fmt, io};

use axum::body::Body;
use bytes::Bytes;
use http::{Response, header::CONTENT_TYPE};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use tokio::{
    sync::{mpsc, mpsc::error::TrySendError},
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
    BODY_IDLE_TIMEOUT, BodyChunk, ReceiverByteStream, STREAM_CHANNEL_DEPTH, default_content_type,
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
    terminal_reporter: Option<AttemptTerminalReporter>,
    terminal_status: AttemptStatus,
    terminal_http_status: Option<u16>,
    in_flight_guard: Option<InFlightRequestGuard>,
) -> Response<Body> {
    let (mut parts, body) = response.into_parts();
    remove_hop_by_hop_response_headers(&mut parts.headers);
    set_request_id(&mut parts.headers, request_id);
    if !parts.headers.contains_key(CONTENT_TYPE) {
        parts.headers.insert(CONTENT_TYPE, default_content_type());
    }
    let stream_tap = stream_tap.filter(|_| is_sse_response(&parts.headers));
    let terminal = RelayTerminal {
        reporter: terminal_reporter,
        status: terminal_status,
        http_status: terminal_http_status,
    };
    Response::from_parts(
        parts,
        relay_body(
            body,
            request_id.clone(),
            "upstream_response",
            stream_tap,
            terminal,
            in_flight_guard,
        ),
    )
}

struct RelayTerminal {
    reporter: Option<AttemptTerminalReporter>,
    status: AttemptStatus,
    http_status: Option<u16>,
}

fn relay_body<B>(
    mut body: B,
    request_id: RequestId,
    direction: &'static str,
    stream_tap: Option<StreamTapConfig>,
    terminal: RelayTerminal,
    in_flight_guard: Option<InFlightRequestGuard>,
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
            let frame = tokio::select! {
                frame = body.frame() => frame,
                () = time::sleep(BODY_IDLE_TIMEOUT) => {
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
                                terminal.reporter.as_ref(),
                                terminal.http_status,
                                &mut stats,
                                &mut stream_seen_done,
                            );
                        }
                        if sender.send(Ok(data)).await.is_err() {
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
