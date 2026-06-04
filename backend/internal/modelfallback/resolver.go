package modelfallback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

const (
	defaultMaxDepth = 2
	wildcardModel   = "*"
)

type ErrorClass string

const (
	General               ErrorClass = "general"
	ContextWindowExceeded ErrorClass = "context_window_exceeded"
	ContentPolicy         ErrorClass = "content_policy"
)

type Config struct {
	Enabled       bool                `json:"enabled"`
	MaxDepth      int                 `json:"max_depth"`
	General       map[string][]string `json:"general"`
	ContextWindow map[string][]string `json:"context_window"`
	ContentPolicy map[string][]string `json:"content_policy"`
}

type Resolver struct {
	cfg Config
}

type SettingsReader interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

func ParseConfig(raw string) (Resolver, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Resolver{}, nil
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Resolver{}, err
	}
	normalizeConfig(&cfg)
	return Resolver{cfg: cfg}, nil
}

func FromSettings(ctx context.Context, settings SettingsReader) Resolver {
	if settings == nil {
		return Resolver{}
	}
	stored, err := settings.Get(ctx, platformsettings.KeyModelFallbackChains)
	if err != nil {
		return Resolver{}
	}
	resolver, err := ParseConfig(stored.Value)
	if err != nil {
		return Resolver{}
	}
	return resolver
}

func (r Resolver) Enabled() bool {
	return r.cfg.Enabled
}

func (r Resolver) MaxDepth() int {
	if !r.Enabled() {
		return 0
	}
	if r.cfg.MaxDepth > 0 {
		return r.cfg.MaxDepth
	}
	return defaultMaxDepth
}

func (r Resolver) Resolve(requestedModel string, class ErrorClass, alreadyTried []string) string {
	if !r.Enabled() {
		return ""
	}
	tried := make(map[string]struct{}, len(alreadyTried)+1)
	original := normalizeModel(requestedModel)
	if original != "" {
		tried[original] = struct{}{}
	}
	for _, model := range alreadyTried {
		if normalized := normalizeModel(model); normalized != "" {
			tried[normalized] = struct{}{}
		}
	}
	for _, candidate := range r.chainFor(requestedModel, class) {
		normalized := normalizeModel(candidate)
		if normalized == "" || normalized == original {
			continue
		}
		if _, seen := tried[normalized]; seen {
			continue
		}
		return candidate
	}
	return ""
}

func ClassForFailure(code string, endClass gateway.StreamEndClass, upstream gateway.ErrorClass, abortReason string) ErrorClass {
	code = strings.TrimSpace(code)
	abortReason = strings.TrimSpace(abortReason)
	switch {
	case code == "upstream_"+string(gateway.ErrorClassRequestTooLarge),
		upstream == gateway.ErrorClassRequestTooLarge,
		abortReason == "request_too_large":
		return ContextWindowExceeded
	case code == "upstream_"+string(gateway.ErrorClassPlatformPolicy),
		upstream == gateway.ErrorClassPlatformPolicy,
		abortReason == "upstream_forbidden":
		return ContentPolicy
	}
	switch code {
	case clienterr.CodeNoCapacity,
		clienterr.CodePoolSelectError,
		clienterr.CodeCredentialResolveError,
		clienterr.CodeQueueWait,
		clienterr.CodeUpstreamDispatchError,
		clienterr.CodeUpstreamEmptyResponse,
		clienterr.CodeStreamForwardError,
		clienterr.CodeAttemptFailed:
		return General
	}
	switch endClass {
	case gateway.UpstreamError5xx,
		gateway.UpstreamRateLimit,
		gateway.FirstTokenTimeout,
		gateway.InterEventTimeout,
		gateway.UpstreamAuthFailure:
		return General
	default:
		return General
	}
}

func DeriveLogicalRequestID(base, model string) string {
	base = strings.TrimSpace(base)
	model = strings.TrimSpace(model)
	sum := sha256.Sum256([]byte(model))
	return base + "#mf:" + hex.EncodeToString(sum[:8])
}

func (r Resolver) chainFor(requestedModel string, class ErrorClass) []string {
	requestedModel = normalizeModel(requestedModel)
	switch class {
	case ContextWindowExceeded:
		if chain := firstConfiguredChain(r.cfg.ContextWindow, requestedModel); len(chain) > 0 {
			return chain
		}
	case ContentPolicy:
		if chain := firstConfiguredChain(r.cfg.ContentPolicy, requestedModel); len(chain) > 0 {
			return chain
		}
	}
	return firstConfiguredChain(r.cfg.General, requestedModel)
}

func firstConfiguredChain(chains map[string][]string, model string) []string {
	if len(chains) == 0 {
		return nil
	}
	if chain := chains[model]; len(chain) > 0 {
		return chain
	}
	return chains[wildcardModel]
}

func normalizeConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = defaultMaxDepth
	}
	cfg.General = normalizeChains(cfg.General)
	cfg.ContextWindow = normalizeChains(cfg.ContextWindow)
	cfg.ContentPolicy = normalizeChains(cfg.ContentPolicy)
}

func normalizeChains(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for model, chain := range in {
		key := normalizeModel(model)
		if key == "" {
			continue
		}
		for _, candidate := range chain {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				out[key] = append(out[key], candidate)
			}
		}
	}
	return out
}

func normalizeModel(model string) string {
	return strings.TrimSpace(model)
}
