package projectenrich

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type resolverStub struct {
	projectRef string
	err        error
	calls      int
	token      string
	wait       bool
}

type metadataResolverStub struct {
	resolverStub
	tier string
}

type profileMetadataResolverStub struct {
	resolverStub
	profile string
	project string
	tier    string
}

func (s *profileMetadataResolverStub) ResolveProjectMetadataForProfile(_ context.Context, profile, token string) (string, string, error) {
	s.calls++
	s.profile = profile
	s.token = token
	return s.projectRef, s.tier, s.err
}

func (s *profileMetadataResolverStub) ResolveProjectMetadataForProfileAndProject(_ context.Context, profile, token, project string) (string, string, error) {
	s.calls++
	s.profile = profile
	s.token = token
	s.project = project
	return s.projectRef, s.tier, s.err
}

func (s *metadataResolverStub) ResolveProjectMetadata(ctx context.Context, token string) (string, string, error) {
	s.calls++
	s.token = token
	if s.wait {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	return s.projectRef, s.tier, s.err
}

func (s *resolverStub) ResolveProjectID(ctx context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	if s.wait {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return s.projectRef, s.err
}

func TestServiceEnrichesMissingProject(t *testing.T) {
	resolver := &resolverStub{projectRef: "project-resolved"}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{
		"access_token":"access-secret",
		"refresh_token":"refresh-secret"
	}`))
	if err != nil {
		t.Fatalf("Enrich 失败：%v", err)
	}
	if !result.Attempted || result.ProjectRef != "project-resolved" || resolver.calls != 1 || resolver.token != "access-secret" {
		t.Fatalf("补齐结果不符：result=%+v resolver=%+v", result, resolver)
	}
	var fields map[string]string
	if err := json.Unmarshal(result.Payload, &fields); err != nil {
		t.Fatalf("解析补齐载荷失败：%v", err)
	}
	if fields["project_id"] != "project-resolved" || fields["project_metadata_status"] != StatusResolved {
		t.Fatalf("project 字段未补齐：%s", result.Payload)
	}
}

func TestServiceEnrichesProjectAndSubscriptionInOneRequest(t *testing.T) {
	resolver := &metadataResolverStub{
		resolverStub: resolverStub{projectRef: "project-resolved"},
		tier:         "g1-pro-tier",
	}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{
		"access_token":"access-secret",
		"refresh_token":"refresh-secret"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Attempted || result.ProjectRef != "project-resolved" || result.SubscriptionTierRaw != "g1-pro-tier" ||
		!result.SubscriptionVerified || result.SubscriptionConflict || resolver.calls != 1 {
		t.Fatalf("账号元数据补齐结果错误：result=%+v resolver=%+v", result, resolver)
	}
	var fields map[string]string
	if err := json.Unmarshal(result.Payload, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["subscription_tier_raw"] != "g1-pro-tier" || fields["subscription_metadata_status"] != StatusResolved {
		t.Fatalf("套餐字段未写回凭据：%s", result.Payload)
	}
}

func TestServiceUsesGeminiCodeAssistProfile(t *testing.T) {
	resolver := &profileMetadataResolverStub{
		resolverStub: resolverStub{projectRef: "gemini-project"}, tier: "free-tier",
	}
	result, err := New(resolver).Enrich(context.Background(), ProfileGeminiCodeAssist,
		[]byte(`{"access_token":"access-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resolver.profile != ProfileGeminiCodeAssist || resolver.token != "access-secret" ||
		result.ProjectRef != "gemini-project" || result.SubscriptionTierRaw != "free-tier" {
		t.Fatalf("resolver=%+v result=%+v", resolver, result)
	}
}

func TestServicePassesExistingProjectToProfileValidation(t *testing.T) {
	resolver := &profileMetadataResolverStub{
		resolverStub: resolverStub{projectRef: "operator-project"}, tier: "standard-tier",
	}
	result, err := New(resolver).Enrich(context.Background(), ProfileGeminiCodeAssist,
		[]byte(`{"access_token":"access-secret","project_id":"operator-project"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resolver.project != "operator-project" || result.ProjectRef != "operator-project" ||
		result.SubscriptionTierRaw != "standard-tier" || !result.SubscriptionVerified {
		t.Fatalf("resolver=%+v result=%+v", resolver, result)
	}
}

func TestServiceWithExistingProjectFetchesOnlyMissingSubscription(t *testing.T) {
	resolver := &metadataResolverStub{
		resolverStub: resolverStub{projectRef: "project-existing"},
		tier:         "g1-ultra-tier",
	}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{
		"access_token":"access-secret",
		"project_id":"project-existing"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectRef != "project-existing" || result.SubscriptionTierRaw != "g1-ultra-tier" || !result.SubscriptionVerified || resolver.calls != 1 {
		t.Fatalf("已有 project 时的套餐补齐错误：result=%+v resolver=%+v", result, resolver)
	}
}

func TestServiceRejectsConflictingProjectMetadata(t *testing.T) {
	resolver := &metadataResolverStub{
		resolverStub: resolverStub{projectRef: "project-observed"},
		tier:         "g1-ultra-tier",
	}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{
		"access_token":"access-secret",
		"project_id":"project-existing"
	}`))
	if err == nil {
		t.Fatal("上游 project 与已有值冲突时必须显式报错")
	}
	if !errors.Is(err, ErrProjectMetadataConflict) {
		t.Fatalf("冲突错误类型不符：%v", err)
	}
	if result.ProjectRef != "project-existing" || result.SubscriptionTierRaw != "" || !result.SubscriptionConflict || result.SubscriptionVerified {
		t.Fatalf("冲突时必须保留已有账号且不得串入套餐：%+v", result)
	}
	var fields map[string]string
	if decodeErr := json.Unmarshal(result.Payload, &fields); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if fields["project_id"] != "project-existing" ||
		fields["observed_project_id"] != "project-observed" ||
		fields["project_metadata_status"] != StatusConflict ||
		fields["subscription_metadata_status"] != StatusConflict ||
		fields["subscription_tier_raw"] != "" {
		t.Fatalf("冲突状态没有完整保留：%s", result.Payload)
	}
}

func TestServiceFailureMarksOperatorAttention(t *testing.T) {
	resolver := &resolverStub{err: errors.New("上游暂时不可用")}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{"access_token":"access-secret"}`))
	if err == nil {
		t.Fatal("resolver 失败时必须返回错误供调用方记录")
	}
	if !errors.Is(err, ErrProjectMetadataUnavailable) {
		t.Fatalf("缺少 project 时错误类型不符：%v", err)
	}
	var fields map[string]string
	if decodeErr := json.Unmarshal(result.Payload, &fields); decodeErr != nil {
		t.Fatalf("解析待处理载荷失败：%v", decodeErr)
	}
	if fields["project_id"] != "" || fields["project_metadata_status"] != StatusOperatorAttention {
		t.Fatalf("失败状态不符：%s", result.Payload)
	}
}

func TestServiceExistingProjectDefersSubscriptionFailure(t *testing.T) {
	resolver := &metadataResolverStub{resolverStub: resolverStub{err: errors.New("上游暂时不可用")}}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{
		"access_token":"access-secret",
		"project_id":"project-existing"
	}`))
	if !errors.Is(err, ErrSubscriptionMetadataDeferred) {
		t.Fatalf("已有 project 时应仅延后套餐更新：%v", err)
	}
	var fields map[string]string
	if decodeErr := json.Unmarshal(result.Payload, &fields); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if fields["project_id"] != "project-existing" || fields["subscription_metadata_status"] != StatusOperatorAttention {
		t.Fatalf("已有项目事实未保留：%s", result.Payload)
	}
}

func TestServicePreservesExistingProjectWithoutNetwork(t *testing.T) {
	resolver := &resolverStub{projectRef: "unexpected"}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{"access_token":"access-secret","project_id":"project-existing"}`))
	if err != nil {
		t.Fatalf("Enrich 失败：%v", err)
	}
	if result.Attempted || result.ProjectRef != "project-existing" || resolver.calls != 0 {
		t.Fatalf("已有 project 不应再次请求：result=%+v calls=%d", result, resolver.calls)
	}
}

func TestServiceTotalTimeoutBoundsResolver(t *testing.T) {
	resolver := &resolverStub{wait: true}
	started := time.Now()
	result, err := New(resolver, 15*time.Millisecond).Enrich(context.Background(), "antigravity", []byte(`{"access_token":"access-secret"}`))
	if err == nil {
		t.Fatal("超时必须返回错误")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("总超时未生效，耗时 %s", elapsed)
	}
	var fields map[string]string
	if decodeErr := json.Unmarshal(result.Payload, &fields); decodeErr != nil {
		t.Fatalf("解析超时载荷失败：%v", decodeErr)
	}
	if fields["project_metadata_status"] != StatusOperatorAttention {
		t.Fatalf("超时后未标人工处理：%s", result.Payload)
	}
}
