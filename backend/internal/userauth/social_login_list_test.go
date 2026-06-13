package userauth

import (
	"context"
	"testing"
	"time"
)

// listLinksStubStore 仅实现 ListSocialIdentityLinks(其余 Store 方法由嵌入的 nil Store 占位,
// list 路径不会调用它们),并记录收到的 tenant/user,用以验证 service 严格透传 session 身份。
type listLinksStubStore struct {
	Store

	gotTenantID int64
	gotUserID   int64
	calls       int
	out         []SocialIdentityLink
	err         error
}

func (s *listLinksStubStore) ListSocialIdentityLinks(_ context.Context, tenantID, userID int64) ([]SocialIdentityLink, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	if s.err != nil {
		return nil, s.err
	}
	return s.out, nil
}

// service 必须把传入的 tenant/user 原样转给 store(不改写、不从别处取),并对返回的 subject 脱敏。
// discriminating fixture: store 返回未脱敏的长 subject,断言出参已被 maskSocialSubject 改写、
// 且 store 收到的就是入参 tenant=7/user=42。
// MUTATION: service 跳过脱敏(直接返回 store 结果)→ subject 仍是原文 → 红;
// service 把别的 id 传给 store → gotUserID!=42 → 红。
func TestServiceListSocialIdentityLinksMasksAndScopes(t *testing.T) {
	linkedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store := &listLinksStubStore{out: []SocialIdentityLink{
		{Provider: SocialProviderGoogle, Subject: "1234567890", LinkedAt: linkedAt},
		{Provider: SocialProviderGitHub, Subject: "ab", LinkedAt: linkedAt},
	}}
	svc := NewService(store)

	got, err := svc.ListSocialIdentityLinks(context.Background(), 7, 42)
	if err != nil {
		t.Fatalf("ListSocialIdentityLinks: %v", err)
	}
	if store.calls != 1 || store.gotTenantID != 7 || store.gotUserID != 42 {
		t.Fatalf("store scope mismatch: calls=%d tenant=%d user=%d want 1/7/42", store.calls, store.gotTenantID, store.gotUserID)
	}
	if len(got) != 2 {
		t.Fatalf("links len=%d want 2", len(got))
	}
	if got[0].Subject == "1234567890" {
		t.Fatalf("subject not masked: %q; MUTATION: skipping maskSocialSubject leaves raw subject", got[0].Subject)
	}
	if got[0].Subject != "12******90" {
		t.Fatalf("subject mask=%q want 12******90", got[0].Subject)
	}
	if got[1].Subject != "**" {
		t.Fatalf("short subject mask=%q want ** (full redaction for <=2 chars)", got[1].Subject)
	}
	if got[0].Provider != SocialProviderGoogle || got[0].LinkedAt != linkedAt {
		t.Fatalf("provider/linked_at altered: %+v", got[0])
	}
}

// 入参非法(tenant/user <= 0)必须早返 ErrInvalidInput 且 store 不被调(不越权、不空查)。
func TestServiceListSocialIdentityLinksRejectsInvalidInput(t *testing.T) {
	store := &listLinksStubStore{}
	svc := NewService(store)
	if _, err := svc.ListSocialIdentityLinks(context.Background(), 0, 42); err != ErrInvalidInput {
		t.Fatalf("err=%v want ErrInvalidInput", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0 on invalid input", store.calls)
	}
}

// maskSocialSubject 边界:验证脱敏函数本身在各长度档位输出符合预期(不泄露完整 subject)。
func TestMaskSocialSubjectBoundaries(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"a":          "*",
		"ab":         "**",
		"abc":        "a**",
		"abcdef":     "a*****",
		"abcdefg":    "ab***fg",
		"1234567890": "12******90",
	}
	for in, want := range cases {
		if got := maskSocialSubject(in); got != want {
			t.Fatalf("maskSocialSubject(%q)=%q want %q", in, got, want)
		}
	}
}
