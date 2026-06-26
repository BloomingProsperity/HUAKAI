// 包 registry:为 Slice 5 预留的 cache 桩。
//
// 第二轮综合(D2 + D13):registry 在 L0 仅做 SELECT 解析,「不带」进程内缓存。缓存
// 随 Slice 5 与 admin-writer 驱动的快照版本号递增一起落地;键将为 (tenant_id,
// alias_normalized, registry_version),这样陈旧条目会在下一次版本不匹配的读取时自我
// 失效。
//
// 本文件仅用于预留接口面。具体实现刻意留空;解析器在 L0 不查询 Cache。

package registry

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// Cache 是 Slice 5 落地后解析器将查询的接口。在 L0 它未被使用——
// registry.NewPostgresRegistry 默认构造一个 noopCache,因此所有 Resolve 都直接走
// Postgres。
type Cache interface {
	Get(ctx context.Context, tenantID int64, aliasLower string, version int64) (router.ResolvedModel, bool)
	Put(ctx context.Context, tenantID int64, aliasLower string, version int64, m router.ResolvedModel)
}

type noopCache struct{}

func (noopCache) Get(_ context.Context, _ int64, _ string, _ int64) (router.ResolvedModel, bool) {
	return router.ResolvedModel{}, false
}

func (noopCache) Put(_ context.Context, _ int64, _ string, _ int64, _ router.ResolvedModel) {}
