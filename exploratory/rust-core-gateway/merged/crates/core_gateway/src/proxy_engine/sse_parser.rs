//! HUAKAI SSE (Server-Sent Events) parser.
//!
//! 按 WHATWG/W3C EventSource 协议解析 `text/event-stream` 响应。该实现只依赖
//! 协议文本与 HUAKAI 本地需求，不读取第三方 SSE parser 源码。

use bytes::{Buf, Bytes, BytesMut};

/// 一个已完成的 SSE 帧。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SseFrame {
    /// `event:` 字段；缺省或空值按协议视为 `message`。
    pub event_name: String,
    /// 多个 `data:` 字段用 `\n` 合并。
    pub data: String,
    /// 当前帧携带的 `id:` 字段；不会继承上一帧的 id。
    pub id: Option<String>,
    /// 当前帧携带的合法 `retry:` 毫秒数。
    pub retry_ms: Option<u32>,
}

/// HUAKAI SSE parser.
///
/// 输入为任意切分的 `Bytes` chunk，例如 hyper body chunk。`feed` 会累积跨 chunk
/// 边界的半行，只有遇到空行完成帧时才返回 `SseFrame`。
#[derive(Debug)]
pub struct SseParser {
    buffer: BytesMut,
    state: ParseState,
    start_checked: bool,
}

#[derive(Debug)]
enum ParseState {
    BetweenFrames,
    InFrame(FrameFields),
}

#[derive(Debug, Default)]
struct FrameFields {
    event_name: Option<String>,
    data_lines: Vec<String>,
    id: Option<String>,
    retry_ms: Option<u32>,
}

impl Default for SseParser {
    fn default() -> Self {
        Self::new()
    }
}

impl SseParser {
    pub fn new() -> Self {
        Self {
            buffer: BytesMut::new(),
            state: ParseState::BetweenFrames,
            start_checked: false,
        }
    }

    /// 推入一个 body chunk，返回本次 chunk 完成的全部 SSE 帧。
    pub fn feed(&mut self, chunk: Bytes) -> Vec<SseFrame> {
        if !chunk.is_empty() {
            self.buffer.extend_from_slice(&chunk);
        }

        if !self.strip_initial_bom_if_ready() {
            return Vec::new();
        }

        let mut frames = Vec::new();
        while let Some(line) = take_line(&mut self.buffer) {
            self.process_line(&line, &mut frames);
        }
        frames
    }

    /// 关闭输入时丢弃未被空行结束的半帧，保持 EventSource 语义。
    pub fn finish(&mut self) {
        self.buffer.clear();
        self.state = ParseState::BetweenFrames;
        self.start_checked = false;
    }

    fn strip_initial_bom_if_ready(&mut self) -> bool {
        if self.start_checked {
            return true;
        }

        if self.buffer.is_empty() {
            return false;
        }

        const BOM: &[u8] = b"\xEF\xBB\xBF";
        if self.buffer.len() < BOM.len() && BOM.starts_with(&self.buffer) {
            return false;
        }

        if self.buffer.starts_with(BOM) {
            self.buffer.advance(BOM.len());
        }
        self.start_checked = true;
        true
    }

    fn process_line(&mut self, line: &[u8], frames: &mut Vec<SseFrame>) {
        if line.is_empty() {
            self.dispatch_current_frame(frames);
            return;
        }

        if line.starts_with(b":") {
            return;
        }

        let (field, value) = split_field(line);
        let ParseState::InFrame(fields) = self.frame_fields() else {
            unreachable!("frame_fields always enters InFrame")
        };

        match field {
            b"event" => {
                fields.event_name = Some(decode_value(value));
            }
            b"data" => {
                fields.data_lines.push(decode_value(value));
            }
            b"id" if !value.contains(&b'\0') => {
                fields.id = Some(decode_value(value));
            }
            b"retry" => {
                if let Some(retry_ms) = parse_retry_ms(value) {
                    fields.retry_ms = Some(retry_ms);
                }
            }
            _ => {}
        }
    }

    fn frame_fields(&mut self) -> &mut ParseState {
        if matches!(self.state, ParseState::BetweenFrames) {
            self.state = ParseState::InFrame(FrameFields::default());
        }
        &mut self.state
    }

    fn dispatch_current_frame(&mut self, frames: &mut Vec<SseFrame>) {
        let state = std::mem::replace(&mut self.state, ParseState::BetweenFrames);
        let ParseState::InFrame(fields) = state else {
            return;
        };

        if let Some(frame) = fields.into_frame() {
            frames.push(frame);
        }
    }
}

impl FrameFields {
    fn into_frame(self) -> Option<SseFrame> {
        if self.data_lines.is_empty() {
            return None;
        }

        let event_name = match self.event_name {
            Some(name) if !name.is_empty() => name,
            _ => "message".to_owned(),
        };

        Some(SseFrame {
            event_name,
            data: self.data_lines.join("\n"),
            id: self.id,
            retry_ms: self.retry_ms,
        })
    }
}

fn take_line(buffer: &mut BytesMut) -> Option<BytesMut> {
    for index in 0..buffer.len() {
        match buffer[index] {
            b'\n' => {
                let mut line = buffer.split_to(index + 1);
                line.truncate(index);
                if line.ends_with(b"\r") {
                    line.truncate(line.len().saturating_sub(1));
                }
                return Some(line);
            }
            b'\r' => {
                if index + 1 == buffer.len() {
                    return None;
                }

                let consume = if buffer[index + 1] == b'\n' {
                    index + 2
                } else {
                    index + 1
                };
                let mut line = buffer.split_to(consume);
                line.truncate(index);
                return Some(line);
            }
            _ => {}
        }
    }

    None
}

fn split_field(line: &[u8]) -> (&[u8], &[u8]) {
    match line.iter().position(|byte| *byte == b':') {
        Some(colon) => {
            let field = &line[..colon];
            let value_start = if line.get(colon + 1) == Some(&b' ') {
                colon + 2
            } else {
                colon + 1
            };
            (field, &line[value_start..])
        }
        None => (line, b""),
    }
}

fn decode_value(value: &[u8]) -> String {
    String::from_utf8_lossy(value).into_owned()
}

fn parse_retry_ms(value: &[u8]) -> Option<u32> {
    if value.is_empty() || !value.iter().all(u8::is_ascii_digit) {
        return None;
    }
    std::str::from_utf8(value).ok()?.parse::<u32>().ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn feed_all(chunks: &[&str]) -> Vec<SseFrame> {
        let mut parser = SseParser::new();
        let mut frames = Vec::new();
        for chunk in chunks {
            frames.extend(parser.feed(Bytes::copy_from_slice(chunk.as_bytes())));
        }
        frames
    }

    #[test]
    fn parses_single_frame() {
        let frames = feed_all(&["data:hello\n\n"]);

        assert_eq!(
            frames,
            vec![SseFrame {
                event_name: "message".to_owned(),
                data: "hello".to_owned(),
                id: None,
                retry_ms: None,
            }]
        );
    }

    #[test]
    fn parses_multiple_frames_from_one_chunk() {
        let frames = feed_all(&["data:hello\n\ndata:world\n\n"]);

        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].data, "hello");
        assert_eq!(frames[1].data, "world");
    }

    #[test]
    fn parses_frame_split_across_chunks() {
        let frames = feed_all(&["data:he", "llo\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "hello");
    }

    #[test]
    fn joins_multiline_data_with_newline() {
        let frames = feed_all(&["data:line1\ndata:line2\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "line1\nline2");
    }

    #[test]
    fn parses_custom_event_name() {
        let frames = feed_all(&["event:custom\ndata:hi\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].event_name, "custom");
        assert_eq!(frames[0].data, "hi");
    }

    #[test]
    fn skips_comment_lines() {
        let frames = feed_all(&[":keepalive\ndata:hi\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "hi");
    }

    #[test]
    fn parses_id_and_retry_fields() {
        let frames = feed_all(&["id: evt-7\nretry: 1500\ndata:ready\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].id.as_deref(), Some("evt-7"));
        assert_eq!(frames[0].retry_ms, Some(1500));
        assert_eq!(frames[0].data, "ready");
    }

    #[test]
    fn treats_crlf_and_lf_equally() {
        let frames = feed_all(&["event:custom\r\ndata:hi\r\n\r\ndata:there\n\n"]);

        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].event_name, "custom");
        assert_eq!(frames[0].data, "hi");
        assert_eq!(frames[1].data, "there");
    }

    #[test]
    fn skips_blank_frames_without_fields() {
        let frames = feed_all(&["\n\ndata:hi\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "hi");
    }

    #[test]
    fn data_without_value_still_dispatches_empty_data() {
        let frames = feed_all(&["data:\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "");
    }

    #[test]
    fn ignores_invalid_retry_and_id_with_null() {
        let frames = feed_all(&["id: bad\0id\nretry: 12ms\ndata:ok\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].id, None);
        assert_eq!(frames[0].retry_ms, None);
        assert_eq!(frames[0].data, "ok");
    }

    #[test]
    fn strips_initial_utf8_bom() {
        let frames = feed_all(&["\u{feff}data:hi\n\n"]);

        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "hi");
    }

    #[test]
    fn finish_discards_incomplete_frame() {
        let mut parser = SseParser::new();

        assert!(parser.feed(Bytes::from_static(b"data:hi\n")).is_empty());
        parser.finish();
        let frames = parser.feed(Bytes::from_static(b"data:there\n\n"));
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, "there");
    }
}
