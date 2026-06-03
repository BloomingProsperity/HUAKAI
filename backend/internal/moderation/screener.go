package moderation

import (
	"context"
	"fmt"
	"strings"
)

type ScreenerDeps struct {
	Config   ConfigStore
	Keywords KeywordStore
	Hashes   HashStore
	Audit    AuditLogger
}

type storeScreener struct {
	config   ConfigStore
	keywords KeywordStore
	hashes   HashStore
	audit    AuditLogger
}

func NewScreener(deps ScreenerDeps) Screener {
	return &storeScreener{
		config:   deps.Config,
		keywords: deps.Keywords,
		hashes:   deps.Hashes,
		audit:    deps.Audit,
	}
}

func (s *storeScreener) Screen(ctx context.Context, req ScreenRequest) (ScreenResult, error) {
	cfg, err := s.loadConfig(ctx, req.TenantID)
	if err != nil {
		return s.backendResult(cfg, "config_backend_error", err)
	}
	if !cfg.Enabled {
		return ScreenResult{Decision: DecisionPass, ReasonCode: "moderation_disabled"}, nil
	}
	hashResult, err := s.checkHash(ctx, req, cfg)
	if err != nil || hashResult.Decision != "" {
		return hashResult, err
	}
	keywordResult, err := s.checkKeywords(ctx, req, cfg)
	if err != nil || keywordResult.Decision != "" {
		return keywordResult, err
	}
	result := ScreenResult{Decision: DecisionPass, ReasonCode: "clean"}
	_ = s.writeAudit(ctx, req, result, cfg)
	return result, nil
}

func (s *storeScreener) loadConfig(ctx context.Context, tenantID int64) (ModerationConfig, error) {
	if s.config == nil {
		return DefaultConfig(tenantID), nil
	}
	cfg, err := s.config.GetConfig(ctx, tenantID)
	if err != nil {
		return DefaultConfig(tenantID), err
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
	_ = s.writeAudit(ctx, req, result, cfg)
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
	body := strings.ToLower(string(req.Body))
	for _, rule := range rules {
		keyword := strings.TrimSpace(strings.ToLower(rule.Keyword))
		if keyword == "" || !strings.Contains(body, keyword) {
			continue
		}
		id := rule.ID
		result := ScreenResult{
			Decision:         DecisionBlockKeyword,
			ReasonCode:       nonEmpty(rule.ReasonCode, "keyword_match"),
			MatchedKeywordID: &id,
		}
		_ = s.writeAudit(ctx, req, result, cfg)
		return result, nil
	}
	return ScreenResult{}, nil
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
