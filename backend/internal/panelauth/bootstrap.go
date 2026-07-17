package panelauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// AdminBootstrapEmailEnv 指定要提升为 role=admin 的运维账号邮箱。
const AdminBootstrapEmailEnv = "HUAKAI_ADMIN_BOOTSTRAP_EMAIL"

// ErrBootstrapBackend 标记 MaybeBootstrapAdminUser 的基础设施失败(启动 fatal)。
var ErrBootstrapBackend = errors.New("panelauth: admin user bootstrap backend error")

// MaybeBootstrapAdminUser 把 HUAKAI_ADMIN_BOOTSTRAP_EMAIL 指定的、已在 tenantID 下注册的
// 账号提升为 role=admin。解决 role 制单登录(D1 硬切,取消粘贴 admin token 入口)的鸡生蛋:
// 第一个 admin 账号从哪来——运维自己用正常注册流建账号,再由本钩子按邮箱提升。
//
// HUAKAI 不创建合成账号，env 只保存邮箱而非凭据，并复用包含社交登录和 2FA 的
// 真实注册流，因此不存在默认弱密码。
//
// 安全姿态镜像 admin.MaybeBootstrap:
//   - env 未设 → no-op(绝大多数部署)。
//   - 账号已是 admin → 幂等 no-op。
//   - 邮箱无对应账号(未注册 / 拼错)→ 记日志、绝不 crash(陈旧 env 不破坏启动)。
//   - advisory lock 串行化多网关并发启动。
func MaybeBootstrapAdminUser(ctx context.Context, pool *pgxpool.Pool, tenantID int64, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	email := strings.TrimSpace(os.Getenv(AdminBootstrapEmailEnv))
	if email == "" {
		return nil
	}
	// 参数校验先于 pool(纯值检查,与是否接上 DB 无关):tenantID<=0 会命中系统伪租户
	// 甚至全表,必须拦。
	if tenantID <= 0 {
		return fmt.Errorf("%w: 非法 tenantID %d", ErrBootstrapBackend, tenantID)
	}
	if pool == nil {
		return fmt.Errorf("%w: nil pool", ErrBootstrapBackend)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin tx: %v", ErrBootstrapBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 与 admin_bootstrap / tenancy_default_tenant 不同的 lock key:三钩子互不阻塞。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin_bootstrap_user'::text, 0))`); err != nil {
		return fmt.Errorf("%w: advisory lock: %v", ErrBootstrapBackend, err)
	}

	// 只提升「未软删、当前非 admin」的匹配账号。lower(email) 与 uq_users_tenant_email
	// 的大小写不敏感唯一约束一致;tenant_id 谓词防跨租户误提升他租户同邮箱账号。
	tag, err := tx.Exec(ctx, `
UPDATE users SET role = 'admin', updated_at = now()
WHERE tenant_id = $1 AND lower(email) = lower($2) AND deleted_at IS NULL AND role <> 'admin'`,
		tenantID, email)
	if err != nil {
		return fmt.Errorf("%w: promote: %v", ErrBootstrapBackend, err)
	}

	if tag.RowsAffected() == 0 {
		// 0 行有两因:已是 admin(幂等)或无此账号(陈旧 env)。查存在性只为给有用日志,
		// 两者都不是错误。
		var exists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM users WHERE tenant_id = $1 AND lower(email) = lower($2) AND deleted_at IS NULL)`,
			tenantID, email).Scan(&exists); err != nil {
			return fmt.Errorf("%w: existence check: %v", ErrBootstrapBackend, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("%w: commit: %v", ErrBootstrapBackend, err)
		}
		if exists {
			logger.Info("admin 用户 bootstrap 跳过:账号已是 admin(幂等)",
				zap.String("email", email), zap.Int64("tenant_id", tenantID))
		} else {
			logger.Info("admin 用户 bootstrap 跳过:无匹配账号(尚未注册或邮箱拼错),不 crash",
				zap.String("email", email), zap.Int64("tenant_id", tenantID))
		}
		return nil
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrBootstrapBackend, err)
	}
	logger.Warn("admin 用户 bootstrap:已把账号提升为 admin —— 请确认这是预期的运维账号",
		zap.String("email", email), zap.Int64("tenant_id", tenantID))
	return nil
}
