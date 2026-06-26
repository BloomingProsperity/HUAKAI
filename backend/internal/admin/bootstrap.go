// Bootstrap-admin token 加载器。
//
// 问题:/admin/v1/api-keys 是一个真实端点,但要签发第一个 admin token
// 必须先以 admin 身份认证 —— 鸡生蛋问题。解决方案:启动时读取
// HUAKAI_ADMIN_BOOTSTRAP_TOKEN;若已设置【且】admin_tokens 为空,
// 则 INSERT 一行 bootstrap=true 的 platform_admin。运维用它一次性签发
// 真正的 admin token,随后轮换 / 禁用该 bootstrap 行。
//
// 安全姿态:
//   - 该 env var 持有明文。运维【必须】将其视作 Secret
//    (k8s Secret、sealed-secret、vault)—— 不能放进 ConfigMap。
//   - 我们刻意仅在 admin_tokens 为空时接受。在已存在真实 admin 之后再
//     设置该 env 是一个 no-op(记日志,不 crash,这样意外的配置漂移
//     不会破坏启动)。
//   - bootstrap 行标记 bootstrap=true,以便日后 admin 工具能在 dashboard
//     上呈现"请轮换我"的告警。

package admin

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// BootstrapEnv 是启动时读取的环境变量。其值为第一个 admin token 的
// 明文 bearer。
const BootstrapEnv = "HUAKAI_ADMIN_BOOTSTRAP_TOKEN"

// MaybeBootstrap 在满足以下条件时插入一行 bootstrap admin token:
//  1. HUAKAI_ADMIN_BOOTSTRAP_TOKEN 已设置且非空
//  2. admin_tokens 中尚无任何行
//
// 对"no-op"(env 未设置或表非空)返回 nil;仅在数据存储 / bcrypt 故障
//(应令启动失败)时返回 error。
//
// count + insert 在一个持有常量 advisory lock 的 TX 内运行,这样两个
// 针对空 DB 启动的 gateway 实例不会都观测到 count=0 而双重插入。锁在
// commit/rollback 时自动释放。
func MaybeBootstrap(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	bearer := os.Getenv(BootstrapEnv)
	if bearer == "" {
		return nil
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin bootstrap tx: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := admindb.New(tx)

	if err := qtx.AcquireAdminBootstrapLock(ctx); err != nil {
		return fmt.Errorf("%w: bootstrap lock: %v", ErrAdminBackend, err)
	}

	count, err := qtx.CountAdminTokensIncludingInactive(ctx)
	if err != nil {
		return fmt.Errorf("%w: count admin_tokens: %v", ErrAdminBackend, err)
	}
	if count > 0 {
		// count 把 disabled/revoked 行也算进来,
		// 这样 bootstrap 保持一次性。跳过【所有】后续工作 —— 不校验、
		// 不 bcrypt、不切片 —— 这样一个陈旧的 env 值(可能对 bcrypt 来说
		// 太长,或比 PrefixLen 还短)无法让一个健康的启动 crash。
		logger.Info("admin bootstrap skipped: admin_tokens has prior rows",
			zap.Int64("token_row_count", count))
		return nil
	}

	// 仅在确知我们确实会插入之后,才校验生成 bearer 的完整形态。
	// hk_admin_ namespace + 24 字符 base32 suffix = 共 33 字符。早期版本
	// 在 count 检查之前就抛这个错,破坏了对已填充 DB 的 no-op 契约。
	const adminNamespace = "hk_admin_"
	const expectedLen = len(adminNamespace) + 24
	if len(bearer) != expectedLen || bearer[:len(adminNamespace)] != adminNamespace {
		return fmt.Errorf("%w: %s must be a generated bearer of shape '%s<24-char-base32>' (%d chars)",
			ErrAdminBadRequest, BootstrapEnv, adminNamespace, expectedLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("%w: bcrypt bootstrap token: %v", ErrAdminBackend, err)
	}
	prefix := bearer[:PrefixLen]

	if _, err = qtx.InsertAdminToken(ctx, admindb.InsertAdminTokenParams{
		Name:      "bootstrap-admin",
		KeyHash:   string(hash),
		KeyPrefix: prefix,
		Role:      RolePlatformAdmin,
		Bootstrap: true,
	}); err != nil {
		return fmt.Errorf("%w: insert bootstrap admin: %v", ErrAdminBackend, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit bootstrap tx: %v", ErrAdminBackend, err)
	}

	logger.Warn("admin bootstrap token loaded from env — rotate before public exposure",
		zap.String("env_var", BootstrapEnv),
		zap.String("key_prefix", prefix))
	return nil
}

var _ = errors.New // 预留未来扩展
