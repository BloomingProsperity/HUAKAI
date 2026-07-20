package moderation

import (
	"context"
	"expvar"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

var moderationFailureMetrics = expvar.NewMap("huakai_moderation_failure_total")

type ScreenerDeps struct {
	Config         ConfigStore
	Keywords       KeywordStore
	Hashes         HashStore
	Audit          AuditLogger
	Ban            AutoBanCounter
	External       ExternalModerator
	ConfigCacheTTL time.Duration
	Now            func() time.Time
	// RandomIntn 只控制外部审核的 1..99% 抽样；nil 使用并发安全的 math/rand 全局源。
	RandomIntn func(int) int
}

type storeScreener struct {
	config     ConfigStore
	keywords   KeywordStore
	hashes     HashStore
	audit      AuditLogger
	ban        AutoBanCounter
	external   ExternalModerator
	randomIntn func(int) int
	configs    *ttlLRU[int64, ModerationConfig]
}

type AutoBanCounter interface {
	RecordAndCheck(context.Context, ModerationEvent, ModerationConfig) (BanResult, error)
}

func NewScreener(deps ScreenerDeps) Screener {
	configTTL := deps.ConfigCacheTTL
	if configTTL == 0 {
		configTTL = 30 * time.Second
	}
	var configs *ttlLRU[int64, ModerationConfig]
	if configTTL > 0 {
		configs = newTTLLRU[int64, ModerationConfig](CacheOptions{
			MaxEntries: 1024,
			TTL:        configTTL,
			Now:        deps.Now,
		})
	}
	randomIntn := deps.RandomIntn
	if randomIntn == nil {
		randomIntn = rand.Intn
	}
	return &storeScreener{
		config:     deps.Config,
		keywords:   deps.Keywords,
		hashes:     deps.Hashes,
		audit:      deps.Audit,
		ban:        deps.Ban,
		external:   deps.External,
		randomIntn: randomIntn,
		configs:    configs,
	}
}

func (s *storeScreener) Screen(ctx context.Context, req ScreenRequest) (ScreenResult, error) {
	cfg, err := s.loadConfig(ctx, req.TenantID)
	if !cfg.Enabled {
		result := ScreenResult{Decision: DecisionPass, ReasonCode: "moderation_disabled"}
		if err != nil {
			reportModerationFailure(ctx, "config_backend_error", req, result, err)
		}
		return result, nil
	}
	if err != nil {
		return s.backendResult(cfg, "config_backend_error", err)
	}
	hashResult, err := s.checkHash(ctx, req, cfg)
	if err != nil || hashResult.Decision != "" {
		return hashResult, err
	}
	keywordResult, err := s.checkKeywords(ctx, req, cfg)
	if err != nil || keywordResult.Decision != "" {
		return keywordResult, err
	}
	externalResult, err := s.checkExternal(ctx, req, cfg)
	if err != nil || externalResult.Decision != "" {
		return externalResult, err
	}
	result := ScreenResult{Decision: DecisionPass, ReasonCode: "clean"}
	// DM-16:重发轮的 clean 审计是纯噪音(同对话每轮一条);命中(block)的
	// 审计仍无条件写——取证不打折。
	if !repeatAgentTurn(req) {
		if err := s.writeAudit(ctx, req, result, cfg); err != nil {
			reportModerationFailure(ctx, "audit_write_failed", req, result, err)
		}
	}
	return result, nil
}

func (s *storeScreener) loadConfig(ctx context.Context, tenantID int64) (ModerationConfig, error) {
	if s.config == nil {
		return DefaultConfig(tenantID), nil
	}
	var staleCfg ModerationConfig
	hasStale := false
	if s.configs != nil {
		if cfg, fresh, stale := s.configs.GetAllowStale(tenantID); fresh {
			return cfg, nil
		} else if stale {
			staleCfg = cfg
			hasStale = true
		}
	}
	cfg, err := s.config.GetConfig(ctx, tenantID)
	if err != nil {
		if hasStale {
			return staleCfg, err
		}
		if cfg.TenantID == 0 {
			cfg = DefaultConfig(tenantID)
		}
		return cfg, err
	}
	if s.configs != nil {
		s.configs.Set(tenantID, cfg)
	}
	return cfg, nil
}

func (s *storeScreener) checkHash(ctx context.Context, req ScreenRequest, cfg ModerationConfig) (ScreenResult, error) {
	if s.hashes == nil || req.PayloadHash == "" {
		return ScreenResult{}, nil
	}
	match, err := s.hashes.Contains(ctx, req.TenantID, req.PayloadHash)
	if err != nil {
		return s.backendResult(cfg, "hash_backend_error", err)
	}
	if !match.Matched {
		return ScreenResult{}, nil
	}
	result := ScreenResult{
		Decision:      DecisionBlockHash,
		ReasonCode:    nonEmpty(match.ReasonCode, "hash_match"),
		MatchedHashID: &match.ID,
	}
	if err := s.writeAudit(ctx, req, result, cfg); err != nil {
		reportModerationFailure(ctx, "audit_write_failed", req, result, err)
	}
	if err := s.recordAutoBan(ctx, req, result, cfg); err != nil {
		reportModerationFailure(ctx, "auto_ban_record_failed", req, result, err)
	}
	return result, nil
}

func (s *storeScreener) checkKeywords(ctx context.Context, req ScreenRequest, cfg ModerationConfig) (ScreenResult, error) {
	if s.keywords == nil || len(req.Body) == 0 {
		return ScreenResult{}, nil
	}
	rules, err := s.keywords.ListEnabled(ctx, req.TenantID)
	if err != nil {
		return s.backendResult(cfg, "keyword_backend_error", err)
	}
	bodyText := string(req.Body)
	bodyTokens := moderationTokens(bodyText)
	for _, rule := range rules {
		if !containsKeyword(bodyText, bodyTokens, rule.Keyword) {
			continue
		}
		id := rule.ID
		result := ScreenResult{
			Decision:         DecisionBlockKeyword,
			ReasonCode:       nonEmpty(rule.ReasonCode, "keyword_match"),
			MatchedKeywordID: &id,
		}
		if err := s.writeAudit(ctx, req, result, cfg); err != nil {
			reportModerationFailure(ctx, "audit_write_failed", req, result, err)
		}
		if err := s.recordAutoBan(ctx, req, result, cfg); err != nil {
			reportModerationFailure(ctx, "auto_ban_record_failed", req, result, err)
		}
		return result, nil
	}
	return ScreenResult{}, nil
}

func (s *storeScreener) checkExternal(ctx context.Context, req ScreenRequest, cfg ModerationConfig) (ScreenResult, error) {
	if s.external == nil || !cfg.External.Enabled {
		return ScreenResult{}, nil
	}
	// 本地 hash/keyword 已在调用本函数前完成；只采样有成本的外部调用。
	// 0%=按外部审核未启用路径放行，100%=不取随机数、保持接线前逐请求全检。
	if !shouldSampleExternal(cfg.SampleRatePct, s.randomIntn) {
		return ScreenResult{}, nil
	}
	externalResult, err := s.external.ScreenExternal(ctx, req, cfg.External)
	if err != nil {
		result := ScreenResult{Decision: DecisionPass, ReasonCode: "external_moderation_error"}
		if auditErr := s.writeAudit(ctx, req, result, cfg); auditErr != nil {
			reportModerationFailure(ctx, "audit_write_failed", req, result, auditErr)
		}
		reportModerationFailure(ctx, "external_moderation_failed", req, result, err)
		return result, nil
	}
	if !externalResult.Blocked {
		return ScreenResult{}, nil
	}
	result := ScreenResult{
		Decision:   DecisionBlockExternal,
		ReasonCode: nonEmpty(externalResult.ReasonCode, "external_moderation"),
	}
	if err := s.writeAudit(ctx, req, result, cfg); err != nil {
		reportModerationFailure(ctx, "audit_write_failed", req, result, err)
	}
	if err := s.recordAutoBan(ctx, req, result, cfg); err != nil {
		reportModerationFailure(ctx, "auto_ban_record_failed", req, result, err)
	}
	return result, nil
}

func containsKeyword(body string, bodyTokens []string, keyword string) bool {
	if containsKeywordTokens(bodyTokens, keyword) {
		return true
	}
	normalizedKeyword := normalizeModerationText(keyword)
	if normalizedKeyword == "" || !containsNoBoundaryScript(normalizedKeyword) {
		return false
	}
	// 中文/日文等无空格脚本不能只靠 token 边界，否则 "这是违规内容" 会被折成单 token。
	// 仅当 keyword 自身含无词边界脚本时才回退 substring，保留英文关键词的整词匹配。
	return strings.Contains(normalizeModerationText(body), normalizedKeyword)
}

func containsKeywordTokens(bodyTokens []string, keyword string) bool {
	keywordTokens := moderationTokens(keyword)
	if len(keywordTokens) == 0 || len(keywordTokens) > len(bodyTokens) {
		return false
	}
	for i := 0; i <= len(bodyTokens)-len(keywordTokens); i++ {
		matched := true
		for j := range keywordTokens {
			if bodyTokens[i+j] != keywordTokens[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func containsNoBoundaryScript(value string) bool {
	for _, r := range value {
		if unicode.In(r,
			unicode.Han,
			unicode.Hiragana,
			unicode.Katakana,
			unicode.Hangul,
			unicode.Thai,
			unicode.Lao,
			unicode.Khmer,
			unicode.Myanmar,
		) {
			return true
		}
	}
	return false
}

func moderationTokens(value string) []string {
	value = normalizeModerationText(value)
	var tokens []string
	var token strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			token.WriteRune(r)
			continue
		}
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	if token.Len() > 0 {
		tokens = append(tokens, token.String())
	}
	return tokens
}

func normalizeModerationText(value string) string {
	// NFKC + 剥离零宽字符可拦住常见的全角/不可见字符绕过。
	// 这仍是 token 匹配,不是语义过滤；跨脚本形近字
	// 和语言相关的分词需要后续的分类器处理。
	return strings.Map(func(r rune) rune {
		if isZeroWidthRune(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFKC.String(value))
}

func isZeroWidthRune(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\ufeff', '\u2060':
		return true
	default:
		return false
	}
}

func (s *storeScreener) backendResult(cfg ModerationConfig, reason string, err error) (ScreenResult, error) {
	if !cfg.FailClosed {
		return ScreenResult{Decision: DecisionPass, ReasonCode: reason}, nil
	}
	return ScreenResult{Decision: DecisionBlockBackend, ReasonCode: reason},
		fmt.Errorf("%w: %s: %v", ErrScreenerBackend, reason, err)
}

func (s *storeScreener) writeAudit(ctx context.Context, req ScreenRequest, res ScreenResult, cfg ModerationConfig) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Log(ctx, eventFromResult(req, res), cfg)
}

// repeatAgentTurn 报告本次请求是否为 agent 工具循环的重发轮(尾消息非
// user)。空 TailRole(未知)按首轮处理——fail-open 到现行为(DM-16)。
func repeatAgentTurn(req ScreenRequest) bool {
	return req.TailRole != "" && !strings.EqualFold(req.TailRole, "user")
}

func (s *storeScreener) recordAutoBan(ctx context.Context, req ScreenRequest, res ScreenResult, cfg ModerationConfig) error {
	if s.ban == nil {
		return nil
	}
	// TailRole 来自客户端请求体，只能用于压缩干净请求的日志噪音，不能作为
	// 免计违规的授权信号；否则攻击者把尾角色写成 assistant 即可绕过封禁。
	_, err := s.ban.RecordAndCheck(ctx, eventFromResult(req, res), cfg)
	return err
}

func reportModerationFailure(ctx context.Context, kind string, req ScreenRequest, res ScreenResult, err error) {
	moderationFailureMetrics.Add(kind, 1)
	// 安全:WARN 只记录租户/key/request 元数据和错误类型，不写 raw body、payload_hash 或关键词。
	slog.WarnContext(ctx, "moderation_"+kind,
		slog.Int64("tenant_id", req.TenantID),
		slog.Int64("api_key_id", req.APIKeyID),
		slog.String("request_id", req.RequestID),
		slog.String("decision", string(res.Decision)),
		slog.String("reason_code", res.ReasonCode),
		slog.String("error_class", privacy.ErrorClassFor(ctx, err)),
	)
}

func eventFromResult(req ScreenRequest, res ScreenResult) ModerationEvent {
	return ModerationEvent{
		TenantID:         req.TenantID,
		APIKeyID:         req.APIKeyID,
		UserID:           req.UserID,
		RequestID:        req.RequestID,
		PayloadHash:      req.PayloadHash,
		Decision:         res.Decision,
		ReasonCode:       res.ReasonCode,
		MatchedKeywordID: res.MatchedKeywordID,
		MatchedHashID:    res.MatchedHashID,
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
