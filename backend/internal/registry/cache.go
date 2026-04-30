// Package registry: cache stub for Slice 5.
//
// Round-2 synthesis (D2 + D13): registry resolves SELECT-only at L0 with
// NO process cache. Cache lands in Slice 5 along with admin-writer-driven
// snapshot version bumps; key will be (tenant_id, alias_normalized,
// registry_version) so a stale entry self-invalidates on the next
// version-mismatched read.
//
// This file exists only to reserve the interface surface. Concrete impl
// is intentionally empty; the resolver does not consult Cache at L0.

package registry

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

// Cache is the interface the resolver will consult once Slice 5 lands.
// At L0 this is unused — registry.NewPostgresRegistry constructs a
// noopCache by default, so all Resolves go straight to Postgres.
type Cache interface {
	Get(ctx context.Context, tenantID int64, aliasLower string, version int64) (router.ResolvedModel, bool)
	Put(ctx context.Context, tenantID int64, aliasLower string, version int64, m router.ResolvedModel)
}

type noopCache struct{}

func (noopCache) Get(_ context.Context, _ int64, _ string, _ int64) (router.ResolvedModel, bool) {
	return router.ResolvedModel{}, false
}

func (noopCache) Put(_ context.Context, _ int64, _ string, _ int64, _ router.ResolvedModel) {}
