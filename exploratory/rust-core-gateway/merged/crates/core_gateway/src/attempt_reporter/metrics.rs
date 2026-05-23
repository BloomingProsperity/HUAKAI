use crate::stream_pipeline::{CacheDelta, UsageDelta};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AttemptTokenMetrics {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub total_tokens: u64,
    pub source: String,
}

impl AttemptTokenMetrics {
    pub(super) fn missing() -> Self {
        Self {
            source: "missing".to_owned(),
            ..Self::default()
        }
    }

    pub(super) fn add_usage_delta(&mut self, delta: &UsageDelta) {
        self.input_tokens = self.input_tokens.saturating_add(delta.input_tokens);
        self.output_tokens = self.output_tokens.saturating_add(delta.output_tokens);
        self.total_tokens = self.total_tokens.saturating_add(delta.total_tokens);
        if self.source.is_empty() || self.source == "missing" {
            self.source = "stream_pipeline".to_owned();
        }
    }

    /// W12-B D-5: 非流式 2xx body 解析出真实 usage → source="response_body" 标识。
    pub fn from_response_body(delta: &UsageDelta) -> Self {
        Self {
            input_tokens: delta.input_tokens,
            output_tokens: delta.output_tokens,
            total_tokens: delta.total_tokens,
            source: "response_body".to_owned(),
        }
    }

    /// W12-B D-5: 非流式 2xx body 解析失败 / usage 字段缺失 → source="pending_reconciliation"
    /// 区别于 "missing" (从未检查) 与 "response_body" (有真实值)。
    pub fn pending_reconciliation() -> Self {
        Self {
            source: "pending_reconciliation".to_owned(),
            ..Self::default()
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AttemptCacheMetrics {
    pub cache_read_tokens: u64,
    pub cache_write_tokens: u64,
    pub cache_hit: bool,
    pub source: String,
}

impl AttemptCacheMetrics {
    pub(super) fn missing() -> Self {
        Self {
            source: "missing".to_owned(),
            ..Self::default()
        }
    }

    pub(super) fn add_cache_delta(&mut self, delta: &CacheDelta) {
        self.cache_read_tokens = self
            .cache_read_tokens
            .saturating_add(delta.cache_read_input_tokens);
        self.cache_write_tokens = self
            .cache_write_tokens
            .saturating_add(delta.cache_creation_input_tokens);
        self.cache_hit |= delta.cache_read_input_tokens > 0;
        if self.source.is_empty() || self.source == "missing" {
            self.source = "stream_pipeline".to_owned();
        }
    }
}
