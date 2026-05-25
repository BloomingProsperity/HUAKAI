use bytes::{Bytes, BytesMut};
use memchr::memchr;

pub const DEFAULT_MAX_FRAME_BYTES: usize = 64 * 1024;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SseFrame {
    pub event: Option<Bytes>,
    pub data: Bytes,
    pub data_line_count: usize,
}

impl SseFrame {
    pub fn event_name(&self) -> Option<&str> {
        self.event
            .as_ref()
            .and_then(|event| std::str::from_utf8(event).ok())
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SseItem {
    Frame(SseFrame),
    ProtocolError(String),
}

#[derive(Debug)]
pub struct SseScanner {
    buffer: BytesMut,
    frame: FrameBuilder,
    max_frame_bytes: usize,
}

impl SseScanner {
    pub fn new(max_frame_bytes: usize) -> Self {
        Self {
            buffer: BytesMut::new(),
            frame: FrameBuilder::default(),
            max_frame_bytes: if max_frame_bytes == 0 {
                DEFAULT_MAX_FRAME_BYTES
            } else {
                max_frame_bytes
            },
        }
    }

    pub fn push_bytes(&mut self, chunk: &[u8]) -> Vec<SseItem> {
        let mut items = Vec::new();
        if chunk.is_empty() {
            return items;
        }

        self.buffer.extend_from_slice(chunk);
        if self.buffer.len() > self.max_frame_bytes {
            self.reset_after_error(&mut items, "SSE partial line exceeds max frame bytes");
            return items;
        }

        while let Some(newline) = memchr(b'\n', &self.buffer) {
            let mut raw_line = self.buffer.split_to(newline + 1);
            raw_line.truncate(newline);
            if raw_line.ends_with(b"\r") {
                raw_line.truncate(raw_line.len().saturating_sub(1));
            }
            self.process_line(&raw_line, &mut items);
        }

        items
    }

    pub fn finish(&mut self) -> Vec<SseItem> {
        let mut items = Vec::new();
        if !self.buffer.is_empty() {
            let mut raw_line = self.buffer.split_to(self.buffer.len());
            if raw_line.ends_with(b"\r") {
                raw_line.truncate(raw_line.len().saturating_sub(1));
            }
            self.process_line(&raw_line, &mut items);
        }
        self.dispatch_frame(&mut items);
        items
    }

    fn process_line(&mut self, line: &[u8], items: &mut Vec<SseItem>) {
        if line.is_empty() {
            self.dispatch_frame(items);
            return;
        }

        if line.starts_with(b":") {
            return;
        }

        let (field, value) = split_field(line);
        if let Err(message) = self.frame.push_field(field, value, self.max_frame_bytes) {
            self.reset_after_error(items, &message);
            return;
        }

        if self.frame.bytes_seen > self.max_frame_bytes {
            self.reset_after_error(items, "SSE frame exceeds max frame bytes");
        }
    }

    fn dispatch_frame(&mut self, items: &mut Vec<SseItem>) {
        if let Some(frame) = self.frame.take() {
            items.push(SseItem::Frame(frame));
        }
    }

    fn reset_after_error(&mut self, items: &mut Vec<SseItem>, message: &str) {
        self.buffer.clear();
        self.frame.clear();
        items.push(SseItem::ProtocolError(message.to_owned()));
    }
}

#[derive(Debug, Default)]
struct FrameBuilder {
    event: Option<BytesMut>,
    data: BytesMut,
    data_line_count: usize,
    bytes_seen: usize,
    has_field: bool,
}

impl FrameBuilder {
    fn push_field(
        &mut self,
        field: &[u8],
        value: &[u8],
        max_frame_bytes: usize,
    ) -> Result<(), String> {
        self.has_field = true;
        self.bytes_seen = self
            .bytes_seen
            .saturating_add(field.len())
            .saturating_add(value.len())
            .saturating_add(2);
        if self.bytes_seen > max_frame_bytes {
            return Err("SSE frame exceeds max frame bytes".to_owned());
        }

        match field {
            b"event" => {
                self.event = Some(BytesMut::from(value));
            }
            b"data" => {
                if self.data_line_count > 0 {
                    self.data.extend_from_slice(b"\n");
                }
                self.data.extend_from_slice(value);
                self.data_line_count += 1;
            }
            _ => {}
        }

        Ok(())
    }

    fn take(&mut self) -> Option<SseFrame> {
        if !self.has_field {
            return None;
        }

        let event = self.event.take().map(BytesMut::freeze);
        let data = std::mem::take(&mut self.data).freeze();
        let data_line_count = self.data_line_count;
        self.clear();

        Some(SseFrame {
            event,
            data,
            data_line_count,
        })
    }

    fn clear(&mut self) {
        self.event = None;
        self.data.clear();
        self.data_line_count = 0;
        self.bytes_seen = 0;
        self.has_field = false;
    }
}

fn split_field(line: &[u8]) -> (&[u8], &[u8]) {
    match memchr(b':', line) {
        Some(colon) => {
            let field = &line[..colon];
            let value = if line.get(colon + 1) == Some(&b' ') {
                &line[colon + 2..]
            } else {
                &line[colon + 1..]
            };
            (field, value)
        }
        None => (line, b""),
    }
}
