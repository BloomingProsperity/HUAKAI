use memchr::memmem;
use serde::Deserialize;

use crate::stream_pipeline::{
    CacheDelta, StreamEvent, UsageDelta,
    sse::{SseItem, SseScanner},
};

#[derive(Debug)]
pub struct AnthropicStreamParser {
    scanner: SseScanner,
}

impl AnthropicStreamParser {
    pub fn new(max_frame_bytes: usize) -> Self {
        Self {
            scanner: SseScanner::new(max_frame_bytes),
        }
    }

    pub fn push_bytes(&mut self, chunk: &[u8]) -> Vec<StreamEvent> {
        let items = self.scanner.push_bytes(chunk);
        self.events_from_items(items)
    }

    pub fn finish(&mut self) -> Vec<StreamEvent> {
        let items = self.scanner.finish();
        self.events_from_items(items)
    }

    fn events_from_items(&self, items: Vec<SseItem>) -> Vec<StreamEvent> {
        let mut events = Vec::new();
        for item in items {
            match item {
                SseItem::Frame(frame) => {
                    let event_name = frame.event_name().unwrap_or("");
                    match event_name {
                        "message_stop" => events.push(StreamEvent::Done),
                        "error" => {
                            let message = parse_error_message(&frame.data)
                                .unwrap_or_else(|| "anthropic upstream error".to_owned());
                            events.push(StreamEvent::UpstreamError(message));
                        }
                        // Owner item 3 fix 2026-05-24: Anthropic 真实 SSE 事件集合 (docs/api/messages-streaming):
                        // message_start / ping / content_block_start / content_block_delta /
                        // content_block_stop / message_delta / message_stop / error。
                        // 旧 whitelist 漏 ping / content_block_start / content_block_stop -> 全走 other ->
                        // 误报 ProtocolError (功能不破, 但 metric 噪声 + 误导审计)。
                        // content_delta 是 HUAKAI 内部 alias 保留 (回兼老 test)。
                        "message_start"
                        | "content_delta"
                        | "content_block_start"
                        | "content_block_delta"
                        | "content_block_stop"
                        | "ping"
                        | "message_delta"
                        | "" => {
                            events.push(StreamEvent::Data(frame.data.clone()));
                            extract_json_metrics(&frame.data, &mut events);
                        }
                        other => {
                            events.push(StreamEvent::Data(frame.data.clone()));
                            events.push(StreamEvent::ProtocolError(format!(
                                "unknown anthropic SSE event {other:?}"
                            )));
                            extract_json_metrics(&frame.data, &mut events);
                        }
                    }
                }
                SseItem::ProtocolError(message) => events.push(StreamEvent::ProtocolError(message)),
            }
        }

        events
    }
}

fn extract_json_metrics(data: &[u8], events: &mut Vec<StreamEvent>) {
    if data.is_empty() || memmem::find(data, b"usage").is_none() {
        return;
    }

    let envelope = match serde_json::from_slice::<AnthropicMetricsEnvelope>(data) {
        Ok(envelope) => envelope,
        Err(err) => {
            events.push(StreamEvent::ProtocolError(format!(
                "anthropic SSE JSON parse failed: {err}"
            )));
            return;
        }
    };

    if let Some(usage) = anthropic_usage(&envelope)
        && !usage.is_empty()
    {
        events.push(StreamEvent::Usage(usage));
    }
    if let Some(cache) = anthropic_cache(&envelope)
        && !cache.is_empty()
    {
        events.push(StreamEvent::CacheMetric(cache));
    }
}

fn parse_error_message(data: &[u8]) -> Option<String> {
    let envelope = serde_json::from_slice::<AnthropicErrorEnvelope>(data).ok()?;
    envelope
        .error
        .and_then(|error| error.message)
        .or(envelope.message)
}

/// W12-B D-5: Anthropic 非流式 2xx body usage 解析。
/// Anthropic Messages API 响应顶层有 `usage: {input_tokens, output_tokens, ...}`,
/// 也可能嵌在 `message.usage` 内 — 复用现有 anthropic_usage 合并逻辑。
/// 返回 Err on JSON parse 错; Ok(None) on 合法 JSON 但无 usage 字段。
pub fn extract_usage_from_json_bytes(data: &[u8]) -> Result<Option<UsageDelta>, serde_json::Error> {
    let envelope = serde_json::from_slice::<AnthropicMetricsEnvelope>(data)?;
    Ok(anthropic_usage(&envelope))
}

#[derive(Debug, Deserialize)]
struct AnthropicMetricsEnvelope {
    #[serde(default)]
    usage: Option<AnthropicUsageFields>,
    #[serde(default)]
    message: Option<AnthropicMessageFields>,
}

#[derive(Clone, Copy, Debug, Deserialize)]
struct AnthropicMessageFields {
    #[serde(default)]
    usage: Option<AnthropicUsageFields>,
}

#[derive(Clone, Copy, Debug, Default, Deserialize)]
struct AnthropicUsageFields {
    #[serde(default)]
    input_tokens: u64,
    #[serde(default)]
    output_tokens: u64,
    #[serde(default)]
    total_tokens: u64,
    #[serde(default)]
    cache_creation_input_tokens: u64,
    #[serde(default)]
    cache_read_input_tokens: u64,
}

#[derive(Debug, Deserialize)]
struct AnthropicErrorEnvelope {
    #[serde(default)]
    error: Option<AnthropicErrorFields>,
    #[serde(default)]
    message: Option<String>,
}

#[derive(Debug, Deserialize)]
struct AnthropicErrorFields {
    #[serde(default)]
    message: Option<String>,
}

fn anthropic_usage(envelope: &AnthropicMetricsEnvelope) -> Option<UsageDelta> {
    let mut usage = UsageDelta::default();
    merge_usage_object(envelope.usage, &mut usage);
    merge_usage_object(
        envelope.message.and_then(|message| message.usage),
        &mut usage,
    );

    if usage.total_tokens == 0 {
        usage.total_tokens = usage.input_tokens.saturating_add(usage.output_tokens);
    }

    (!usage.is_empty()).then_some(usage)
}

fn anthropic_cache(envelope: &AnthropicMetricsEnvelope) -> Option<CacheDelta> {
    let mut cache = CacheDelta::default();
    merge_cache_object(envelope.usage, &mut cache);
    merge_cache_object(
        envelope.message.and_then(|message| message.usage),
        &mut cache,
    );

    (!cache.is_empty()).then_some(cache)
}

fn merge_usage_object(fields: Option<AnthropicUsageFields>, usage: &mut UsageDelta) {
    let Some(fields) = fields else {
        return;
    };

    usage.input_tokens = usage.input_tokens.saturating_add(fields.input_tokens);
    usage.output_tokens = usage.output_tokens.saturating_add(fields.output_tokens);
    usage.total_tokens = usage.total_tokens.saturating_add(fields.total_tokens);
}

fn merge_cache_object(fields: Option<AnthropicUsageFields>, cache: &mut CacheDelta) {
    let Some(fields) = fields else {
        return;
    };

    cache.cache_creation_input_tokens = cache
        .cache_creation_input_tokens
        .saturating_add(fields.cache_creation_input_tokens);
    cache.cache_read_input_tokens = cache
        .cache_read_input_tokens
        .saturating_add(fields.cache_read_input_tokens);
}
