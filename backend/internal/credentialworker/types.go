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

// stormAcquirer 是三 scope 的 refresh-storm 准入契约。
// Acquire 预留持久化的 account 槽位(其返回的 func 用于释放它)。
// AcquireProviderEndpoint / AcquireGlobal 消耗 endpoint/global 速率预算；生产接线
// 使用共享存储保证跨副本一致，单实例开发接线可使用进程内实现。
// AcquireProviderEndpoint 返回的 func 用于退还其 token,且仅在级联中靠后的某个
// scope 拒绝时被调用。一次拒绝以非空的 Outcome 来表示(此时 func 为 nil);一次
// 基础设施故障以非 nil 的 error 来表示。
type stormAcquirer interface {
	Acquire(ctx context.Context, tenantID, accountID int64) (func(), auth.Outcome, error)
	AcquireProviderEndpoint(ctx context.Context, tenantID int64, providerCode, endpointFingerprint string) (func(), auth.Outcome, error)
	AcquireGlobal(ctx context.Context, tenantID int64) (func(), auth.Outcome, error)
}

type Option func(*Scheduler)
