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
	// 变异:把阈值比较从 >= 改成 >,或者忽略
	// 类别阈值而只信任 flagged,都会让这条恰好压在边界上的
	// 违规没被拦截,使 decision/audit 断言变红。
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
	// 变异:在 External.Enabled=false 时仍调用外部 screener,
	// 会让 spy 自增,使这条「默认关闭」的回归用例变红。
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
	// 变异:把外部 provider 故障当成 fail-closed,或者放行
	// 但不写审计事件,都会让 decision/audit 断言变红。
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

func TestScreenerExternalSamplingZeroSkipsAllCallsAndPasses(t *testing.T) {
	// 变异：删除采样守卫后，pct=0 的三次请求都会调用外部审核，calls 从 0 变 3。
	external := &externalModeratorStub{result: ExternalModerationResult{Blocked: true}}
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			TenantID: 7, Enabled: true, FailClosed: true, SampleRatePct: 0,
			External: ExternalModerationConfig{Enabled: true},
		}},
		Keywords: &keywordStoreStub{}, Hashes: hashStoreStub{}, External: external,
		RandomIntn: func(int) int { t.Fatal("pct=0 不应读取随机源"); return 0 },
	})
	for i := 0; i < 3; i++ {
		res, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "zero-sample"})
		if err != nil || res.Decision != DecisionPass {
			t.Fatalf("第 %d 次 result=%+v err=%v, want pass", i, res, err)
		}
	}
	if external.calls != 0 {
		t.Fatalf("external calls=%d, want 0", external.calls)
	}
}

func TestScreenerExternalSamplingHundredCallsEveryRequest(t *testing.T) {
	// 变异：把 100% 也送入随机分支会触发本测试的 panic 源；少调一次也会使 calls!=3。
	external := &externalModeratorStub{}
	s := NewScreener(ScreenerDeps{
		Config: configStub{cfg: ModerationConfig{
			TenantID: 7, Enabled: true, FailClosed: true, SampleRatePct: 100,
			External: ExternalModerationConfig{Enabled: true},
		}},
		Keywords: &keywordStoreStub{}, Hashes: hashStoreStub{}, External: external,
		RandomIntn: func(int) int { t.Fatal("pct=100 不应读取随机源"); return 0 },
	})
	for i := 0; i < 3; i++ {
		if _, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "full-sample"}); err != nil {
			t.Fatalf("第 %d 次 Screen: %v", i, err)
		}
	}
	if external.calls != 3 {
		t.Fatalf("external calls=%d, want 3", external.calls)
	}
}

func TestScreenerExternalSamplingIntermediateUsesInjectedRandomSource(t *testing.T) {
	// 变异：翻转 < 边界或忽略注入源后，draw=24/25 在 pct=25 下不会分别得到调用/跳过。
	for _, tc := range []struct {
		name      string
		draw      int
		wantCalls int
	}{
		{name: "边界内采样", draw: 24, wantCalls: 1},
		{name: "边界外跳过", draw: 25, wantCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			external := &externalModeratorStub{}
			s := NewScreener(ScreenerDeps{
				Config: configStub{cfg: ModerationConfig{
					TenantID: 7, Enabled: true, FailClosed: true, SampleRatePct: 25,
					External: ExternalModerationConfig{Enabled: true},
				}},
				Keywords: &keywordStoreStub{}, Hashes: hashStoreStub{}, External: external,
				RandomIntn: func(n int) int {
					if n != 100 {
						t.Fatalf("random upper bound=%d, want 100", n)
					}
					return tc.draw
				},
			})
			res, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: tc.name})
			if err != nil || res.Decision != DecisionPass {
				t.Fatalf("result=%+v err=%v, want pass", res, err)
			}
			if external.calls != tc.wantCalls {
				t.Fatalf("external calls=%d, want %d", external.calls, tc.wantCalls)
			}
		})
	}
}

func TestScreenerExternalSamplingNeverSkipsLocalKeywordOrHash(t *testing.T) {
	// 变异：把采样守卫错误地放到本地检查之前，会让 pct=0 的两条本地命中都被放行。
	for _, tc := range []struct {
		name     string
		keywords KeywordStore
		hashes   HashStore
		want     Decision
	}{
		{
			name: "keyword", keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 1, Keyword: "forbidden"}}},
			hashes: hashStoreStub{}, want: DecisionBlockKeyword,
		},
		{
			name: "hash", keywords: &keywordStoreStub{},
			hashes: hashStoreStub{match: HashMatch{Matched: true, ID: 2}}, want: DecisionBlockHash,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			external := &externalModeratorStub{}
			s := NewScreener(ScreenerDeps{
				Config: configStub{cfg: ModerationConfig{
					TenantID: 7, Enabled: true, FailClosed: true, SampleRatePct: 0,
					External: ExternalModerationConfig{Enabled: true},
				}},
				Keywords: tc.keywords, Hashes: tc.hashes, External: external,
			})
			res, err := s.Screen(context.Background(), ScreenRequest{
				TenantID: 7, PayloadHash: "known", Body: []byte("forbidden"),
			})
			if err != nil || res.Decision != tc.want {
				t.Fatalf("result=%+v err=%v, want %s", res, err, tc.want)
			}
			if external.calls != 0 {
				t.Fatalf("local block 后 external calls=%d, want 0", external.calls)
			}
		})
	}
}

func TestModerationDefaultSampleRateKeepsPreWiringFullInspection(t *testing.T) {
	// 防翻转守卫：默认值若从 100 漂移，未改配置的租户会开始跳过外部审核。
	if got := DefaultConfig(7).SampleRatePct; got != 100 {
		t.Fatalf("default SampleRatePct=%d, want pre-wiring 100", got)
	}
}

func TestScreener_KeywordBoundaryDoesNotMatchInsideWord(t *testing.T) {
	// 变异:恢复 substring 匹配会让 "ass" 命中 "passage",
	// 把这条干净请求误判成拦截。
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
	// 变异:删掉 NFKC 归一化或零宽字符剥离,会让这个
	// 肉眼一看就是 "attack" 的内容没被拦截。
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

// MUTATION: clean 审计忽略 TailRole(去掉 repeatAgentTurn 守卫)→ 重发轮 clean
// 审计断言红。DM-16 的重发去重只对纯噪音的 clean 审计生效;auto-ban 计数是安全
// 控制,命中拦截即无条件计(见 B4 [S2]:否则非 user 尾可绕过自动封禁)。
func TestScreener_RepeatAgentTurnSuppressesCleanAudit(t *testing.T) {
	mk := func() (Screener, *auditSpy, *banCounterSpy) {
		audit := &auditSpy{}
		ban := &banCounterSpy{}
		s := NewScreener(ScreenerDeps{
			Config: configStub{cfg: ModerationConfig{
				Enabled: true, FailClosed: true, SampleRatePct: 100,
				BanThreshold: 3, BanWindowSeconds: 3600,
			}},
			Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 17, Keyword: "forbidden", ReasonCode: "policy_keyword"}}},
			Hashes:   hashStoreStub{},
			Audit:    audit,
			Ban:      ban,
		})
		return s, audit, ban
	}
	blockedBody := []byte(`{"messages":[{"role":"user","content":"contains a forbidden phrase"},{"role":"assistant","content":"x"}]}`)

	// 重发轮(尾=assistant):拦截照旧,审计照写(取证),ban 照计(B4:安全控制
	// 不受客户端可控的 TailRole 降级,否则自动封禁可被非 user 尾完全绕过)。
	s, audit, ban := mk()
	res, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "r1", Body: blockedBody, TailRole: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionBlockKeyword {
		t.Fatalf("重发轮拦截判定不得放水: %q", res.Decision)
	}
	if len(audit.events) != 1 {
		t.Fatalf("block 审计必须写: %d", len(audit.events))
	}
	if ban.calls != 1 {
		t.Fatalf("非 user 尾的 block 仍须计 ban(防自动封禁绕过): calls=%d", ban.calls)
	}

	// 用户轮:ban 照计
	s, _, ban = mk()
	if _, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "r2", Body: blockedBody, TailRole: "user"}); err != nil {
		t.Fatal(err)
	}
	if ban.calls != 1 {
		t.Fatalf("用户轮应计 ban: calls=%d", ban.calls)
	}

	// 干净请求:重发轮不写 clean 审计;未知 TailRole 照写(现行为)
	s, audit, _ = mk()
	clean := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"}]}`)
	if _, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "r3", Body: clean, TailRole: "assistant"}); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 0 {
		t.Fatalf("重发轮 clean 审计应跳过: %d", len(audit.events))
	}
	s, audit, _ = mk()
	if _, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "r4", Body: clean}); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("未知 TailRole 应保持现行为写 clean 审计: %d", len(audit.events))
	}
}
