use serde_json::Value;

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
                        "message_start"
                        | "content_delta"
                        | "content_block_delta"
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
    if data.is_empty() {
        return;
    }

    let value = match serde_json::from_slice::<Value>(data) {
        Ok(value) => value,
        Err(err) => {
            events.push(StreamEvent::ProtocolError(format!(
                "anthropic SSE JSON parse failed: {err}"
            )));
            return;
        }
    };

    if let Some(usage) = anthropic_usage(&value)
        && !usage.is_empty()
    {
        events.push(StreamEvent::Usage(usage));
    }
    if let Some(cache) = anthropic_cache(&value)
        && !cache.is_empty()
    {
        events.push(StreamEvent::CacheMetric(cache));
    }
}

fn parse_error_message(data: &[u8]) -> Option<String> {
    let value = serde_json::from_slice::<Value>(data).ok()?;
    value
        .get("error")
        .and_then(|error| error.get("message"))
        .and_then(Value::as_str)
        .or_else(|| value.get("message").and_then(Value::as_str))
        .map(ToOwned::to_owned)
}

fn anthropic_usage(value: &Value) -> Option<UsageDelta> {
    let mut usage = UsageDelta::default();
    merge_usage_object(value.get("usage"), &mut usage);
    merge_usage_object(
        value
            .get("message")
            .and_then(|message| message.get("usage")),
        &mut usage,
    );

    if usage.total_tokens == 0 {
        usage.total_tokens = usage.input_tokens.saturating_add(usage.output_tokens);
    }

    (!usage.is_empty()).then_some(usage)
}

fn anthropic_cache(value: &Value) -> Option<CacheDelta> {
    let mut cache = CacheDelta::default();
    merge_cache_object(value.get("usage"), &mut cache);
    merge_cache_object(
        value
            .get("message")
            .and_then(|message| message.get("usage")),
        &mut cache,
    );

    (!cache.is_empty()).then_some(cache)
}

fn merge_usage_object(value: Option<&Value>, usage: &mut UsageDelta) {
    let Some(value) = value else {
        return;
    };

    usage.input_tokens = usage
        .input_tokens
        .saturating_add(u64_field(value, "input_tokens"));
    usage.output_tokens = usage
        .output_tokens
        .saturating_add(u64_field(value, "output_tokens"));
    usage.total_tokens = usage
        .total_tokens
        .saturating_add(u64_field(value, "total_tokens"));
}

fn merge_cache_object(value: Option<&Value>, cache: &mut CacheDelta) {
    let Some(value) = value else {
        return;
    };

    cache.cache_creation_input_tokens = cache
        .cache_creation_input_tokens
        .saturating_add(u64_field(value, "cache_creation_input_tokens"));
    cache.cache_read_input_tokens = cache
        .cache_read_input_tokens
        .saturating_add(u64_field(value, "cache_read_input_tokens"));
}

fn u64_field(value: &Value, key: &str) -> u64 {
    value.get(key).and_then(Value::as_u64).unwrap_or(0)
}
