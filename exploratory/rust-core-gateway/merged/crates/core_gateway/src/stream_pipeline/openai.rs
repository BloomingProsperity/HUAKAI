use memchr::memmem;
use serde::Deserialize;

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
    if memmem::find(data, b"usage").is_none() {
        return Ok(None);
    }

    let envelope = serde_json::from_slice::<OpenAiUsageEnvelope>(data)?;
    Ok(envelope.usage.and_then(OpenAiUsageFields::into_delta))
}

#[derive(Debug, Deserialize)]
struct OpenAiUsageEnvelope {
    #[serde(default)]
    usage: Option<OpenAiUsageFields>,
}

#[derive(Debug, Deserialize)]
struct OpenAiUsageFields {
    #[serde(default)]
    prompt_tokens: u64,
    #[serde(default)]
    input_tokens: u64,
    #[serde(default)]
    completion_tokens: u64,
    #[serde(default)]
    output_tokens: u64,
    #[serde(default)]
    total_tokens: u64,
}

impl OpenAiUsageFields {
    fn into_delta(self) -> Option<UsageDelta> {
        let input_tokens = self.prompt_tokens.saturating_add(self.input_tokens);
        let output_tokens = self.completion_tokens.saturating_add(self.output_tokens);
        let total_tokens = self
            .total_tokens
            .max(input_tokens.saturating_add(output_tokens));

        let usage = UsageDelta {
            input_tokens,
            output_tokens,
            total_tokens,
        };

        (!usage.is_empty()).then_some(usage)
    }
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
