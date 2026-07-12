package rate

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresAccountErrorRulesProvider 在上游错误路径(不频繁)上,从
// provider_accounts 表获取按账号的错误封禁配置。
// 遇到任何查询错误时返回空切片(fail-open),以确保永不阻塞错误路径。
type postgresAccountErrorRulesProvider struct {
	pool *pgxpool.Pool
}

// NewPostgresAccountErrorRulesProvider 返回一个由 Postgres 支撑的
// AccountErrorRulesProvider。pool 必须非 nil。
func NewPostgresAccountErrorRulesProvider(pool *pgxpool.Pool) AccountErrorRulesProvider {
	return &postgresAccountErrorRulesProvider{pool: pool}
}

// GetAccountErrorPolicy 实现 AccountErrorRulesProvider。
// 它应用两个 enable 标志:
//   - 当 temp_unschedulable_enabled = false 时返回空 rules
//   - 当 custom_error_codes_enabled = false 时返回空 codes
func (p *postgresAccountErrorRulesProvider) GetAccountErrorPolicy(accountID int64) AccountErrorPolicy {
	if p == nil || p.pool == nil || accountID <= 0 {
		return AccountErrorPolicy{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	row := p.pool.QueryRow(ctx,
		`SELECT temp_unschedulable_enabled, temp_unschedulable_rules,
			        custom_error_codes_enabled, custom_error_codes, pool_mode
			   FROM provider_accounts
			  WHERE id = $1
			    AND deleted_at IS NULL`,
		accountID,
	)

	var (
		tempEnabled   bool
		rulesRaw      []byte
		customEnabled bool
		customCodes   []int32
		poolMode      bool
	)
	if err := row.Scan(&tempEnabled, &rulesRaw, &customEnabled, &customCodes, &poolMode); err != nil {
		// 行未找到或查询错误:fail-open。
		return AccountErrorPolicy{}
	}

	var rules []TempUnschedulableRule
	if tempEnabled {
		rules = ParseTempUnschedulableRules(rulesRaw)
	}

	var effectiveCodes []int32
	if customEnabled {
		effectiveCodes = customCodes
	}

	return AccountErrorPolicy{Rules: rules, CustomErrorCodes: effectiveCodes, PoolMode: poolMode}
}
