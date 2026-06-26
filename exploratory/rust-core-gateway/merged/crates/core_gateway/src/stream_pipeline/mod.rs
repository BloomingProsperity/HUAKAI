//! SSE stream pipeline。
//!
//! 解析器只观察 upstream bytes, 不改变透传给客户端的 body。

pub mod anthropic;
pub mod openai;
pub mod sse;

use bytes::Bytes;

use self::{
    anthropic::AnthropicStreamParser, openai::OpenAiStreamParser, sse::DEFAULT_MAX_FRAME_BYTES,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StreamProtocol {
    Anthropic,
    OpenAi,
}

impl StreamProtocol {
    pub fn from_vendor(vendor: &str) -> Option<Self> {
        if vendor.eq_ignore_ascii_case("anthropic") {
            Some(Self::Anthropic)
        } else if vendor.eq_ignore_ascii_case("openai") {
            Some(Self::OpenAi)
        } else {
            None
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct UsageDelta {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub total_tokens: u64,
}

impl UsageDelta {
    pub fn is_empty(&self) -> bool {
        self.input_tokens == 0 && self.output_tokens == 0 && self.total_tokens == 0
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct CacheDelta {
    pub cache_creation_input_tokens: u64,
    pub cache_read_input_tokens: u64,
}

impl CacheDelta {
    pub fn is_empty(&self) -> bool {
        self.cache_creation_input_tokens == 0 && self.cache_read_input_tokens == 0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum StreamEvent {
    Data(Bytes),
    Usage(UsageDelta),
    CacheMetric(CacheDelta),
    Done,
    ProtocolError(String),
    UpstreamError(String),
}

pub struct StreamPipeline {
    inner: PipelineInner,
}

enum PipelineInner {
    Anthropic(AnthropicStreamParser),
    OpenAi(OpenAiStreamParser),
}

impl StreamPipeline {
    pub fn new(protocol: StreamProtocol, max_frame_bytes: usize) -> Self {
        let max_frame_bytes = if max_frame_bytes == 0 {
            DEFAULT_MAX_FRAME_BYTES
        } else {
            max_frame_bytes
        };
        let inner = match protocol {
            StreamProtocol::Anthropic => {
                PipelineInner::Anthropic(AnthropicStreamParser::new(max_frame_bytes))
            }
            StreamProtocol::OpenAi => {
                PipelineInner::OpenAi(OpenAiStreamParser::new(max_frame_bytes))
            }
        };

        Self { inner }
    }

    pub fn push_bytes(&mut self, chunk: &[u8]) -> Vec<StreamEvent> {
        match &mut self.inner {
            PipelineInner::Anthropic(parser) => parser.push_bytes(chunk),
            PipelineInner::OpenAi(parser) => parser.push_bytes(chunk),
        }
    }

    pub fn finish(&mut self) -> Vec<StreamEvent> {
        match &mut self.inner {
            PipelineInner::Anthropic(parser) => parser.finish(),
            PipelineInner::OpenAi(parser) => parser.finish(),
        }
    }
}
