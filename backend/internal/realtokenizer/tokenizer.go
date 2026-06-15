// Package realtokenizer sharpens input-token *estimates* with a real BPE
// tokenizer (tiktoken) for OpenAI-family models, falling back to the shared
// CJK-aware heuristic in internal/tokencheck for other vendors (Anthropic /
// Gemini / unknown). It only feeds pre-request estimates — the predicted cost and
// the quota reservation headroom — never the authoritative charge, which is always
// the provider-reported usage. BILL-086/TOK-008.
package realtokenizer

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/tiktoken-go/tokenizer"
)

// EnabledEnv toggles the real-tokenizer estimate. Default on; set to false to fall
// back to the legacy byte-count estimate.
const EnabledEnv = "HUAKAI_REAL_TOKENIZER_ENABLED"

var (
	enabledOnce sync.Once
	enabledVal  bool

	// codecCache memoizes the per-model codec resolution (including the negative
	// "no codec" result) so ForModel/Get is not paid on every hot-path request.
	codecCache sync.Map // model string -> codecEntry
)

type codecEntry struct {
	codec tokenizer.Codec
	ok    bool
}

// Enabled reports whether the real-tokenizer estimate is active. Default on; an
// unparseable value stays on (fails toward the more accurate default).
func Enabled() bool {
	enabledOnce.Do(func() { enabledVal = parseEnabled(os.Getenv(EnabledEnv)) })
	return enabledVal
}

// parseEnabled is the pure flag policy: empty defaults on, an explicit false
// disables, and an unparseable value stays on (fails toward the better default).
func parseEnabled(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	v, err := strconv.ParseBool(raw)
	return err != nil || v
}

// InputTokens returns a sharpened input-token estimate for the raw request body.
// For OpenAI-family models the plain-text JSON leaves are counted with tiktoken;
// for other vendors, unknown models, or on any tokenizer error it uses the shared
// CJK heuristic. Either way base64/binary blobs are capped by tokencheck so a
// multimodal request is not estimated by its base64 volume.
func InputTokens(model string, body []byte) int {
	if counter, ok := textCounter(model); ok {
		return tokencheck.EstimateRequestInputTokensWith(body, counter)
	}
	return tokencheck.EstimateRequestInputTokens(body)
}

// textCounter resolves a cached tiktoken counter for the model, or (nil,false)
// when no encoder applies. The returned counter is fail-soft: a tokenizer error
// counts as zero for that leaf rather than corrupting the whole estimate.
func textCounter(model string) (func(string) int, bool) {
	codec, ok := codecForModel(model)
	if !ok {
		return nil, false
	}
	return func(s string) int {
		if s == "" {
			return 0
		}
		n, err := codec.Count(s)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}, true
}

func codecForModel(model string) (tokenizer.Codec, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, false
	}
	if cached, ok := codecCache.Load(model); ok {
		entry := cached.(codecEntry)
		return entry.codec, entry.ok
	}
	codec, err := tokenizer.ForModel(tokenizer.Model(model))
	entry := codecEntry{codec: codec, ok: err == nil && codec != nil}
	codecCache.Store(model, entry)
	return entry.codec, entry.ok
}
