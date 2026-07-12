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

func TestServiceFailureMarksOperatorAttention(t *testing.T) {
	resolver := &resolverStub{err: errors.New("上游暂时不可用")}
	result, err := New(resolver).Enrich(context.Background(), "antigravity", []byte(`{"access_token":"access-secret"}`))
	if err == nil {
		t.Fatal("resolver 失败时必须返回错误供调用方记录")
	}
	var fields map[string]string
	if decodeErr := json.Unmarshal(result.Payload, &fields); decodeErr != nil {
		t.Fatalf("解析待处理载荷失败：%v", decodeErr)
	}
	if fields["project_id"] != "" || fields["project_metadata_status"] != StatusOperatorAttention {
		t.Fatalf("失败状态不符：%s", result.Payload)
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
