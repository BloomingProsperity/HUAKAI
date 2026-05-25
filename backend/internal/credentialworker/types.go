package credentialworker

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// Signer 是审计签名器的最小接口；*sign.Signer 满足它。
type Signer interface {
	Sign(message []byte) []byte
	Fingerprint() string
}

type refreshQueries interface {
	ListAccountsForRefresh(ctx context.Context, arg dbbilling.ListAccountsForRefreshParams) ([]dbbilling.ListAccountsForRefreshRow, error)
}

type stormAcquirer interface {
	Acquire(ctx context.Context, tenantID, accountID int64) (func(), auth.Outcome, error)
}

type Option func(*Scheduler)
