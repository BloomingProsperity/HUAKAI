// Phase C.2 production adapter: DB-backed pool.AccountSource.
//
// Bridges the selector's account-listing seam to a pool-group-keyed sqlc
// query. The previous DBRepository.ListEligibleAccounts was channel-keyed;
// for the chat-completions hot path we want a single round-trip from
// pool_group → eligible accounts.
//
// LoadRate is computed as in_flight_count / cap_concurrency. Capacity rows
// at zero capacity are treated as load=1.0 (excluded by upstream gates,
// not silently division-by-zero).

package dispatcher

import (
	"context"
	"fmt"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// DBAccountSource implements AccountSource over the
// ListEligibleAccountsByPoolGroup sqlc query.
type DBAccountSource struct {
	q *dbbilling.Queries
}

// NewDBAccountSource constructs the adapter from a sqlc.Queries handle.
func NewDBAccountSource(q *dbbilling.Queries) *DBAccountSource {
	return &DBAccountSource{q: q}
}

// ListAccounts implements AccountSource.
func (s *DBAccountSource) ListAccounts(ctx context.Context, req SelectionRequest) ([]*AccountSnapshot, error) {
	if s == nil || s.q == nil {
		return nil, fmt.Errorf("pool: DBAccountSource not configured")
	}
	// 必须把 req.RequestedModel + req.CapabilityFlags
	// 透传到 SQL 端做 model_allow_list / capability_flags 过滤, 否则 production
	// gate AllowAll 全过, 出现 "选到明确不支持该 model 的 account" 误派发。
	required := req.CapabilityFlags
	if required == nil {
		required = []string{}
	}
	rows, err := s.q.ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:             req.TenantID,
		PoolGroupID:          req.PoolGroupID,
		RequestedModel:       req.RequestedModel,
		RequiredCapabilities: required,
	})
	if err != nil {
		return nil, fmt.Errorf("pool: list eligible accounts: %w", err)
	}
	out := make([]*AccountSnapshot, 0, len(rows))
	for _, r := range rows {
		snap := &AccountSnapshot{
			ID:             r.ID,
			TenantID:       r.TenantID,
			Priority:       int(r.Priority),
			MaxConcurrency: int(r.CapConcurrency),
			LoadRate:       loadRate(r.InFlightCount, r.CapConcurrency),
			// MaxWaiting feeds the selector's WaitPlan fallback path
			// (selector.go fallbackWaitPlan). cap_queue_fallback is the
			// per-account cap on the fallback queue length.
			MaxWaiting: int(r.CapQueueFallback),
			// WaitTimeoutMS left at 0 — selector overrides with
			// RoutingPolicy.FallbackTimeoutMS when the policy is set.
			// Per-account timeout override is a Phase E refinement.
		}
		if r.LastDispatchAt.Valid {
			snap.LastUsedAt = r.LastDispatchAt.Time
		}
		out = append(out, snap)
	}
	return out, nil
}

// loadRate maps in-flight count to a [0,1] load fraction. Zero-capacity
// accounts are reported as load=1 so upstream gates exclude them rather
// than triggering division-by-zero.
func loadRate(inFlight, cap int32) float64 {
	if cap <= 0 {
		return 1.0
	}
	if inFlight <= 0 {
		return 0.0
	}
	if inFlight >= cap {
		return 1.0
	}
	return float64(inFlight) / float64(cap)
}

var _ AccountSource = (*DBAccountSource)(nil)
