package moderation

import (
	"bytes"
	"context"
	"errors"
	"expvar"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestScreener_KeywordMatchRejectsRequest(t *testing.T) {
	audit := &auditSpy{}
	ban := &banCounterSpy{}
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			Enabled: true, FailClosed: true, SampleRatePct: 100,
			BanThreshold: 3, BanWindowSeconds: 3600,
		}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{
			ID:         17,
			Keyword:    "forbidden",
			ReasonCode: "policy_keyword",
		}}},
		Hashes: hashStoreStub{},
		Audit:  audit,
		Ban:    ban,
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-keyword",
		PayloadHash: "hash-keyword",
		Body:        []byte(`{"messages":[{"content":"contains a forbidden phrase"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockKeyword {
		t.Fatalf("decision=%q want %q", res.Decision, DecisionBlockKeyword)
	}
	if res.MatchedKeywordID == nil || *res.MatchedKeywordID != 17 {
		t.Fatalf("matched keyword id=%v want 17", res.MatchedKeywordID)
	}
	if len(audit.events) != 1 || audit.events[0].PayloadHash != "hash-keyword" {
		t.Fatalf("audit metadata mismatch: %+v", audit.events)
	}
	if audit.events[0].ReasonCode == "forbidden" {
		t.Fatalf("audit reason leaked raw keyword: %+v", audit.events[0])
	}
	if ban.calls != 1 {
		t.Fatalf("auto-ban calls=%d want 1 for keyword block", ban.calls)
	}
	if ban.events[0].TenantID != 7 || ban.events[0].APIKeyID != 11 || ban.events[0].UserID != 13 {
		t.Fatalf("auto-ban identity mismatch: %+v", ban.events[0])
	}
	if ban.cfgs[0].BanThreshold != 3 || ban.cfgs[0].BanWindowSeconds != 3600 {
		t.Fatalf("auto-ban config mismatch: %+v", ban.cfgs[0])
	}
}

func TestScreener_HashPrecheckRejectsKnownHash(t *testing.T) {
	hashID := int64(91)
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 17, Keyword: "forbidden"}}},
		Hashes: hashStoreStub{match: HashMatch{
			Matched:    true,
			ID:         hashID,
			ReasonCode: "known_blocked_hash",
		}},
		Audit: &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-hit",
		Body:        []byte("forbidden also appears"),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockHash {
		t.Fatalf("decision=%q want %q", res.Decision, DecisionBlockHash)
	}
	if res.MatchedHashID == nil || *res.MatchedHashID != hashID {
		t.Fatalf("matched hash id=%v want %d", res.MatchedHashID, hashID)
	}
}

func TestScreener_AllowsCleanRequest(t *testing.T) {
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 17, Keyword: "forbidden"}}},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-clean",
		Body:        []byte(`{"messages":[{"content":"ordinary request"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass {
		t.Fatalf("decision=%q want %q", res.Decision, DecisionPass)
	}
}

func TestScreener_ExternalBlocksOverThreshold(t *testing.T) {
	// Mutation: changing threshold comparison from >= to >, or ignoring
	// category thresholds and trusting only flagged, leaves this exact-boundary
	// violation unblocked and makes the decision/audit assertions red.
	audit := &auditSpy{}
	ban := &banCounterSpy{}
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "Bearer screen-key" {
				t.Fatalf("authorization header=%q want Bearer screen-key", got)
			}
			body := `{"results":[{"flagged":true,"categories":{"violence":true},"category_scores":{"violence":0.73}}]}`
			return moderationHTTPResponse(http.StatusOK, body), nil
		})},
	})
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			Enabled: true, FailClosed: true, SampleRatePct: 100,
			BanThreshold: 3, BanWindowSeconds: 3600,
			External: ExternalModerationConfig{
				Enabled:    true,
				BaseURL:    "https://moderation.example.test/v1/moderations",
				APIKeys:    []string{"screen-key"},
				Model:      "omni-moderation-latest",
				Thresholds: map[string]float64{"violence": 0.73},
			},
		}},
		Keywords: &keywordStoreStub{},
		Hashes:   hashStoreStub{},
		Audit:    audit,
		Ban:      ban,
		External: provider,
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-external-block",
		PayloadHash: "hash-external-block",
		Body:        []byte(`{"messages":[{"content":"external threshold fixture"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockExternal {
		t.Fatalf("decision=%q want %q", res.Decision, DecisionBlockExternal)
	}
	if res.ReasonCode != "external_moderation:violence" {
		t.Fatalf("reason=%q want external_moderation:violence", res.ReasonCode)
	}
	if len(audit.events) != 1 || audit.events[0].Decision != DecisionBlockExternal ||
		audit.events[0].ReasonCode != "external_moderation:violence" {
		t.Fatalf("external block audit mismatch: %+v", audit.events)
	}
	if ban.calls != 1 || ban.events[0].Decision != DecisionBlockExternal {
		t.Fatalf("external block must feed auto-ban: calls=%d events=%+v", ban.calls, ban.events)
	}
}

func TestScreener_ExternalDisabledNoCall(t *testing.T) {
	// Mutation: calling the external screener when External.Enabled=false
	// increments the spy and turns this default-off regression red.
	external := &externalModeratorStub{err: errors.New("must not call external")}
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			Enabled: true, FailClosed: true,
			External: ExternalModerationConfig{
				Enabled: false,
				BaseURL: "https://moderation.example.test/v1/moderations",
				APIKeys: []string{"disabled-key"},
			},
		}},
		Keywords: &keywordStoreStub{},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
		External: external,
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID: 7, APIKeyID: 11, UserID: 13,
		PayloadHash: "hash-external-disabled",
		Body:        []byte(`{"messages":[{"content":"ordinary request"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass {
		t.Fatalf("decision=%q want pass", res.Decision)
	}
	if external.calls != 0 {
		t.Fatalf("external calls=%d want 0 while external disabled", external.calls)
	}
}

func TestScreener_ExternalFailOpenOnErrorAudits(t *testing.T) {
	// Mutation: treating external provider outage as fail-closed, or returning
	// pass without an audit event, makes the decision/audit assertions red.
	audit := &auditSpy{}
	ban := &banCounterSpy{}
	external := &externalModeratorStub{err: errors.New("upstream moderation down")}
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			Enabled: true, FailClosed: true, SampleRatePct: 100,
			BanThreshold: 3, BanWindowSeconds: 3600,
			External: ExternalModerationConfig{
				Enabled: true,
				BaseURL: "https://moderation.example.test/v1/moderations",
				APIKeys: []string{"fail-open-key"},
			},
		}},
		Keywords: &keywordStoreStub{},
		Hashes:   hashStoreStub{},
		Audit:    audit,
		Ban:      ban,
		External: external,
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-external-error",
		PayloadHash: "hash-external-error",
		Body:        []byte(`{"messages":[{"content":"ordinary request"}]}`),
	})
	if err != nil {
		t.Fatalf("external fail-open Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass || res.ReasonCode != "external_moderation_error" {
		t.Fatalf("result=%+v want pass external_moderation_error", res)
	}
	if len(audit.events) != 1 || audit.events[0].Decision != DecisionPass ||
		audit.events[0].ReasonCode != "external_moderation_error" {
		t.Fatalf("fail-open audit mismatch: %+v", audit.events)
	}
	if ban.calls != 0 {
		t.Fatalf("fail-open pass must not feed auto-ban: calls=%d", ban.calls)
	}
}

func TestScreener_KeywordBoundaryDoesNotMatchInsideWord(t *testing.T) {
	// Mutation: restoring substring matching makes "ass" match "passage",
	// turning this clean request into a false positive block.
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 21, Keyword: "ass"}}},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-passage",
		Body:        []byte(`{"messages":[{"content":"read this passage carefully"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass {
		t.Fatalf("decision=%q want pass; keyword must respect token boundary", res.Decision)
	}
}

func TestScreener_KeywordMatchNoBoundaryScriptsBySubstring(t *testing.T) {
	// 根因:中文/日文等无空格脚本会被 moderationTokens 合成一个长 token。
	// Mutation:把 fallback 还原成纯 token 序列匹配时，"违规" 不会命中 "这是违规内容"。
	cases := []struct {
		name    string
		keyword string
		body    string
	}{
		{name: "chinese", keyword: "违规", body: `{"messages":[{"content":"这是违规内容"}]}`},
		{name: "japanese", keyword: "禁止", body: `{"messages":[{"content":"これは禁止内容です"}]}`},
		{name: "mixed_cjk_english", keyword: "违规GPT", body: `{"messages":[{"content":"这是违规GPT内容"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScreener(ScreenerDeps{
				Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
				Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 23, Keyword: tc.keyword}}},
				Hashes:   hashStoreStub{},
				Audit:    &auditSpy{},
			})

			res, err := s.Screen(context.Background(), ScreenRequest{
				TenantID:    7,
				APIKeyID:    11,
				UserID:      13,
				PayloadHash: "hash-no-boundary-" + tc.name,
				Body:        []byte(tc.body),
			})
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}
			if res.Decision != DecisionBlockKeyword {
				t.Fatalf("decision=%q want keyword block for %q inside %q", res.Decision, tc.keyword, tc.body)
			}
		})
	}
}

func TestScreener_KeywordMatchNormalizesNFKCAndZeroWidth(t *testing.T) {
	// Mutation: deleting NFKC normalization or zero-width stripping leaves this
	// visually obvious "attack" unblocked.
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 22, Keyword: "attack"}}},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-normalized",
		Body:        []byte("{\"messages\":[{\"content\":\"\uff41t\u200bt\uff41ck\"}]}"),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockKeyword {
		t.Fatalf("decision=%q want keyword block after normalization", res.Decision)
	}
}

func TestScreener_AuditAndAutoBanFailuresEmitWarnAndMetric(t *testing.T) {
	// 根因:写审计和 auto-ban 失败被 `_ =` 吞掉，运维既看不到 WARN 也看不到失败 metric。
	// Mutation:删掉 reportModerationFailure 调用时，block 决策仍返回，但日志/metric 断言会变红。
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	auditBefore := moderationFailureMetricValue("audit_write_failed")
	banBefore := moderationFailureMetricValue("auto_ban_record_failed")
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			Enabled: true, FailClosed: true, SampleRatePct: 100,
			BanThreshold: 3, BanWindowSeconds: 3600,
		}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 24, Keyword: "forbidden"}}},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{err: errors.New("audit sink down")},
		Ban:      &banCounterSpy{err: errors.New("ban store down")},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-observe-failures",
		PayloadHash: "hash-observe-failures",
		Body:        []byte(`{"messages":[{"content":"forbidden"}]}`),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockKeyword {
		t.Fatalf("decision=%q want keyword block despite observer failures", res.Decision)
	}
	if got := moderationFailureMetricValue("audit_write_failed") - auditBefore; got != 1 {
		t.Fatalf("audit failure metric delta=%d want 1", got)
	}
	if got := moderationFailureMetricValue("auto_ban_record_failed") - banBefore; got != 1 {
		t.Fatalf("auto-ban failure metric delta=%d want 1", got)
	}
	logged := logs.String()
	if !strings.Contains(logged, "moderation_audit_write_failed") ||
		!strings.Contains(logged, "moderation_auto_ban_record_failed") {
		t.Fatalf("missing WARN logs for audit/ban failures: %s", logged)
	}
}

func TestScreener_HashTakesPriorityOverKeyword(t *testing.T) {
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 17, Keyword: "forbidden"}}},
		Hashes:   hashStoreStub{match: HashMatch{Matched: true, ID: 91}},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-hit",
		Body:        []byte("forbidden"),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionBlockHash {
		t.Fatalf("decision=%q want hash priority", res.Decision)
	}
}

func TestScreener_KeywordStoreErrorFailsClosed(t *testing.T) {
	backendErr := errors.New("db gone")
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: true}},
		Keywords: &keywordStoreStub{err: backendErr},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-error",
		Body:        []byte("ordinary"),
	})
	if !errors.Is(err, ErrScreenerBackend) {
		t.Fatalf("error=%v want ErrScreenerBackend", err)
	}
	if res.Decision != DecisionBlockBackend {
		t.Fatalf("decision=%q want fail-closed backend block", res.Decision)
	}
}

func TestScreener_KeywordStoreErrorFailsOpenWhenConfigured(t *testing.T) {
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: true, FailClosed: false}},
		Keywords: &keywordStoreStub{err: errors.New("db gone")},
		Hashes:   hashStoreStub{},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-open",
		Body:        []byte("ordinary"),
	})
	if err != nil {
		t.Fatalf("fail-open Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass {
		t.Fatalf("decision=%q want fail-open pass", res.Decision)
	}
}

func TestScreener_DisabledConfigSkipsStores(t *testing.T) {
	keywords := &keywordStoreStub{err: errors.New("must not call")}
	s := NewScreener(ScreenerDeps{
		Config:   configStub{cfg: ModerationConfig{Enabled: false, FailClosed: true}},
		Keywords: keywords,
		Hashes:   hashStoreStub{err: errors.New("must not call")},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-disabled",
		Body:        []byte("forbidden"),
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if res.Decision != DecisionPass {
		t.Fatalf("decision=%q want %q", res.Decision, DecisionPass)
	}
	if keywords.calls != 0 {
		t.Fatalf("keyword store was called while disabled: calls=%d", keywords.calls)
	}
}

type configStub struct {
	cfg ModerationConfig
	err error
}

func (s configStub) GetConfig(context.Context, int64) (ModerationConfig, error) {
	if s.err != nil {
		return ModerationConfig{}, s.err
	}
	return s.cfg, nil
}

type keywordStoreStub struct {
	rules []KeywordRule
	err   error
	calls int
}

func (s *keywordStoreStub) ListEnabled(context.Context, int64) ([]KeywordRule, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rules, nil
}

type hashStoreStub struct {
	match HashMatch
	err   error
}

func (s hashStoreStub) Contains(context.Context, int64, string) (HashMatch, error) {
	if s.err != nil {
		return HashMatch{}, s.err
	}
	return s.match, nil
}

type auditSpy struct {
	events []ModerationEvent
	err    error
}

func (s *auditSpy) Log(_ context.Context, event ModerationEvent, _ ModerationConfig) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

type banCounterSpy struct {
	calls  int
	events []ModerationEvent
	cfgs   []ModerationConfig
	err    error
}

func (s *banCounterSpy) RecordAndCheck(_ context.Context, event ModerationEvent, cfg ModerationConfig) (BanResult, error) {
	s.calls++
	s.events = append(s.events, event)
	s.cfgs = append(s.cfgs, cfg)
	if s.err != nil {
		return BanResult{}, s.err
	}
	return BanResult{Count: int64(s.calls)}, nil
}

type externalModeratorStub struct {
	calls  int
	reqs   []ScreenRequest
	cfgs   []ExternalModerationConfig
	result ExternalModerationResult
	err    error
}

func (s *externalModeratorStub) ScreenExternal(_ context.Context, req ScreenRequest, cfg ExternalModerationConfig) (ExternalModerationResult, error) {
	s.calls++
	s.reqs = append(s.reqs, req)
	s.cfgs = append(s.cfgs, cfg)
	if s.err != nil {
		return ExternalModerationResult{}, s.err
	}
	return s.result, nil
}

type moderationRoundTripFunc func(*http.Request) (*http.Response, error)

func (f moderationRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func moderationHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func moderationFailureMetricValue(key string) int64 {
	m, ok := expvar.Get("huakai_moderation_failure_total").(*expvar.Map)
	if !ok || m == nil {
		return 0
	}
	v, ok := m.Get(key).(*expvar.Int)
	if !ok || v == nil {
		return 0
	}
	return v.Value()
}
