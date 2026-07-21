package mediatask

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

var errAccountRequestRateLimited = errors.New("mediatask: provider account request rate limited")

// AccountRequestAdmitter 对已经固定账号的后台轮询与产物下载执行账号级 RPM 准入。
// 它不重复消费用户 Key 或模型绑定的逻辑请求预算。
type AccountRequestAdmitter interface {
	Admit(context.Context, int64, int64) error
}

type PostgresAccountRequestAdmitter struct {
	pool    *pgxpool.Pool
	counter *precheck.Counter
}

func NewPostgresAccountRequestAdmitter(pool *pgxpool.Pool, counter *precheck.Counter) *PostgresAccountRequestAdmitter {
	return &PostgresAccountRequestAdmitter{pool: pool, counter: counter}
}

func (a *PostgresAccountRequestAdmitter) Admit(ctx context.Context, tenantID, accountID int64) error {
	if a == nil || a.counter == nil {
		return nil
	}
	if a.pool == nil || tenantID <= 0 || accountID <= 0 {
		return ErrProviderUnavailable
	}
	var rpm, tpm int64
	err := a.pool.QueryRow(ctx, `
SELECT rpm_limit, tpm_limit
FROM provider_accounts
WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, accountID, tenantID).Scan(&rpm, &tpm)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProviderUnavailable
	}
	if err != nil {
		return err
	}
	if decision := a.counter.TryRecord(accountID, precheck.Limits{RPM: rpm, TPM: tpm}, 0); !decision.Allowed {
		return errAccountRequestRateLimited
	}
	return nil
}
