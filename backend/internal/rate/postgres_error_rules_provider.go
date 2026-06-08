package rate

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresAccountErrorRulesProvider fetches per-account error-ban config from
// the provider_accounts table on the upstream-ERROR path (infrequent).
// On any query error it returns empty slices (fail-open) to never block the
// error path.
type postgresAccountErrorRulesProvider struct {
	pool *pgxpool.Pool
}

// NewPostgresAccountErrorRulesProvider returns an AccountErrorRulesProvider
// backed by Postgres. pool must be non-nil.
func NewPostgresAccountErrorRulesProvider(pool *pgxpool.Pool) AccountErrorRulesProvider {
	return &postgresAccountErrorRulesProvider{pool: pool}
}

// GetAccountErrorRules implements AccountErrorRulesProvider.
// It applies both enable flags:
//   - returns empty rules when temp_unschedulable_enabled = false
//   - returns empty codes when custom_error_codes_enabled = false
func (p *postgresAccountErrorRulesProvider) GetAccountErrorRules(accountID int64) ([]TempUnschedulableRule, []int32) {
	if p == nil || p.pool == nil || accountID <= 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	row := p.pool.QueryRow(ctx,
		`SELECT temp_unschedulable_enabled, temp_unschedulable_rules,
		        custom_error_codes_enabled, custom_error_codes
		   FROM provider_accounts
		  WHERE id = $1`,
		accountID,
	)

	var (
		tempEnabled   bool
		rulesRaw      []byte
		customEnabled bool
		customCodes   []int32
	)
	if err := row.Scan(&tempEnabled, &rulesRaw, &customEnabled, &customCodes); err != nil {
		// Row not found or query error: fail-open.
		return nil, nil
	}

	var rules []TempUnschedulableRule
	if tempEnabled {
		rules = ParseTempUnschedulableRules(rulesRaw)
	}

	var effectiveCodes []int32
	if customEnabled {
		effectiveCodes = customCodes
	}

	return rules, effectiveCodes
}
