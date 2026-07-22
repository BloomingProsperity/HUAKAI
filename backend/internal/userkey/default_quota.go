package userkey

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// defaultKeyQuotaConfig 是 Service 上默认 key 配额字段的类型(别名 config.DefaultKeyQuota),
// 与默认配额逻辑同文件,使 userkey.go 无需为此字段单独引 config 包。
type defaultKeyQuotaConfig = config.DefaultKeyQuota

// WithDefaultKeyQuota 注入新建自助 key 时种入的保守默认配额(防滥用兜底)。
// 零值(Enabled=false)= 不种,行为同旧版"无限直到运营手动配"。
func WithDefaultKeyQuota(cfg config.DefaultKeyQuota) Option {
	return func(s *Service) {
		s.defaultKeyQuota = cfg
	}
}

// seedDefaultKeyQuota 在新建 key 的同一事务里种入保守默认配额策略(api_key scope),
// 堵"新 key 默认 0 限制 → 单 key 烧余额前并发猛刷打爆上游账号池"。scope_id 取 key.id 的
// 十进制串,与 relay 请求时 quotaenforce 构造 api_key scope 的口径一致,故策略必被请求前
// Reserve 命中并 enforce。复用现成 quota_policies 表与 Reserve 执行链,不改 schema、不改执行逻辑。
// 仅对自助签发的 key 生效;运维可经 config.DefaultKeyQuota 调整或整体关闭。
func (s *Service) seedDefaultKeyQuota(ctx context.Context, tx pgx.Tx, tenantID, userID, apiKeyID int64) error {
	cfg := s.defaultKeyQuota
	if !cfg.Enabled {
		return nil
	}
	scopeID := strconv.FormatInt(apiKeyID, 10)
	// 维度规格:RPM 走每分钟固定窗口;并发为无窗口槽位计数。limit<=0 的维度跳过(运维关该维度)。
	specs := []struct {
		metric        string
		windowKind    string
		windowSeconds int
		limit         int
	}{
		{metric: string(quota.MetricRequests), windowKind: string(quota.WindowFixed), windowSeconds: 60, limit: cfg.RPM},
		{metric: string(quota.MetricConcurrency), windowKind: string(quota.WindowNone), windowSeconds: 0, limit: cfg.ConcurrencyMax},
	}
	for _, spec := range specs {
		if spec.limit <= 0 {
			continue
		}
		// WHERE EXISTS 复核 key 在本事务可见且 tenant/user 归属正确(防错插);scope_id 全新本无冲突,
		// ON CONFLICT DO NOTHING 仅作幂等兜底。created_by_actor 标 system_default 供运营区分自动种入。
		if _, err := tx.Exec(ctx,
			`INSERT INTO quota_policies (
			     tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
			     limit_value, burst_value, mode, priority, enabled, valid_from,
			     valid_until, created_by_actor, last_modified_by_actor)
			 SELECT $1::bigint, 'api_key', $2::text, $3::text, $4::text, $5::integer,
			        $6::numeric(20,8), 0, 'enforce', 200, true, now(),
			        NULL, 'system_default', 'system_default'
			 WHERE EXISTS (
				     SELECT 1 FROM api_keys ak
				      WHERE ak.id = $7::bigint AND ak.tenant_id = $1::bigint
				        AND ak.user_id = $8::bigint AND ak.purpose = 'user'
				        AND ak.deleted_at IS NULL)
			 ON CONFLICT DO NOTHING`,
			tenantID, scopeID, spec.metric, spec.windowKind, spec.windowSeconds, spec.limit, apiKeyID, userID,
		); err != nil {
			return fmt.Errorf("%w: seed default key quota (%s): %v", ErrBackend, spec.metric, err)
		}
	}
	return nil
}
