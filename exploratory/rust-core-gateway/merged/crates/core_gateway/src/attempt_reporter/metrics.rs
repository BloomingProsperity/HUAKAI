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
