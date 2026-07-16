package quota

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestAssessPolicyBurstHardCapAcrossWindowMetrics 守住窗口真顶为基础上限加突发值。
// 变异:任一 metric 改回只与 LimitValue 比较,对应的 burst 放行区间都会变红。
func TestAssessPolicyBurstHardCapAcrossWindowMetrics(t *testing.T) {
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		metric         Metric
		current        string
		predictedCost  string
		reservedTokens int64
		wantExceeded   bool
	}{
		{name: "requests within burst", metric: MetricRequests, current: "10", wantExceeded: false},
		{name: "requests above hard cap", metric: MetricRequests, current: "15", wantExceeded: true},
		{name: "cost at hard cap", metric: MetricCostUSD, current: "10", predictedCost: "5", wantExceeded: false},
		{name: "cost above hard cap", metric: MetricCostUSD, current: "10", predictedCost: "5.01", wantExceeded: true},
		{name: "tokens at hard cap", metric: MetricTokensEstimated, current: "10", reservedTokens: 5, wantExceeded: false},
		{name: "tokens above hard cap", metric: MetricTokensEstimated, current: "10", reservedTokens: 6, wantExceeded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &burstAssessmentStore{counter: WindowCounter{
				TenantID:      1,
				ID:            71,
				PolicyID:      81,
				ReservedValue: decimal.RequireFromString(tt.current),
				SettledValue:  decimal.Zero,
			}}
			policy := Policy{
				TenantID:   1,
				ID:         81,
				Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
				Metric:     tt.metric,
				Window:     Window{Kind: WindowFixed, Seconds: 3600},
				LimitValue: decimal.NewFromInt(10),
				BurstValue: decimal.NewFromInt(5),
				Mode:       ModeEnforce,
				ValidFrom:  at.Add(-time.Hour),
			}
			req := ReserveRequest{
				TenantID:       1,
				PredictedCost:  decimal.RequireFromString(decimalOrZero(tt.predictedCost)),
				ReservedTokens: tt.reservedTokens,
				At:             at,
			}

			assessment, err := assessPolicy(context.Background(), store, req, policy)
			if err != nil {
				t.Fatalf("assessPolicy: %v", err)
			}
			if assessment.exceeded != tt.wantExceeded {
				t.Fatalf("exceeded=%v want %v; metric=%s current=%s amount=%s limit=10 burst=5",
					assessment.exceeded, tt.wantExceeded, tt.metric, assessment.current, assessment.amount)
			}
			if !assessment.limit.Equal(decimal.NewFromInt(10)) {
				t.Fatalf("assessment.limit=%s want base LimitValue 10", assessment.limit)
			}

			var payload map[string]any
			if err := json.Unmarshal(assessment.payload(policy), &payload); err != nil {
				t.Fatalf("decode assessment payload: %v", err)
			}
			if payload["limit"] != "10" || payload["burst_value"] != "5" || payload["effective_limit"] != "15" {
				t.Fatalf("payload limits=%v/%v/%v want 10/5/15",
					payload["limit"], payload["burst_value"], payload["effective_limit"])
			}
		})
	}
}

// TestAssessPolicyBurstZeroPreservesBaseLimitComparison 钉住默认 burst=0 时仍按
// current+amount > LimitValue 拒绝,边界与本次改动前完全一致。
func TestAssessPolicyBurstZeroPreservesBaseLimitComparison(t *testing.T) {
	at := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		metric         Metric
		current        string
		predictedCost  string
		reservedTokens int64
	}{
		{name: "requests boundary", metric: MetricRequests, current: "9"},
		{name: "requests above", metric: MetricRequests, current: "10"},
		{name: "cost boundary", metric: MetricCostUSD, current: "7.5", predictedCost: "2.5"},
		{name: "cost above", metric: MetricCostUSD, current: "7.5", predictedCost: "2.51"},
		{name: "tokens boundary", metric: MetricTokensEstimated, current: "7", reservedTokens: 3},
		{name: "tokens above", metric: MetricTokensEstimated, current: "7", reservedTokens: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &burstAssessmentStore{counter: WindowCounter{
				TenantID:      1,
				ID:            72,
				PolicyID:      82,
				ReservedValue: decimal.RequireFromString(tt.current),
			}}
			policy := Policy{
				TenantID:   1,
				ID:         82,
				Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
				Metric:     tt.metric,
				Window:     Window{Kind: WindowFixed, Seconds: 3600},
				LimitValue: decimal.NewFromInt(10),
				BurstValue: decimal.Zero,
				Mode:       ModeEnforce,
				ValidFrom:  at.Add(-time.Hour),
			}
			req := ReserveRequest{
				TenantID:       1,
				PredictedCost:  decimal.RequireFromString(decimalOrZero(tt.predictedCost)),
				ReservedTokens: tt.reservedTokens,
				At:             at,
			}

			assessment, err := assessPolicy(context.Background(), store, req, policy)
			if err != nil {
				t.Fatalf("assessPolicy: %v", err)
			}
			wantExceeded := assessment.current.Add(assessment.amount).GreaterThan(policy.LimitValue)
			if assessment.exceeded != wantExceeded {
				t.Fatalf("exceeded=%v want legacy comparison %v", assessment.exceeded, wantExceeded)
			}
		})
	}
}

// TestApplyEnforceReservationsUsesBurstHardCap 钉住竞争保护收到的也是窗口真顶；
// 否则预判虽放行,原子 UPDATE 仍会在基础上限处误拒。
func TestApplyEnforceReservationsUsesBurstHardCap(t *testing.T) {
	tests := []struct {
		name   string
		metric Metric
		amount decimal.Decimal
	}{
		{name: "requests", metric: MetricRequests, amount: decimal.NewFromInt(1)},
		{name: "cost", metric: MetricCostUSD, amount: decimal.NewFromInt(5)},
		{name: "tokens", metric: MetricTokensEstimated, amount: decimal.NewFromInt(5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &burstAtomicLimitStore{}
			policy := Policy{
				TenantID:   1,
				ID:         91,
				Scope:      Scope{TenantID: 1, Kind: ScopeUser, ID: "42"},
				Metric:     tt.metric,
				LimitValue: decimal.NewFromInt(10),
				BurstValue: decimal.NewFromInt(5),
			}
			err := applyEnforceReservations(context.Background(), store, ReserveRequest{
				TenantID:      1,
				PredictedCost: decimal.NewFromInt(5),
			}, Reservation{ID: 101}, []evaluatedPolicy{{
				policy: policy,
				window: WindowCounter{ID: 111},
				amount: tt.amount,
				metric: tt.metric,
			}})
			if err != nil {
				t.Fatalf("applyEnforceReservations: %v", err)
			}
			if len(store.limits) != 1 || !store.limits[0].Equal(decimal.NewFromInt(15)) {
				t.Fatalf("atomic limit=%v want effective limit 15", store.limits)
			}
		})
	}
}

func decimalOrZero(raw string) string {
	if raw == "" {
		return "0"
	}
	return raw
}

type burstAssessmentStore struct {
	noTxReserveStore
	counter WindowCounter
}

func (s *burstAssessmentStore) UpsertWindow(context.Context, WindowUpsert) (WindowCounter, error) {
	return s.counter, nil
}

func (s *burstAssessmentStore) GetWindowForUpdate(context.Context, int64, int64) (WindowCounter, error) {
	return s.counter, nil
}

type burstAtomicLimitStore struct {
	noTxReserveStore
	limits []decimal.Decimal
}

func (s *burstAtomicLimitStore) IncrementWindowReserved(_ context.Context, input WindowReserve) (WindowCounter, error) {
	s.limits = append(s.limits, input.LimitValue)
	return WindowCounter{TenantID: input.TenantID, ID: input.WindowID}, nil
}
