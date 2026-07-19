package projectenrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultTimeout          = 10 * time.Second
	StatusResolved          = "resolved"
	StatusOperatorAttention = "operator_attention"
	StatusMissing           = "missing"
	StatusConflict          = "conflict"
)

type Resolver interface {
	ResolveProjectID(context.Context, string) (string, error)
}

type MetadataResolver interface {
	ResolveProjectMetadata(context.Context, string) (string, string, error)
}

type Enricher interface {
	Enrich(context.Context, string, []byte) (Result, error)
}

type Result struct {
	Payload              []byte
	ProjectRef           string
	SubscriptionTierRaw  string
	SubscriptionVerified bool
	SubscriptionConflict bool
	Attempted            bool
}

type Service struct {
	resolver Resolver
	timeout  time.Duration
}

func New(resolver Resolver, timeout ...time.Duration) *Service {
	limit := DefaultTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		limit = timeout[0]
	}
	return &Service{resolver: resolver, timeout: limit}
}

func (s *Service) Enrich(ctx context.Context, vendor string, payload []byte) (Result, error) {
	result := Result{Payload: append([]byte(nil), payload...)}
	if !strings.EqualFold(strings.TrimSpace(vendor), "antigravity") {
		return result, nil
	}

	fields, err := decodePayload(payload)
	if err != nil {
		return result, err
	}
	existingProjectRef := stringField(fields, "project_id")
	existingTier := stringField(fields, "subscription_tier_raw")
	result.ProjectRef = existingProjectRef
	result.SubscriptionTierRaw = existingTier
	if s == nil || s.resolver == nil {
		if existingProjectRef != "" {
			return markSubscriptionAttention(fields, result, errors.New("projectenrich: resolver 未配置"))
		}
		return markAttention(fields, result, errors.New("projectenrich: resolver 未配置"))
	}
	_, hasMetadataResolver := s.resolver.(MetadataResolver)
	if existingProjectRef != "" && (existingTier != "" || !hasMetadataResolver) {
		return result, nil
	}
	result.Attempted = true

	accessToken := stringField(fields, "access_token")
	if accessToken == "" {
		if existingProjectRef != "" {
			return markSubscriptionAttention(fields, result, errors.New("projectenrich: access_token 缺失"))
		}
		return markAttention(fields, result, errors.New("projectenrich: access_token 缺失"))
	}

	limit := s.timeout
	if limit <= 0 {
		limit = DefaultTimeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, limit)
	projectRef, tier, err := s.resolveMetadata(resolveCtx, accessToken)
	cancel()
	projectRef = strings.TrimSpace(projectRef)
	tier = strings.TrimSpace(tier)
	if err != nil {
		if existingProjectRef != "" {
			return markSubscriptionAttention(fields, result, fmt.Errorf("projectenrich: 解析套餐层级失败: %w", err))
		}
		return markAttention(fields, result, fmt.Errorf("projectenrich: 解析 project 标识失败: %w", err))
	}
	if projectRef == "" {
		projectRef = existingProjectRef
	}
	if projectRef == "" {
		return markAttention(fields, result, errors.New("projectenrich: resolver 返回空 project 标识"))
	}
	if existingProjectRef != "" && projectRef != existingProjectRef {
		fields["project_metadata_status"] = mustJSON(StatusConflict)
		fields["subscription_metadata_status"] = mustJSON(StatusConflict)
		fields["observed_project_id"] = mustJSON(projectRef)
		payload, marshalErr := json.Marshal(fields)
		cause := fmt.Errorf("projectenrich: 已有 project 标识 %q 与上游识别结果 %q 冲突", existingProjectRef, projectRef)
		if marshalErr != nil {
			return Result{}, errors.Join(cause, fmt.Errorf("projectenrich: 编码冲突状态失败: %w", marshalErr))
		}
		result.Payload = payload
		result.SubscriptionConflict = true
		return result, cause
	}

	fields["project_id"] = mustJSON(projectRef)
	fields["project_metadata_status"] = mustJSON(StatusResolved)
	if tier != "" {
		fields["subscription_tier_raw"] = mustJSON(tier)
		fields["subscription_metadata_status"] = mustJSON(StatusResolved)
		result.SubscriptionTierRaw = tier
		result.SubscriptionVerified = true
	} else {
		fields["subscription_metadata_status"] = mustJSON(StatusMissing)
	}
	result.Payload, err = json.Marshal(fields)
	if err != nil {
		return Result{}, fmt.Errorf("projectenrich: 编码补齐后的凭据失败: %w", err)
	}
	result.ProjectRef = projectRef
	return result, nil
}

func (s *Service) resolveMetadata(ctx context.Context, token string) (string, string, error) {
	if resolver, ok := s.resolver.(MetadataResolver); ok {
		return resolver.ResolveProjectMetadata(ctx, token)
	}
	projectRef, err := s.resolver.ResolveProjectID(ctx, token)
	return projectRef, "", err
}

func markAttention(fields map[string]json.RawMessage, result Result, cause error) (Result, error) {
	fields["project_metadata_status"] = mustJSON(StatusOperatorAttention)
	payload, err := json.Marshal(fields)
	if err != nil {
		return Result{}, errors.Join(cause, fmt.Errorf("projectenrich: 编码待处理状态失败: %w", err))
	}
	result.Payload = payload
	return result, cause
}

func markSubscriptionAttention(fields map[string]json.RawMessage, result Result, cause error) (Result, error) {
	fields["subscription_metadata_status"] = mustJSON(StatusOperatorAttention)
	payload, err := json.Marshal(fields)
	if err != nil {
		return Result{}, errors.Join(cause, fmt.Errorf("projectenrich: 编码套餐待处理状态失败: %w", err))
	}
	result.Payload = payload
	return result, cause
}

func decodePayload(payload []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("projectenrich: 凭据载荷不是 JSON 对象: %w", err)
	}
	if fields == nil {
		return nil, errors.New("projectenrich: 凭据载荷必须是 JSON 对象")
	}
	return fields, nil
}

func stringField(fields map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func mustJSON(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
