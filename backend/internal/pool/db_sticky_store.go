// db_sticky_store.go — DBRepository → StickyStore adapter (Track B 闭环)。
//
// 把 pool.StickyStore 接口（selector.go:47 trySticky 用）桥接到
// DBRepository.GetStickyBinding (sticky_bindings 表持久化)。
//
// 在 commit e8d1621 之前: WithStickyStore 接口存在但从未被任何 caller
// 注入真实现**, selector 走 trySticky 直接 nil-guard return false → 永远
// fall-through 到 round-robin。Track B 的 prompt-hash 路由价值因此为零。
//
// 本文件提供 DBStickyStore 把 DB 层 sticky_bindings 表暴露给 selector,
// 让 prompt-hash 真生效。
package pool

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/jackc/pgx/v5"
)

// stickyBindingReader 是 DBRepository 的最小子集——只读路径。
// selector.trySticky 只调 GetStickyBinding；upsert 由 selector 选定后
// 单独路径写（未来 atomic）。
type stickyBindingReader interface {
	GetStickyBinding(ctx context.Context, arg db.GetStickyBindingParams) (int64, error)
}

// DBStickyStore 实现 pool.StickyStore 接口。
// 用 sticky_bindings 表 (tenant_id, session_hash, model) → provider_account_id
// 的复合主键。session_hash 在 chat handler 里被填为 prompt-prefix hash
// (cache_routing.ComputePromptHash), 让相同 prefix 的请求路由到同一账号
// 最大化 vendor prompt cache 命中率。
type DBStickyStore struct {
	repo stickyBindingReader
}

// NewDBStickyStore 用 DBRepository (或任何 stickyBindingReader 实现) 构造 store。
func NewDBStickyStore(repo stickyBindingReader) *DBStickyStore {
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
	accountID, err := s.repo.GetStickyBinding(ctx, db.GetStickyBindingParams{
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

// 编译期接口断言
var _ StickyStore = (*DBStickyStore)(nil)
