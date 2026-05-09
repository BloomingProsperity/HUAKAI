use serde_json::Value;

use crate::stream_pipeline::{
    StreamEvent, UsageDelta,
    sse::{SseItem, SseScanner},
};

#[derive(Debug)]
pub struct OpenAiStreamParser {
    scanner: SseScanner,
}

impl OpenAiStreamParser {
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
                    let data = trim_ascii(&frame.data);
                    if data == b"[DONE]" {
                        events.push(StreamEvent::Done);
                        continue;
                    }

                    if data.is_empty() {
                        continue;
                    }

                    events.push(StreamEvent::Data(frame.data.clone()));
                    match extract_usage_from_json_bytes(data) {
                        Ok(Some(usage)) => events.push(StreamEvent::Usage(usage)),
                        Ok(None) => {}
                        Err(err) => events.push(StreamEvent::ProtocolError(format!(
                            "openai SSE JSON parse failed: {err}"
                        ))),
                    }
                }
                SseItem::ProtocolError(message) => events.push(StreamEvent::ProtocolError(message)),
            }
        }

        events
    }
}

pub fn extract_usage_from_json_bytes(data: &[u8]) -> Result<Option<UsageDelta>, serde_json::Error> {
    let value = serde_json::from_slice::<Value>(data)?;
    Ok(openai_usage(&value))
}

fn openai_usage(value: &Value) -> Option<UsageDelta> {
    let usage_value = value.get("usage")?;
    let input_tokens = u64_field(usage_value, "prompt_tokens")
        .saturating_add(u64_field(usage_value, "input_tokens"));
    let output_tokens = u64_field(usage_value, "completion_tokens")
        .saturating_add(u64_field(usage_value, "output_tokens"));
    let total_tokens =
        u64_field(usage_value, "total_tokens").max(input_tokens.saturating_add(output_tokens));

    let usage = UsageDelta {
        input_tokens,
        output_tokens,
        total_tokens,
    };

    (!usage.is_empty()).then_some(usage)
}

fn u64_field(value: &Value, key: &str) -> u64 {
    value.get(key).and_then(Value::as_u64).unwrap_or(0)
}

fn trim_ascii(bytes: &[u8]) -> &[u8] {
    let Some(start) = bytes.iter().position(|b| !b.is_ascii_whitespace()) else {
        return b"";
    };
    let end = bytes
        .iter()
        .rposition(|b| !b.is_ascii_whitespace())
        .map(|idx| idx + 1)
        .unwrap_or(start);
    &bytes[start..end]
}
