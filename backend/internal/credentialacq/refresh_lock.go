package credentialacq

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func AcquireCredentialRefreshAdvisoryLock(ctx context.Context, tx db.DBTX, accountCredentialID int64) error {
	if tx == nil {
		return errors.New("credentialacq: refresh lock requires transaction db")
	}
	if accountCredentialID <= 0 {
		return errors.New("credentialacq: account_credential_id must be positive")
	}
	// 锁键整串在 Go 侧拼好后作单个 text 参数传入。原先 `'credential_refresh:' || $1::text` 把 int64
	// 声明成 text 参数,pgx 扩展协议下无法把 int64 编码成 text(OID 25)→ "cannot find encode plan",
	// 导致凭证刷新锁(及其保护的整个刷新事务)失败。哈希值不变。
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("credential_refresh:%d", accountCredentialID))
	return err
}

func WithRefreshLock(ctx context.Context, tx db.DBTX, accountCredentialID int64, fn func(db.DBTX) error) error {
	if err := AcquireCredentialRefreshAdvisoryLock(ctx, tx, accountCredentialID); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn(tx)
}
