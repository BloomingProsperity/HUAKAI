//go:build integration_pg

package dispatcher

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// TestDBAccountSourceMediaListedGateWiring 从 DBAccountSource.ListAccounts 全链验证媒体清单门
// 的生产接线:EndpointFamily → mediaEndpointFamily → require_model_listed → SQL 谓词。
// 与 db/billing 层直接传布尔的 SQL 测试互补——本测试的变异面是接线本身:删掉
// account_source.go 的 RequireModelListed 赋值、或集合漏掉某个媒体族,对应子测试转红。
// 同时锁定 allow-list 匹配用的是 ProviderModelID(上游模型名),不是公开别名。
func TestDBAccountSourceMediaListedGateWiring(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping media listed gate wiring test")
	}
	pgPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 PG: %v", err)
	}
	defer pgPool.Close()

	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("media-wire-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	insertAccount := func(name string, allowList []string) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type,
				health_state, credential_state, model_allow_list
			)
			VALUES ($1, $2, $3, $4, 'api_key', 'healthy', 'valid', $5)
			RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix, allowList,
		).Scan(&id); err != nil {
			t.Fatalf("插入账号 %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO account_credentials (
				tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
				encrypted_payload, key_id, nonce, aad_hash
			) VALUES ($1, $2, 'openai', 'api_key', 'active', 1, $3, 'test-key', $4, $5)`,
			tenantID, id, []byte("ciphertext"), []byte("nonce-12345678"), "aad-"+suffix+name,
		); err != nil {
			t.Fatalf("插入凭据 %s: %v", name, err)
		}
		return id
	}

	emptyListID := insertAccount("empty-list", []string{})
	listedID := insertAccount("listed", []string{"prov-image-model"})

	source := NewDBAccountSource(dbbilling.New(tx))
	list := func(endpointFamily string) map[int64]bool {
		t.Helper()
		snaps, err := source.ListAccounts(ctx, SelectionRequest{
			TenantID:    tenantID,
			PoolGroupID: poolGroupID,
			// 公开别名与上游模型名刻意不同:清单只列上游名,命中即证明匹配用 ProviderModelID。
			RequestedModel:  "public-alias",
			ProviderModelID: "prov-image-model",
			ProtocolFamily:  "openai_chat",
			EndpointFamily:  endpointFamily,
		})
		if err != nil {
			t.Fatalf("ListAccounts(%s): %v", endpointFamily, err)
		}
		out := make(map[int64]bool, len(snaps))
		for _, snap := range snaps {
			out[snap.ID] = true
		}
		return out
	}

	for _, family := range []string{"images", "videos", "audio", "embeddings", "rerank", "gemini_count_tokens"} {
		got := list(family)
		if got[emptyListID] {
			t.Fatalf("媒体族 %s:空清单账号 id=%d 不得入选", family, emptyListID)
		}
		if !got[listedID] {
			t.Fatalf("媒体族 %s:清单含上游模型名的账号 id=%d 应入选(ProviderModelID 匹配)", family, listedID)
		}
	}
	for _, family := range []string{"chat", "gemini_generate_content", ""} {
		got := list(family)
		if !got[emptyListID] || !got[listedID] {
			t.Fatalf("非媒体族 %q:空清单(无限制)与已列账号都应入选,got=%v", family, got)
		}
	}
}
