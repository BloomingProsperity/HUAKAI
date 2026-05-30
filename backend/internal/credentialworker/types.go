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

// stormAcquirer is the three-scope refresh-storm admission contract (S2-045).
// Acquire reserves the durable account slot (its returned func releases it).
// AcquireProviderEndpoint / AcquireGlobal consume the in-memory endpoint/global
// rate budgets; the func returned by AcquireProviderEndpoint refunds its token
// and is invoked only when a later scope in the cascade denies. A denial is
// signaled by a non-empty Outcome (the func is nil); an infrastructure failure
// by a non-nil error.
type stormAcquirer interface {
	Acquire(ctx context.Context, tenantID, accountID int64) (func(), auth.Outcome, error)
	AcquireProviderEndpoint(ctx context.Context, tenantID int64, providerCode, endpointFingerprint string) (func(), auth.Outcome, error)
	AcquireGlobal(ctx context.Context, tenantID int64) (func(), auth.Outcome, error)
}

type Option func(*Scheduler)
