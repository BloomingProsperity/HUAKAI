use bytes::Bytes;
use core_gateway::stream_pipeline::{
    CacheDelta, StreamEvent, StreamPipeline, StreamProtocol, UsageDelta,
    openai::extract_usage_from_json_bytes,
    sse::{SseFrame, SseItem, SseScanner},
};

fn collect(protocol: StreamProtocol, chunks: &[&[u8]]) -> Vec<StreamEvent> {
    let mut pipeline = StreamPipeline::new(protocol, 64 * 1024);
    let mut events = Vec::new();
    for chunk in chunks {
        events.extend(pipeline.push_bytes(chunk));
    }
    events.extend(pipeline.finish());
    events
}

fn single_frame(items: Vec<SseItem>) -> SseFrame {
    assert_eq!(items.len(), 1);
    match items.into_iter().next().unwrap() {
        SseItem::Frame(frame) => frame,
        SseItem::ProtocolError(message) => panic!("unexpected protocol error: {message}"),
    }
}

#[test]
fn sse_scanner_handles_lf_crlf_comments_and_boundaries() {
    let mut scanner = SseScanner::new(1024);
    let frame = single_frame(
        scanner.push_bytes(b": ignored\r\nevent: message_delta\r\ndata: {\"ok\":true}\r\n\r\n"),
    );

    assert_eq!(frame.event_name(), Some("message_delta"));
    assert_eq!(frame.data, Bytes::from_static(br#"{"ok":true}"#));
    assert_eq!(frame.data_line_count, 1);
}

#[test]
fn sse_scanner_keeps_partial_lines_until_boundary() {
    let mut scanner = SseScanner::new(1024);
    assert!(scanner.push_bytes(b"data: hel").is_empty());
    let frame = single_frame(scanner.push_bytes(b"lo\n\n"));

    assert_eq!(frame.data, Bytes::from_static(b"hello"));
}

#[test]
fn sse_scanner_joins_multiple_data_lines_with_newline() {
    let mut scanner = SseScanner::new(1024);
    let frame = single_frame(scanner.push_bytes(b"data: {\"a\":\ndata: 1}\n\n"));

    assert_eq!(frame.data, Bytes::from_static(b"{\"a\":\n1}"));
    assert_eq!(frame.data_line_count, 2);
}

#[test]
fn sse_scanner_reports_oversized_partial_line() {
    let mut scanner = SseScanner::new(12);
    let items = scanner.push_bytes(b"data: 0123456789012345\n\n");

    assert!(
        matches!(items.as_slice(), [SseItem::ProtocolError(message)] if message.contains("max frame"))
    );
}

#[test]
fn anthropic_golden_stream_extracts_usage_cache_and_done() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[b"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":2,\"cache_read_input_tokens\":3}}}\n\n\
           event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n\
           event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n\
           event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"],
    );

    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 10,
        output_tokens: 0,
        total_tokens: 10,
    })));
    assert!(events.contains(&StreamEvent::CacheMetric(CacheDelta {
        cache_creation_input_tokens: 2,
        cache_read_input_tokens: 3,
    })));
    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 0,
        output_tokens: 4,
        total_tokens: 4,
    })));
    assert!(events.contains(&StreamEvent::Done));
}

#[test]
fn anthropic_content_delta_alias_is_treated_as_data() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[b"event: content_delta\ndata: {\"type\":\"content_delta\",\"text\":\"x\"}\n\n"],
    );

    assert!(events.contains(&StreamEvent::Data(Bytes::from_static(
        br#"{"type":"content_delta","text":"x"}"#
    ))));
}

/// Owner item 3 fix 2026-05-24: Anthropic 真实 SSE 合法事件 ping / content_block_start /
/// content_block_stop 必须 Data 透传, 不产生 ProtocolError noise。
///
/// 旧 whitelist 缺这 3 个 -> 走 `other` 分支 -> 每条都触发 ProtocolError -> metric 噪声 + 误导审计。
///
/// mutation: 删 ping / content_block_start / content_block_stop 任一 -> ProtocolError 事件出现 ->
/// 测试断言 0 ProtocolError 红 + Data 透传断言可能也红。
#[test]
fn anthropic_legitimate_lifecycle_events_pass_through_without_protocol_error() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[
            b"event: ping\ndata: {\"type\":\"ping\"}\n\n\
              event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n\
              event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
        ],
    );

    // 关键断言: 这 3 个合法事件不应触发 ProtocolError
    let protocol_errors: Vec<_> = events
        .iter()
        .filter(|e| matches!(e, StreamEvent::ProtocolError(_)))
        .collect();
    assert!(
        protocol_errors.is_empty(),
        "Owner item 3: ping/content_block_start/content_block_stop 是 Anthropic 合法 SSE 事件, \
         不应产生 ProtocolError。实际 {protocol_errors:?}"
    );

    // 三条 data 必须以 Data event 透传
    let data_events: Vec<_> = events
        .iter()
        .filter_map(|e| {
            if let StreamEvent::Data(b) = e {
                Some(b.as_ref())
            } else {
                None
            }
        })
        .collect();
    assert_eq!(
        data_events.len(),
        3,
        "三条合法事件每条都应产生一个 Data event 透传到 client, 实际 {} (events={events:?})",
        data_events.len()
    );
    assert!(data_events.iter().any(|d| d.starts_with(br#"{"type":"ping""#)));
    assert!(
        data_events
            .iter()
            .any(|d| d.starts_with(br#"{"type":"content_block_start""#))
    );
    assert!(
        data_events
            .iter()
            .any(|d| d.starts_with(br#"{"type":"content_block_stop""#))
    );
}

#[test]
fn anthropic_error_event_maps_to_upstream_error() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[b"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"try later\"}}\n\n"],
    );

    assert_eq!(
        events,
        vec![StreamEvent::UpstreamError("try later".to_owned())]
    );
}

#[test]
fn anthropic_non_usage_bad_json_is_forwarded_without_protocol_error() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[b"event: content_delta\ndata: {bad-json}\n\n"],
    );

    assert_eq!(
        events,
        vec![StreamEvent::Data(Bytes::from_static(b"{bad-json}"))]
    );
}

#[test]
fn anthropic_usage_bad_json_reports_protocol_error_and_continues() {
    let events = collect(
        StreamProtocol::Anthropic,
        &[b"event: message_delta\ndata: {\"usage\":\n\n\
           event: message_delta\ndata: {\"usage\":{\"output_tokens\":6}}\n\n"],
    );

    assert!(
        events
            .iter()
            .any(|event| matches!(event, StreamEvent::ProtocolError(message) if message.contains("JSON parse failed")))
    );
    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 0,
        output_tokens: 6,
        total_tokens: 6,
    })));
}

#[test]
fn openai_golden_stream_extracts_last_chunk_usage_and_done() {
    let events = collect(
        StreamProtocol::OpenAi,
        &[b"data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n\
           data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n\n\
           data: [DONE]\n\n"],
    );

    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 5,
        output_tokens: 7,
        total_tokens: 12,
    })));
    assert!(events.contains(&StreamEvent::Done));
    assert_eq!(
        events
            .iter()
            .filter(|event| matches!(event, StreamEvent::Data(_)))
            .count(),
        2
    );
}

#[test]
fn openai_parser_handles_partial_line_across_chunks() {
    let events = collect(
        StreamProtocol::OpenAi,
        &[b"data: {\"choices\"", b":[{\"delta\":{}}]}\r\n\r\n"],
    );

    assert_eq!(
        events,
        vec![StreamEvent::Data(Bytes::from_static(
            br#"{"choices":[{"delta":{}}]}"#
        ))]
    );
}

#[test]
fn openai_parser_tolerates_multiline_data_frame() {
    let events = collect(
        StreamProtocol::OpenAi,
        &[b"data: {\"usage\":\ndata: {\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"],
    );

    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 1,
        output_tokens: 2,
        total_tokens: 3,
    })));
}

#[test]
fn openai_done_frame_does_not_emit_data() {
    let events = collect(
        StreamProtocol::OpenAi,
        &[b": keepalive\n\ndata: [DONE]\n\n"],
    );

    assert_eq!(events, vec![StreamEvent::Done]);
}

#[test]
fn openai_non_usage_bad_json_is_forwarded_without_protocol_error() {
    let events = collect(StreamProtocol::OpenAi, &[b"data: {bad-json}\n\n"]);

    assert_eq!(
        events,
        vec![StreamEvent::Data(Bytes::from_static(b"{bad-json}"))]
    );
}

#[test]
fn openai_usage_bad_json_reports_protocol_error_without_stopping_next_frame() {
    let events = collect(
        StreamProtocol::OpenAi,
        &[b"data: {\"usage\":\n\ndata: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"],
    );

    assert!(
        events
            .iter()
            .any(|event| matches!(event, StreamEvent::ProtocolError(message) if message.contains("JSON parse failed")))
    );
    assert!(events.contains(&StreamEvent::Usage(UsageDelta {
        input_tokens: 2,
        output_tokens: 3,
        total_tokens: 5,
    })));
}

#[test]
fn openai_non_stream_json_usage_can_be_extracted() {
    let usage = extract_usage_from_json_bytes(
        br#"{"id":"chatcmpl","usage":{"input_tokens":8,"output_tokens":13,"total_tokens":21}}"#,
    )
    .expect("non-stream JSON 应可解析")
    .expect("usage 应存在");

    assert_eq!(
        usage,
        UsageDelta {
            input_tokens: 8,
            output_tokens: 13,
            total_tokens: 21,
        }
    );
}
