//go:build integration_pg

package registry

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestModelSyncStampsLastCheck(t *testing.T) {
	// MUTATION: apply 不 stamp provider_accounts -> last_check_at 仍 NULL -> RED.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedModelSyncTrackingAccount(t, ctx, pool, suffix, "openai")
	modelID := "gpt-cred-040-" + suffix
	t.Cleanup(func() {
		cleanupModelSyncTracking(t, context.Background(), pool, tenantID, suffix)
	})

	svc := modelsync.NewService(modelsync.ServiceConfig{
		Fetchers: []modelsync.Fetcher{
			modelSyncFetcherFunc(func(context.Context) (modelsync.Catalog, error) {
				return modelsync.Catalog{
					Vendor: modelsync.VendorOpenAI,
					Models: []modelsync.Model{{
						ID:             modelID,
						DisplayName:    "GPT CRED 040",
						OwnedBy:        "openai",
						ProtocolFamily: "openai_chat",
						ContextWindow:  128000,
					}},
				}, nil
			}),
		},
		Store: NewPostgresRegistry(pool, nil),
		Now: func() time.Time {
			return time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
		},
	})

	if _, err := svc.Sync(ctx, "cred-040"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var lastCheck pgtype.Timestamptz
	var detectedRaw, ignoredRaw, removedRaw []byte
	if err := pool.QueryRow(ctx, `
SELECT model_sync_last_check_at, model_update_detected, model_update_ignored, model_update_removed
FROM provider_accounts
WHERE id = $1
`, accountID).Scan(&lastCheck, &detectedRaw, &ignoredRaw, &removedRaw); err != nil {
		t.Fatalf("read provider account sync tracking: %v", err)
	}
	if !lastCheck.Valid {
		t.Fatalf("model_sync_last_check_at is NULL, want sync completion timestamp")
	}
	if !jsonArrayContainsString(t, detectedRaw, modelID) {
		t.Fatalf("model_update_detected=%s, want %q", detectedRaw, modelID)
	}
	if !jsonArrayEqualsStrings(t, ignoredRaw, nil) {
		t.Fatalf("model_update_ignored=%s, want [] on first detected sync", ignoredRaw)
	}
	if !jsonArrayEqualsStrings(t, removedRaw, nil) {
		t.Fatalf("model_update_removed=%s, want [] on first detected sync", removedRaw)
	}
}

type modelSyncFetcherFunc func(context.Context) (modelsync.Catalog, error)

func (f modelSyncFetcherFunc) FetchCatalog(ctx context.Context) (modelsync.Catalog, error) {
	return f(ctx)
}

func seedModelSyncTrackingAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix, providerCode string) (tenantID, accountID int64) {
	t.Helper()
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "model-sync-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1, $2, $3, 'openai_chat')
RETURNING id
`, tenantID, providerCode, "Model Sync "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "model-sync-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "model-sync-channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
VALUES ($1, $2, $3, $4, 'api_key')
RETURNING id
`, tenantID, providerID, channelID, "model-sync-account-"+suffix).Scan(&accountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}
	return tenantID, accountID
}

func cleanupModelSyncTracking(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM model_discovery_inbox WHERE provider_model_id LIKE '%' || $1 || '%'`, suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_registry_capabilities WHERE scope = 'global' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_aliases WHERE scope = 'global' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%'`, suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID)
}

func jsonArrayContainsString(t *testing.T, raw []byte, want string) bool {
	t.Helper()
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode json array %s: %v", raw, err)
	}
	for _, item := range got {
		if item == want {
			return true
		}
	}
	return false
}

func jsonArrayEqualsStrings(t *testing.T, raw []byte, want []string) bool {
	t.Helper()
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode json array %s: %v", raw, err)
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
