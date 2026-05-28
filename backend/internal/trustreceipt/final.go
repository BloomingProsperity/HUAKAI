package trustreceipt

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const validationStateValid = "valid"

type FinalReceiptFacts struct {
	RequestID           string
	ReceiptSequence     int
	TenantID            int64
	OccurredAt          time.Time
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CostUSDMicros       int64
	RateTableSnapshotID int64
	SnapshotVersion     string
	ValidationState     string
}

func BuildFinalFromSettleEvent(req billing.SettleRequest, env *proto.HCSF, facts FinalReceiptFacts) TrustReceiptV1 {
	result := auditledger.AuditLedgerResult{
		RequestID:        strings.TrimSpace(req.AuditRequestID),
		UpstreamProvider: strings.TrimSpace(req.Provider),
		UpstreamModel:    strings.TrimSpace(req.UpstreamModel),
	}
	requestID := strings.TrimSpace(facts.RequestID)
	if requestID == "" {
		requestID = requestIDFromEnv(env, result)
	}
	requestedModel := firstNonEmpty(requestedModelFromEnv(env), req.RequestedModel, facts.Model)
	routedModel := firstNonEmpty(routedModelFromEnv(env), req.UpstreamModel, facts.Model)
	upstreamModel := firstNonEmpty(upstreamModelFromEnv(env, result), req.UpstreamModel, facts.Model)
	deliveredModel := firstNonEmpty(deliveredModelFromEnv(env, result), facts.Model, upstreamModel)

	return TrustReceiptV1{
		RequestID:                 requestID,
		ReceiptSequence:           facts.ReceiptSequence,
		TenantScopeRef:            tenantScopeRefFromFinalFacts(req, env, facts),
		OccurredAt:                finalOccurredAt(req, env, facts),
		Provider:                  firstNonEmpty(providerFromEnv(env, result), req.Provider),
		RequestedModel:            requestedModel,
		RoutedModel:               routedModel,
		UpstreamModel:             upstreamModel,
		DeliveredModel:            deliveredModel,
		CostCents:                 finalCostCents(req, facts),
		TokenCounts:               finalTokenCounts(env, facts),
		PriceSnapshot:             finalPriceSnapshot(req, facts),
		ValidationState:           finalValidationState(facts.ValidationState),
		RedactedMetadataAllowlist: map[string]any{},
	}
}

func finalOccurredAt(req billing.SettleRequest, env *proto.HCSF, facts FinalReceiptFacts) time.Time {
	if !facts.OccurredAt.IsZero() {
		return facts.OccurredAt.UTC()
	}
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
	if !req.RequestedAt.IsZero() {
		return req.RequestedAt.UTC()
	}
	return time.Now().UTC()
}

func tenantScopeRefFromFinalFacts(req billing.SettleRequest, env *proto.HCSF, facts FinalReceiptFacts) string {
	if env != nil && env.RequestMeta.TenantID > 0 {
		return auditledger.TenantScopeRef(env.RequestMeta.TenantID)
	}
	if facts.TenantID > 0 {
		return auditledger.TenantScopeRef(facts.TenantID)
	}
	if req.TenantID > 0 {
		return auditledger.TenantScopeRef(req.TenantID)
	}
	return ""
}

func finalTokenCounts(env *proto.HCSF, facts FinalReceiptFacts) TokenCounts {
	if env != nil && proto.UsageHasValue(env.Accounting.Usage) {
		return tokenCountsFromEnv(env)
	}
	return TokenCounts{
		Input:  facts.InputTokens,
		Output: facts.OutputTokens,
		Cached: facts.CachedTokens,
	}
}

func finalCostCents(req billing.SettleRequest, facts FinalReceiptFacts) int64 {
	if !req.ActualCost.IsZero() {
		return decimalUSDCents(req.ActualCost)
	}
	if facts.CostUSDMicros > 0 {
		return decimalUSDCents(decimal.NewFromInt(facts.CostUSDMicros).Div(decimal.NewFromInt(1_000_000)))
	}
	return 0
}

func decimalUSDCents(cost decimal.Decimal) int64 {
	if cost.IsNegative() {
		return 0
	}
	return cost.Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}

func finalPriceSnapshot(req billing.SettleRequest, facts FinalReceiptFacts) PriceSnapshot {
	version := firstNonEmpty(facts.SnapshotVersion, req.SnapshotVersion)
	snapshotID := facts.RateTableSnapshotID
	if snapshotID <= 0 {
		snapshotID = rateTableSnapshotIDFromVersion(version)
	}
	return PriceSnapshot{
		RateTableSnapshotID: snapshotID,
		SnapshotVersion:     version,
		CurrencyCode:        "USD",
	}
}

func finalValidationState(state string) string {
	switch strings.TrimSpace(state) {
	case "", validationStateProvisional, "unknown":
		return validationStateValid
	default:
		return strings.TrimSpace(state)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

var (
	finalSnapshotRegistryVersionPattern = regexp.MustCompile(`(?i)(?:^|;)registry:[^:;]+:([0-9]+)(?:;|$)`)
	finalSnapshotRateIDPattern          = regexp.MustCompile(`(?i)(?:rate_table|pricing|rate)[^0-9]*([0-9]+)`)
)

func rateTableSnapshotIDFromVersion(snapshotVersion string) int64 {
	for _, pattern := range []*regexp.Regexp{finalSnapshotRegistryVersionPattern, finalSnapshotRateIDPattern} {
		match := pattern.FindStringSubmatch(snapshotVersion)
		if len(match) == 2 {
			if id, err := strconv.ParseInt(match[1], 10, 64); err == nil && id > 0 {
				return id
			}
		}
	}
	return 0
}
