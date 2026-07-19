package credentialstore

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func TestMergeSubscriptionStatePreservesLastKnownPlanOnMissingEvidence(t *testing.T) {
	changedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	observedAt := changedAt.Add(time.Hour)
	current := subscriptionStateRow{
		Observation: subscriptionprofile.FromRaw(
			subscriptionprofile.VendorOpenAI, "Plus",
			subscriptionprofile.SourceIDTokenClaim,
			subscriptionprofile.TrustUnverifiedJWT,
			subscriptionprofile.VerificationUnverified,
			"user-1", "",
		),
		ChangedAt: changedAt,
	}

	next, gotChangedAt, replaceObservedAt, replaceSource := mergeSubscriptionState(
		current,
		subscriptionprofile.Missing(subscriptionprofile.VendorOpenAI, subscriptionprofile.SourceCredentialRefresh),
		observedAt,
	)
	if next.Plan != "plus" || next.Status != subscriptionprofile.StatusStale || next.Source != subscriptionprofile.SourceIDTokenClaim {
		t.Fatalf("缺少新证据时不得抹掉最后已知套餐：%+v", next)
	}
	if !gotChangedAt.Equal(observedAt) || replaceObservedAt || replaceSource {
		t.Fatalf("缺少证据应标记状态变更，但不得伪造套餐观测时间：changed=%s replace_observed=%v replace_source=%v", gotChangedAt, replaceObservedAt, replaceSource)
	}
}

func TestMergeSubscriptionStateRejectsWeakerConflictingEvidence(t *testing.T) {
	changedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	current := subscriptionStateRow{
		Observation: subscriptionprofile.FromRaw(
			subscriptionprofile.VendorOpenAI, "Pro",
			subscriptionprofile.SourceOAuthResponse,
			subscriptionprofile.TrustIssuerResponse,
			subscriptionprofile.VerificationIssuerResponse,
			"user-1", "",
		),
		ChangedAt: changedAt,
	}
	incoming := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "Free",
		subscriptionprofile.SourceImportPayload,
		subscriptionprofile.TrustImported,
		subscriptionprofile.VerificationUnverified,
		"user-1", "",
	)

	next, gotChangedAt, replaceObservedAt, replaceSource := mergeSubscriptionState(current, incoming, changedAt.Add(time.Hour))
	if next.Plan != "pro" || next.Status != subscriptionprofile.StatusConflict || next.ErrorClass != "weaker_subscription_evidence_conflict" {
		t.Fatalf("弱证据不得覆盖强证据：%+v", next)
	}
	if !gotChangedAt.Equal(changedAt.Add(time.Hour)) || replaceObservedAt || replaceSource {
		t.Fatalf("冲突应记录状态变更时间，但不得替换有效观测：changed=%s replace_observed=%v replace_source=%v", gotChangedAt, replaceObservedAt, replaceSource)
	}
}

func TestMergeSubscriptionStateNeverDowngradesTrustOnSamePlan(t *testing.T) {
	changedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	current := subscriptionStateRow{
		Observation: subscriptionprofile.FromRaw(
			subscriptionprofile.VendorOpenAI, "Pro",
			subscriptionprofile.SourceOAuthResponse,
			subscriptionprofile.TrustIssuerResponse,
			subscriptionprofile.VerificationIssuerResponse,
			"user-1", "",
		),
		ChangedAt: changedAt,
	}
	weakSame := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "Pro",
		subscriptionprofile.SourceImportPayload,
		subscriptionprofile.TrustImported,
		subscriptionprofile.VerificationUnverified,
		"user-1", "",
	)
	next, gotChangedAt, replaceObservedAt, replaceSource := mergeSubscriptionState(current, weakSame, changedAt.Add(time.Hour))
	if next.Trust != subscriptionprofile.TrustIssuerResponse || next.Source != subscriptionprofile.SourceOAuthResponse ||
		next.Status != subscriptionprofile.StatusObserved {
		t.Fatalf("同值弱证据降低了当前投影信任等级：%+v", next)
	}
	if !gotChangedAt.Equal(changedAt) || replaceObservedAt || replaceSource {
		t.Fatalf("同值弱证据不应替换投影：changed=%s replace_observed=%v replace_source=%v", gotChangedAt, replaceObservedAt, replaceSource)
	}
}

func TestMergeSubscriptionStatePreservesKnownPlanOnMetadataConflict(t *testing.T) {
	changedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	conflictedAt := changedAt.Add(time.Hour)
	current := subscriptionStateRow{
		Observation: subscriptionprofile.FromRaw(
			subscriptionprofile.VendorAntigravity, "pro",
			subscriptionprofile.SourceProviderAPI,
			subscriptionprofile.TrustVerifiedAPI,
			subscriptionprofile.VerificationVerified,
			"user-1", "",
		),
		ChangedAt: changedAt,
	}
	incoming := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorAntigravity, "",
		subscriptionprofile.SourceProviderAPI,
		subscriptionprofile.TrustVerifiedAPI,
		subscriptionprofile.VerificationVerified,
		"user-1", "",
	)
	incoming.Status = subscriptionprofile.StatusConflict
	incoming.ErrorClass = "subscription_metadata_conflict"

	next, gotChangedAt, replaceObservedAt, replaceSource := mergeSubscriptionState(current, incoming, conflictedAt)
	if next.Plan != "pro" || next.Status != subscriptionprofile.StatusConflict ||
		next.ErrorClass != "subscription_metadata_conflict" {
		t.Fatalf("元数据冲突不得抹掉最后已知套餐：%+v", next)
	}
	if !gotChangedAt.Equal(conflictedAt) || replaceObservedAt || replaceSource {
		t.Fatalf("冲突必须只改变状态时间：changed=%s replace_observed=%v replace_source=%v", gotChangedAt, replaceObservedAt, replaceSource)
	}
}

func TestFreshSubscriptionRefreshObservationRequiresChangedEvidence(t *testing.T) {
	sameID := subscriptionTestJWT(t, "Plus")
	if _, ok := freshSubscriptionRefreshObservation(
		subscriptionprofile.VendorOpenAI, "codex_cli_oauth",
		[]byte(`{"access_token":"old-access","id_token":"same-id","chatgpt_plan_type":"Plus"}`),
		[]byte(`{"access_token":"new-access","id_token":"same-id","chatgpt_plan_type":"Plus"}`),
	); ok {
		t.Fatal("只有 access_token 变化时不得把旧 id_token 套餐冒充为新证据")
	}
	newID := subscriptionTestJWT(t, "Pro")
	observation, ok := freshSubscriptionRefreshObservation(
		subscriptionprofile.VendorOpenAI, "codex_cli_oauth",
		[]byte(`{"id_token":"`+sameID+`","chatgpt_plan_type":"Plus"}`),
		[]byte(`{"id_token":"`+newID+`","chatgpt_plan_type":"Plus"}`),
	)
	if !ok || observation.Plan != "pro" || observation.Source != subscriptionprofile.SourceIDTokenClaim {
		t.Fatalf("新 id_token 套餐观测错误：ok=%v observation=%+v", ok, observation)
	}
	observation, ok = freshSubscriptionRefreshObservation(
		subscriptionprofile.VendorOpenAI, "codex_cli_oauth",
		[]byte(`{"id_token":"`+sameID+`","chatgpt_plan_type":"Plus"}`),
		[]byte(`{"id_token":"`+sameID+`","chatgpt_plan_type":"Pro"}`),
	)
	if !ok || observation.Plan != "pro" || observation.Source != subscriptionprofile.SourceCredentialRefresh ||
		observation.Trust != subscriptionprofile.TrustIssuerResponse {
		t.Fatalf("刷新响应的明确套餐必须胜过旧 id_token：ok=%v observation=%+v", ok, observation)
	}
	if _, ok := freshSubscriptionRefreshObservation(
		subscriptionprofile.VendorOpenAI, "codex_cli_oauth", nil, []byte(`{"access_token":"new"}`),
	); ok {
		t.Fatal("缺套餐字段的刷新不得追加伪观测")
	}
}

func TestCredentialMetadataOmitsEmptySubscription(t *testing.T) {
	raw, err := json.Marshal(CredentialMetadata{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if jsonContainsKey(t, raw, "subscription") {
		t.Fatalf("没有套餐投影时不得返回全空对象：%s", raw)
	}
	observation := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "plus",
		subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.TrustIssuerResponse,
		subscriptionprofile.VerificationIssuerResponse,
		"", "",
	)
	raw, err = json.Marshal(CredentialMetadata{ID: 1, Subscription: &observation})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsKey(t, raw, "subscription") {
		t.Fatalf("有套餐投影时响应丢失 subscription：%s", raw)
	}
}

func jsonContainsKey(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	_, ok := fields[key]
	return ok
}

func subscriptionTestJWT(t *testing.T, plan string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": plan},
	})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
