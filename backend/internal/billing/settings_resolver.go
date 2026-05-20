package billing

import (
	"context"
	"expvar"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultPolicyResolverTTL           = 45 * time.Second
	policyResolverRefreshRetryInterval = 5 * time.Second
	policyResolverStaleGraceMultiplier = 10
	policyResolverMaxStaleGrace        = 10 * time.Minute
)

var billingSettingsMetrics = expvar.NewMap("billing_settings")

func init() {
	billingSettingsMetrics.Add("resolver_db_read_fail_total", 0)
	billingSettingsMetrics.Add("resolver_stale_on_refresh_failure_total", 0)
	billingSettingsMetrics.Add("resolver_invalid_value_total", 0)
}

// PolicyResolver 解析租户的生效计费策略, 并维护进程内 TTL 缓存。
type PolicyResolver struct {
	store PolicyStore
	ttl   time.Duration
	now   func() time.Time

	mu    sync.Mutex
	cache map[int64]policyCacheEntry
	gen   map[int64]uint64
}

type policyCacheEntry struct {
	policy        StreamInputOnlyInterruptedPolicy
	expiresAt     time.Time
	staleDeadline time.Time
}

// NewPolicyResolver 构造策略解析器。ttl <= 0 时使用 45 秒默认值。
func NewPolicyResolver(store PolicyStore, ttl time.Duration) *PolicyResolver {
	if ttl <= 0 {
		ttl = defaultPolicyResolverTTL
	}
	return &PolicyResolver{
		store: store,
		ttl:   ttl,
		now:   func() time.Time { return time.Now().UTC() },
		cache: make(map[int64]policyCacheEntry),
		gen:   make(map[int64]uint64),
	}
}

// ResolveStreamInputOnlyInterruptedPolicy 返回租户当前生效策略。
func (r *PolicyResolver) ResolveStreamInputOnlyInterruptedPolicy(ctx context.Context, tenantID int64) StreamInputOnlyInterruptedPolicy {
	if r == nil {
		return DefaultStreamInputOnlyInterruptedPolicy
	}
	now := r.currentTime()
	entry, hasEntry := r.cacheGet(tenantID)
	if hasEntry && now.Before(entry.expiresAt) {
		return entry.policy
	}
	if tenantID <= 0 || r.store == nil {
		r.warnFallback(ctx, tenantID, "resolver_unavailable", nil)
		return DefaultStreamInputOnlyInterruptedPolicy
	}

	generation := r.captureGeneration(tenantID)
	row, ok, err := r.store.Get(ctx, tenantID, StreamInputOnlyInterruptedPolicyKey)
	if err != nil {
		if hasEntry && now.Before(entry.staleDeadline) {
			r.cacheSetIfGeneration(tenantID, policyCacheEntry{
				policy:        entry.policy,
				expiresAt:     now.Add(policyResolverRefreshRetryInterval),
				staleDeadline: entry.staleDeadline,
			}, generation)
			r.warnServedStale(ctx, tenantID, err, entry.expiresAt, entry.staleDeadline)
			return entry.policy
		}
		if hasEntry {
			r.cacheDeleteIfUnchanged(tenantID, entry, generation)
		}
		r.warnFallback(ctx, tenantID, "db_read_failed", err)
		return DefaultStreamInputOnlyInterruptedPolicy
	}
	policy := DefaultStreamInputOnlyInterruptedPolicy
	if ok {
		parsed, err := ParseStreamInputOnlyInterruptedPolicy(row.Value)
		if err != nil {
			billingSettingsMetrics.Add("resolver_invalid_value_total", 1)
			slog.WarnContext(ctx, "billing settings resolver ignored invalid value",
				"tenant_id", tenantID,
				"setting_key", StreamInputOnlyInterruptedPolicyKey,
				"value", row.Value,
				"error", err)
		} else {
			policy = parsed
		}
	}
	expiresAt := now.Add(r.ttl)
	r.cacheSetIfGeneration(tenantID, policyCacheEntry{
		policy:        policy,
		expiresAt:     expiresAt,
		staleDeadline: expiresAt.Add(r.staleGrace()),
	}, generation)
	return policy
}

// SetStreamInputOnlyInterruptedPolicy 写入策略并失效当前进程的租户缓存。
func (r *PolicyResolver) SetStreamInputOnlyInterruptedPolicy(ctx context.Context, tenantID int64, policy StreamInputOnlyInterruptedPolicy, updatedBy string) error {
	if r == nil || r.store == nil {
		return ErrPoolNotConfigured
	}
	if _, err := r.store.UpsertStreamInputOnlyInterruptedPolicy(ctx, tenantID, policy, updatedBy); err != nil {
		return err
	}
	r.Invalidate(tenantID)
	return nil
}

// Invalidate 清除一个租户的解析缓存。
func (r *PolicyResolver) Invalidate(tenantID int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, tenantID)
	if r.gen == nil {
		r.gen = make(map[int64]uint64)
	}
	r.gen[tenantID]++
}

func (r *PolicyResolver) cacheGet(tenantID int64) (policyCacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[tenantID]
	return entry, ok
}

func (r *PolicyResolver) captureGeneration(tenantID int64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen == nil {
		return 0
	}
	return r.gen[tenantID]
}

func (r *PolicyResolver) cacheSetIfGeneration(tenantID int64, entry policyCacheEntry, generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	currentGeneration := uint64(0)
	if r.gen != nil {
		currentGeneration = r.gen[tenantID]
	}
	if currentGeneration != generation {
		return
	}
	if r.cache == nil {
		r.cache = make(map[int64]policyCacheEntry)
	}
	r.cache[tenantID] = entry
}

func (r *PolicyResolver) cacheDeleteIfUnchanged(tenantID int64, entry policyCacheEntry, generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	currentGeneration := uint64(0)
	if r.gen != nil {
		currentGeneration = r.gen[tenantID]
	}
	if currentGeneration != generation {
		return
	}
	current, ok := r.cache[tenantID]
	if !ok ||
		current.policy != entry.policy ||
		!current.expiresAt.Equal(entry.expiresAt) ||
		!current.staleDeadline.Equal(entry.staleDeadline) {
		return
	}
	delete(r.cache, tenantID)
}

func (r *PolicyResolver) currentTime() time.Time {
	if r.now == nil {
		return time.Now().UTC()
	}
	return r.now().UTC()
}

func (r *PolicyResolver) staleGrace() time.Duration {
	ttl := r.ttl
	if ttl <= 0 {
		ttl = defaultPolicyResolverTTL
	}
	grace := ttl * policyResolverStaleGraceMultiplier
	if grace > policyResolverMaxStaleGrace {
		return policyResolverMaxStaleGrace
	}
	return grace
}

func (r *PolicyResolver) warnFallback(ctx context.Context, tenantID int64, reason string, err error) {
	billingSettingsMetrics.Add("resolver_db_read_fail_total", 1)
	attrs := []any{
		"tenant_id", tenantID,
		"setting_key", StreamInputOnlyInterruptedPolicyKey,
		"reason", reason,
	}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	slog.WarnContext(ctx, "billing settings resolver fell back to default policy", attrs...)
}

func (r *PolicyResolver) warnServedStale(ctx context.Context, tenantID int64, err error, expiresAt time.Time, staleDeadline time.Time) {
	billingSettingsMetrics.Add("resolver_db_read_fail_total", 1)
	billingSettingsMetrics.Add("resolver_stale_on_refresh_failure_total", 1)
	slog.WarnContext(ctx, "billing settings resolver served stale policy after refresh failure",
		"tenant_id", tenantID,
		"setting_key", StreamInputOnlyInterruptedPolicyKey,
		"expires_at", expiresAt,
		"stale_deadline", staleDeadline,
		"error", err)
}
