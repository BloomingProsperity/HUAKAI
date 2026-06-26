package binding

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// AuthCredentialGate 按规范 §Phase B "Credential gate: Account credential
// state in {valid, refreshing-with-grace}"，把 auth.TokenProvider 适配成
// pool.CredentialGate。这是 F-AUTH-005 与 F-POOL-001 之间的跨功能接线 ——
// pool selector 在请求路径上调用 auth，在调度前确认账号持有可用 token。
type AuthCredentialGate struct {
	Provider auth.TokenProvider
}

func (g AuthCredentialGate) Allow(ctx context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	// fail-closed: 没注入 Provider (启动 wire 漏 / 测试遗漏) 不能允许账号, 否则
	// credential gate 形同虚设。account == nil 是 caller bug, 同 reject。
	if g.Provider == nil {
		return false, GateFailureCredential, errors.New("auth credential gate: Provider not configured")
	}
	if account == nil {
		return false, GateFailureCredential, errors.New("auth credential gate: nil account snapshot")
	}
	if _, err := g.Provider.GetAccessToken(ctx, account.TenantID, account.ID); err != nil {
		if errors.Is(err, auth.ErrTokenMalformed) || errors.Is(err, auth.ErrAccountUnavailable) {
			return false, GateFailureCredential, nil
		}
		return false, GateFailureCredential, err
	}
	return true, "", nil
}
