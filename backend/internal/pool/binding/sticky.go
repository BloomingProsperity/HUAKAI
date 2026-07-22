// DBStickyStore 把 sticky_bindings 持久化能力接入选号器，让相同会话前缀
// 尽量复用同一账号，同时允许只读模式用于不写绑定的场景。
package binding

import (
	"context"
	"errors"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// stickyBindingReader 是黏性绑定只读路径的最小接口。
// selector.trySticky 只调 GetStickyBinding。
type stickyBindingReader interface {
	GetStickyBinding(ctx context.Context, arg dbbilling.GetStickyBindingParams) (int64, error)
}

// stickyBindingWriter 是写路径的最小子集——selector 选定 fresh 账号后调用
// 持久化绑定, 后续 same-prefix 请求即可命中。
type stickyBindingWriter interface {
	UpsertStickyBinding(ctx context.Context, arg dbbilling.UpsertStickyBindingParams) error
}

// stickyBindingRepo 组合黏性绑定的读写能力。
type stickyBindingRepo interface {
	stickyBindingReader
	stickyBindingWriter
}

type StickyBindingReader = stickyBindingReader
type StickyBindingWriter = stickyBindingWriter
type StickyBindingRepo = stickyBindingRepo

// DBStickyStore 实现 pool.StickyStore 接口。
// 用 sticky_bindings 表 (tenant_id, session_hash, model) → provider_account_id
// 的复合主键。session_hash 在 chat handler 里被填为 prompt-prefix hash
// (cache_routing.ComputePromptHash), 让相同 prefix 的请求路由到同一账号
// 最大化 vendor prompt cache 命中率。
type DBStickyStore struct {
	repo stickyBindingReader
	// writer 可选; 非 nil 时 selector 选定 fresh 账号后通过 Upsert 写入。
	// nil 时 store 退化为 read-only (不会持久化新 binding, 适合测试 / 部分场景)。
	writer stickyBindingWriter

	// TTL 控制 sticky_bindings.expires_at = now() + TTL。
	// 默认 1h 对齐 Anthropic extended prompt cache（5min 默认窗口的扩展形态）。
	// 0 = 用 default。
	TTL time.Duration
}

// defaultStickyTTL = 1h, 对齐 Anthropic prompt cache extended TTL。
const defaultStickyTTL = time.Hour

// NewDBStickyStore 构造可读写的数据库黏性存储。
func NewDBStickyStore(repo stickyBindingRepo) *DBStickyStore {
	return &DBStickyStore{repo: repo, writer: repo}
}

// NewDBStickyStoreReadOnly 仅注入 reader (适合不需 upsert 的场景，例如
// 测试或灰度阶段不写新 binding)。
func NewDBStickyStoreReadOnly(repo stickyBindingReader) *DBStickyStore {
	return &DBStickyStore{repo: repo}
}

// Lookup 实现 StickyStore.Lookup。
//
// 行为:
//   - SessionHash 空 → (0, false, nil) — 短 prompt 不参与 sticky
//   - TenantID / Model 空 → (0, false, nil) — 必填字段缺失等同 cache miss
//   - DB 行不存在或过期 (ErrNoRows) → (0, false, nil), 不算错误
//   - DB 真错 → 透传 error 让 selector 决定 fail-open 还是 fail-closed
//
// **不写**任何 binding——upsert 路径由 selector 选定账号后单独触发
// (本 atomic 范围内 read-only 即可，写路径作下一原子)。
func (s *DBStickyStore) Lookup(ctx context.Context, req SelectionRequest) (int64, bool, error) {
	if s == nil || s.repo == nil {
		return 0, false, nil
	}
	// 必填 sanity check
	if req.SessionHash == "" || req.TenantID == 0 || req.RequestedModel == "" {
		return 0, false, nil
	}
	accountID, err := s.repo.GetStickyBinding(ctx, dbbilling.GetStickyBindingParams{
		TenantID:    req.TenantID,
		SessionHash: req.SessionHash,
		Model:       req.RequestedModel,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return accountID, true, nil
}

// Upsert 把 (tenant, sessionHash, model) → accountID 写入 sticky_bindings,
// 让后续相同 prefix 的请求 sticky 到同一账号 = vendor cache hit。
//
// 行为:
//   - SessionHash / TenantID / Model / accountID 任一缺失 → 静默跳过（无 binding 可写）
//   - writer == nil → 静默跳过 (read-only store 模式)
//   - DB 错传播给 caller (selector 决定是否吞掉, 写失败不应阻塞主流程)
//
// expires_at = now() + TTL (默认 1h 对齐 Anthropic extended cache)。
func (s *DBStickyStore) Upsert(ctx context.Context, tenantID int64, sessionHash, model string, accountID int64) error {
	if s == nil || s.writer == nil {
		return nil
	}
	if tenantID == 0 || sessionHash == "" || model == "" || accountID == 0 {
		return nil
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = defaultStickyTTL
	}
	return s.writer.UpsertStickyBinding(ctx, dbbilling.UpsertStickyBindingParams{
		TenantID:          tenantID,
		SessionHash:       sessionHash,
		Model:             model,
		ProviderAccountID: accountID,
		ExpiresAt:         pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	})
}

// 编译期接口断言
var _ StickyStore = (*DBStickyStore)(nil)
