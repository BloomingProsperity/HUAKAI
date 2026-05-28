package trustreceipt

import (
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const validationStateProvisional = "provisional"

func BuildProvisionalFromEnv(env *proto.HCSF, result auditledger.AuditLedgerResult, requestID string, receiptSequence int) TrustReceiptV1 {
	if requestID == "" {
		requestID = requestIDFromEnv(env, result)
	}
	return TrustReceiptV1{
		RequestID:                 requestID,
		ReceiptSequence:           receiptSequence,
		TenantScopeRef:            tenantScopeRefFromEnv(env),
		OccurredAt:                occurredAtFromEnv(env),
		Provider:                  providerFromEnv(env, result),
		RequestedModel:            requestedModelFromEnv(env),
		RoutedModel:               routedModelFromEnv(env),
		UpstreamModel:             upstreamModelFromEnv(env, result),
		DeliveredModel:            deliveredModelFromEnv(env, result),
		CostCents:                 0,
		TokenCounts:               tokenCountsFromEnv(env),
		PriceSnapshot:             PriceSnapshot{},
		ValidationState:           validationStateProvisional,
		RedactedMetadataAllowlist: map[string]any{},
	}
}

func requestIDFromEnv(env *proto.HCSF, result auditledger.AuditLedgerResult) string {
	if env != nil && env.RequestMeta.RequestID != "" {
		return env.RequestMeta.RequestID
	}
	return result.RequestID
}

func tenantScopeRefFromEnv(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	return auditledger.TenantScopeRef(env.RequestMeta.TenantID)
}

func occurredAtFromEnv(env *proto.HCSF) time.Time {
	if env != nil {
		for _, hop := range env.Accounting.HopChain {
			if ts := parseReceiptTime(hop.StartedAt); !ts.IsZero() {
				return ts
			}
			if ts := parseReceiptTime(hop.Timestamp); !ts.IsZero() {
				return ts
			}
		}
	}
	return time.Now().UTC()
}

func parseReceiptTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts.UTC()
}

func providerFromEnv(env *proto.HCSF, result auditledger.AuditLedgerResult) string {
	provider := result.UpstreamProvider
	if env != nil {
		provider = env.RequestMeta.Provider
		for _, hop := range env.Accounting.HopChain {
			if hop.Hop == proto.HopProvider && hop.Provider != "" {
				provider = hop.Provider
			}
		}
	}
	return provider
}

func requestedModelFromEnv(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.Accounting.ModelChain != nil && env.Accounting.ModelChain.Requested != "" {
		return env.Accounting.ModelChain.Requested
	}
	return env.RequestMeta.Model
}

func routedModelFromEnv(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.Accounting.ModelChain != nil && env.Accounting.ModelChain.RouteDecided != "" {
		return env.Accounting.ModelChain.RouteDecided
	}
	return env.RequestMeta.UpstreamModel
}

func upstreamModelFromEnv(env *proto.HCSF, result auditledger.AuditLedgerResult) string {
	if env != nil {
		if env.RequestMeta.UpstreamModel != "" {
			return env.RequestMeta.UpstreamModel
		}
		if env.Accounting.ModelChain != nil && env.Accounting.ModelChain.RouteDecided != "" {
			return env.Accounting.ModelChain.RouteDecided
		}
	}
	return result.UpstreamModel
}

func deliveredModelFromEnv(env *proto.HCSF, result auditledger.AuditLedgerResult) string {
	if env != nil {
		if env.BufferedResponse != nil && env.BufferedResponse.Model != "" {
			return env.BufferedResponse.Model
		}
		if env.Accounting.ModelChain != nil {
			if env.Accounting.ModelChain.UpstreamReported != "" {
				return env.Accounting.ModelChain.UpstreamReported
			}
			if env.Accounting.ModelChain.RouteDecided != "" {
				return env.Accounting.ModelChain.RouteDecided
			}
		}
		if env.RequestMeta.UpstreamModel != "" {
			return env.RequestMeta.UpstreamModel
		}
	}
	return result.UpstreamModel
}

func tokenCountsFromEnv(env *proto.HCSF) TokenCounts {
	if env == nil {
		return TokenCounts{}
	}
	usage := env.Accounting.Usage
	cacheCreation := usage.CacheCreationInputTokens
	if cacheCreation == 0 {
		cacheCreation = usage.CacheCreationInputTokens5m + usage.CacheCreationInputTokens1h
	}
	return TokenCounts{
		Input:  int64(usage.InputTokens),
		Output: int64(usage.OutputTokens),
		Cached: int64(cacheCreation + usage.CacheReadInputTokens),
	}
}

