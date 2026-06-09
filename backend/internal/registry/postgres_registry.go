// Postgres-backed Registry implementation.
//
// Resolve flow per docs/process/plans/2026-04-30-n5-model-registry.md §"Resolve query":
//
//   1. Normalize alias.
//   2. Open a REPEATABLE READ + read-only TX so all reads observe one
//      consistent snapshot — avoids stamping a SnapshotVersion that
//      doesn't describe the rows used.
//   3. Look up tenant-scoped alias row.
//   4. If row exists with status='active', proceed to model lookup.
//   5. If row exists with status='disabled' -> ErrModelDisabled (D3
//      explicit deny — does NOT fall through to global).
//   6. If no tenant row, consult model_registry_tenant_policies; if
//      inherit_global_catalog=true, look up scope='global' alias.
//   7. Otherwise ErrUnknownModel.
//   8. Resolve canonical model row scoped to (tenant_id OR global) —
//      defends against alias-misconfigured-to-foreign-tenant model.
//   9. Concurrently load capabilities + bindings + snapshot version
//      INSIDE the same TX.
//  10. If bindings list empty -> ErrTenantNoAccess.
//  11. Build Resolved and commit (read-only commit is harmless).
//
// Removed in this revision: the
// `scope` column on `model_pool_bindings`. Bindings are ALWAYS tenant-
// scoped because pool_groups are tenant-owned; a "global binding" was
// a conceptual mistake that leaked pool ids across tenants.

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbregistry "github.com/BloomingProsperity/HUAKAI/internal/db/registry"
)

// PostgresRegistry resolves aliases against the model_registry_* tables.
// Construct via NewPostgresRegistry. Cache lifecycle is owned by the
// caller; pass nil to use the no-op cache (default at L0).
type PostgresRegistry struct {
	pool  *pgxpool.Pool
	cache Cache
}

// NewPostgresRegistry wraps a pgxpool. The cache argument may be nil; at
// L0 it always is (per D2 — cache lands in Slice 5).
func NewPostgresRegistry(pool *pgxpool.Pool, cache Cache) *PostgresRegistry {
	if cache == nil {
		cache = noopCache{}
	}
	return &PostgresRegistry{pool: pool, cache: cache}
}

// ResolveModel implements Registry.
func (r *PostgresRegistry) ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (Resolved, error) {
	if r == nil || r.pool == nil {
		return Resolved{}, ErrRegistryBackend
	}
	aliasLower := AliasNormalize(publicAlias)
	if aliasLower == "" {
		return Resolved{}, ErrUnknownModel
	}

	// REPEATABLE READ + read-only: all reads see a single point-in-time
	// snapshot of the registry, so the version we stamp truly describes
	// the rows used to build Resolved.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: begin: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbregistry.New(tx)

	aliasRow, err := r.lookupAlias(ctx, q, tenantID, aliasLower)
	if err != nil {
		return Resolved{}, err
	}
	if aliasRow.aliasStatus != "active" {
		return Resolved{}, ErrModelDisabled
	}

	modelRow, err := q.GetModelByID(ctx, dbregistry.GetModelByIDParams{
		ID:       aliasRow.modelID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Alias points at a model row not visible to this tenant.
			// Either the model is genuinely missing or it belongs to
			// another tenant (defended by the tenant/scope WHERE clause).
			// Either way the resolver returns ErrModelDisabled rather
			// than ErrUnknownModel so audit logs show the alias->model
			// dangling state without leaking enumeration signal.
			return Resolved{}, ErrModelDisabled
		}
		return Resolved{}, fmt.Errorf("%w: get model: %v", ErrRegistryBackend, err)
	}
	if modelRow.Status != "active" {
		return Resolved{}, ErrModelDisabled
	}

	caps, err := q.ListModelCapabilities(ctx, dbregistry.ListModelCapabilitiesParams{
		TenantID: tenantID,
		ModelID:  modelRow.ID,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: list capabilities: %v", ErrRegistryBackend, err)
	}

	bindings, err := q.ListModelPoolBindings(ctx, dbregistry.ListModelPoolBindingsParams{
		TenantID: tenantID,
		ModelID:  modelRow.ID,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: list bindings: %v", ErrRegistryBackend, err)
	}
	if len(bindings) == 0 {
		return Resolved{}, ErrTenantNoAccess
	}

	version, err := q.GetTenantSnapshotVersion(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return Resolved{}, fmt.Errorf("%w: snapshot: %v", ErrRegistryBackend, err)
		}
		// Missing snapshot row = tenant has had no admin writes yet;
		// treat as version 1 (matches the schema DEFAULT).
		version = 1
	}

	if err := tx.Commit(ctx); err != nil {
		return Resolved{}, fmt.Errorf("%w: commit: %v", ErrRegistryBackend, err)
	}

	out := Resolved{
		PublicAlias:            aliasRow.publicAliasDisplay,
		CanonicalModelID:       modelRow.CanonicalID,
		DefaultProviderModelID: modelRow.DefaultProviderModelID,
		ProviderModelID:        modelRow.DefaultProviderModelID,
		ContextWindow:          int(modelRow.DefaultContextWindow),
		PricingClass:           modelRow.PricingClass,
		ProtocolFamily:         modelRow.ProtocolFamily,
		RequestTimeoutMS:       int(modelRow.DefaultRequestTimeoutMs),
		Capabilities:           make([]string, 0, len(caps)),
		PoolCandidates:         make([]int64, 0, len(bindings)),
		BindingMetadata:        make([]BindingMetadata, 0, len(bindings)),
		SnapshotVersion:        fmt.Sprintf("registry:%d:%d", tenantID, version),
	}
	for _, c := range caps {
		out.Capabilities = append(out.Capabilities, c.Capability)
	}
	for _, b := range bindings {
		bodyParamStrips, err := decodeBindingBodyParamStrips(b.BodyParamStrips)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode body_param_strips: %v", ErrRegistryBackend, err)
		}
		sensitiveWords, err := decodeBindingBodyParamStrips(b.SensitiveWords)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode sensitive_words: %v", ErrRegistryBackend, err)
		}
		paramOverride, err := decodeBindingParamOverride(b.ParamOverride)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode param_override: %v", ErrRegistryBackend, err)
		}
		out.PoolCandidates = append(out.PoolCandidates, b.PoolGroupID)
		// Binding-level provider model rename takes precedence over the
		// model's default; first non-nil override wins for the primary
		// candidate. Per-attempt overrides can replace this later.
		if b.ProviderModelIDOverride != nil && len(out.PoolCandidates) == 1 {
			out.ProviderModelID = *b.ProviderModelIDOverride
		}
		out.BindingMetadata = append(out.BindingMetadata, BindingMetadata{
			BindingID:               b.ID,
			PoolGroupID:             b.PoolGroupID,
			Priority:                b.Priority,
			Weight:                  b.Weight,
			SelectionMode:           b.SelectionMode,
			ProviderModelIDOverride: b.ProviderModelIDOverride,
			RPMLimit:                b.RpmLimit,
			TPMLimit:                b.TpmLimit,
			MaxParallelRequests:     b.MaxParallelRequests,
			FallbackClass:           b.FallbackClass,
			BodyParamStrips:         bodyParamStrips,
			SensitiveWords:          sensitiveWords,
			ParamOverride:           paramOverride,
		})
	}
	return out, nil
}

func decodeBindingBodyParamStrips(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, err
	}
	out := keys[:0]
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func decodeBindingParamOverride(raw string) (map[string]json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var override map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &override); err != nil {
		return nil, err
	}
	for key := range override {
		if strings.TrimSpace(key) == "" {
			delete(override, key)
		}
	}
	if len(override) == 0 {
		return nil, nil
	}
	return override, nil
}

// resolvedAliasRow is the common shape of LookupTenantAlias /
// LookupGlobalAlias rows — both produce the same five fields.
type resolvedAliasRow struct {
	aliasID            int64
	modelID            int64
	aliasStatus        string
	disabledReason     *string
	publicAliasDisplay string
}

// lookupAlias runs the two-step tenant-then-global resolution per D3
// (explicit-deny invariant: tenant-disabled blocks global fallback).
// All reads use the caller-supplied Queries (which is bound to the
// outer REPEATABLE READ tx for snapshot consistency).
func (r *PostgresRegistry) lookupAlias(ctx context.Context, q *dbregistry.Queries, tenantID int64, aliasLower string) (resolvedAliasRow, error) {
	tenantRow, err := q.LookupTenantAlias(ctx, dbregistry.LookupTenantAliasParams{
		TenantID:   tenantID,
		AliasLower: aliasLower,
	})
	if err == nil {
		return resolvedAliasRow{
			aliasID:            tenantRow.AliasID,
			modelID:            tenantRow.ModelID,
			aliasStatus:        tenantRow.AliasStatus,
			disabledReason:     tenantRow.DisabledReason,
			publicAliasDisplay: tenantRow.PublicAliasDisplay,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return resolvedAliasRow{}, fmt.Errorf("%w: tenant alias: %v", ErrRegistryBackend, err)
	}

	// Tenant miss — check inheritance policy.
	inherit, err := q.GetTenantInheritGlobal(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return resolvedAliasRow{}, fmt.Errorf("%w: tenant policy: %v", ErrRegistryBackend, err)
		}
		// No policy row = no inheritance.
		inherit = false
	}
	if !inherit {
		return resolvedAliasRow{}, ErrUnknownModel
	}

	globalRow, err := q.LookupGlobalAlias(ctx, aliasLower)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return resolvedAliasRow{}, ErrUnknownModel
		}
		return resolvedAliasRow{}, fmt.Errorf("%w: global alias: %v", ErrRegistryBackend, err)
	}
	return resolvedAliasRow{
		aliasID:            globalRow.AliasID,
		modelID:            globalRow.ModelID,
		aliasStatus:        globalRow.AliasStatus,
		disabledReason:     globalRow.DisabledReason,
		publicAliasDisplay: globalRow.PublicAliasDisplay,
	}, nil
}

// Compile-time assertion that PostgresRegistry implements Registry.
var _ Registry = (*PostgresRegistry)(nil)
