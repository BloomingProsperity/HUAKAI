package moderation

import (
	"context"
	"errors"
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
