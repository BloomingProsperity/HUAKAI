//go:build integration_pg

package pool

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type mixedProviderFamilyCase struct {
	name           string
	vendor         string
	authMode       string
	protocolFamily string
	publicModel    string
	providerModel  string
	primaryID      int64
	backupID       int64
}

func TestMixedProviderPoolKeepsSelectionInsideModelProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pgPool := openIntegrationPool(t, ctx)
	seed := seedAdapterGraph(t, ctx, pgPool, "mixed-provider-pool")

	families := []mixedProviderFamilyCase{
		{name: "GPT", vendor: "openai", authMode: "api_key", protocolFamily: "openai_chat", publicModel: "public-gpt", providerModel: "wire-gpt"},
		{name: "Claude", vendor: "anthropic", authMode: "api_key", protocolFamily: "anthropic_messages", publicModel: "public-claude", providerModel: "wire-claude"},
		{name: "Gemini", vendor: "gemini", authMode: "aistudio_api_key", protocolFamily: "gemini_messages", publicModel: "public-gemini", providerModel: "wire-gemini"},
		{name: "Kimi", vendor: "kimi", authMode: "api_key", protocolFamily: "kimi_chat", publicModel: "public-kimi", providerModel: "wire-kimi"},
		{name: "Grok", vendor: "grok", authMode: "api_key", protocolFamily: "grok_chat", publicModel: "public-grok", providerModel: "wire-grok"},
	}

	for index := range families {
		item := &families[index]
		if item.protocolFamily == "openai_chat" {
			item.primaryID = seed.providerAccountID
			if _, err := pgPool.Exec(ctx, `
UPDATE provider_accounts
SET priority=10, model_allow_list=$1, capability_flags=$2
WHERE tenant_id=$3 AND id=$4`,
				[]string{item.providerModel}, []string{"stream"}, seed.tenantID, item.primaryID,
			); err != nil {
				t.Fatalf("更新 %s 主账号: %v", item.name, err)
			}
			item.backupID = insertMixedProviderAccount(t, ctx, pgPool, seed, seed.providerID, seed.channelID, *item, 20)
			continue
		}

		var providerID, channelID int64
		suffix := uuid.NewString()
		if err := pgPool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,$4) RETURNING id`,
			seed.tenantID, fmt.Sprintf("mixed-%s-%s", item.vendor, suffix), item.name+" mixed", item.protocolFamily,
		).Scan(&providerID); err != nil {
			t.Fatalf("插入 %s provider: %v", item.name, err)
		}
		if err := pgPool.QueryRow(ctx, `
INSERT INTO channels (tenant_id, pool_group_id, name)
VALUES ($1,$2,$3) RETURNING id`,
			seed.tenantID, seed.poolGroupID, fmt.Sprintf("mixed-%s-%s", item.vendor, suffix),
		).Scan(&channelID); err != nil {
			t.Fatalf("插入 %s channel: %v", item.name, err)
		}
		item.primaryID = insertMixedProviderAccount(t, ctx, pgPool, seed, providerID, channelID, *item, 10)
		item.backupID = insertMixedProviderAccount(t, ctx, pgPool, seed, providerID, channelID, *item, 20)
	}

	selector := NewDefaultSelector(NewDBAccountSource(dbbilling.New(pgPool)))
	for _, item := range families {
		item := item
		t.Run(item.name, func(t *testing.T) {
			request := SelectionRequest{
				TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
				PoolGroupID: seed.poolGroupID, RequestedModel: item.publicModel,
				ProviderModelID: item.providerModel, ProtocolFamily: item.protocolFamily,
				CapabilityFlags: []string{"stream"}, RequestID: "mixed-" + uuid.NewString(),
			}
			selected, err := selector.Select(ctx, request)
			if err != nil {
				t.Fatalf("选择主账号: %v", err)
			}
			if selected.AccountID != item.primaryID {
				t.Fatalf("主账号=%d，期望同协议账号 %d", selected.AccountID, item.primaryID)
			}

			request.ExcludedAccounts = map[int64]struct{}{item.primaryID: {}}
			selected, err = selector.Select(ctx, request)
			if err != nil {
				t.Fatalf("选择备用账号: %v", err)
			}
			if selected.AccountID != item.backupID {
				t.Fatalf("备用账号=%d，期望同协议账号 %d", selected.AccountID, item.backupID)
			}

			request.ExcludedAccounts[item.backupID] = struct{}{}
			selected, err = selector.Select(ctx, request)
			if !errors.Is(err, ErrNoEligibleAccount) || selected != nil {
				t.Fatalf("同协议账号耗尽后必须失败，selected=%+v err=%v", selected, err)
			}
		})
	}

	capabilityCases := []struct {
		familyIndex int
		capability  string
	}{
		{familyIndex: 0, capability: "embeddings"},
		{familyIndex: 3, capability: "rerank"},
		{familyIndex: 2, capability: "countTokens"},
	}
	for _, capabilityCase := range capabilityCases {
		item := families[capabilityCase.familyIndex]
		if _, err := pgPool.Exec(ctx, `
UPDATE provider_accounts
SET capability_flags=$1
WHERE tenant_id=$2 AND id=$3`,
			[]string{"stream", capabilityCase.capability}, seed.tenantID, item.primaryID,
		); err != nil {
			t.Fatalf("更新 %s %s 能力: %v", item.name, capabilityCase.capability, err)
		}

		t.Run(item.name+"-"+capabilityCase.capability, func(t *testing.T) {
			request := SelectionRequest{
				TenantID: seed.tenantID, UserID: seed.userID, APIKeyID: seed.apiKeyID,
				PoolGroupID: seed.poolGroupID, RequestedModel: item.publicModel,
				ProviderModelID: item.providerModel, ProtocolFamily: item.protocolFamily,
				CapabilityFlags: []string{capabilityCase.capability}, RequestID: "capability-" + uuid.NewString(),
			}
			selected, err := selector.Select(ctx, request)
			if err != nil {
				t.Fatalf("选择具备 %s 的账号: %v", capabilityCase.capability, err)
			}
			if selected.AccountID != item.primaryID {
				t.Fatalf("账号=%d，期望具备 %s 的账号 %d", selected.AccountID, capabilityCase.capability, item.primaryID)
			}

			request.ExcludedAccounts = map[int64]struct{}{item.primaryID: {}}
			selected, err = selector.Select(ctx, request)
			if !errors.Is(err, ErrNoEligibleAccount) || selected != nil {
				t.Fatalf("缺少 %s 的同协议备用账号不得被选中，selected=%+v err=%v", capabilityCase.capability, selected, err)
			}
		})
	}
}

func insertMixedProviderAccount(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *adapterSeed,
	providerID, channelID int64,
	item mixedProviderFamilyCase,
	priority int32,
) int64 {
	t.Helper()
	var accountID int64
	if err := pgPool.QueryRow(ctx, `
INSERT INTO provider_accounts (
    tenant_id, provider_id, channel_id, name, account_type,
    cap_concurrency, in_flight_count, priority, model_allow_list, capability_flags
) VALUES ($1,$2,$3,$4,'api_key',4,0,$5,$6,$7) RETURNING id`,
		seed.tenantID, providerID, channelID,
		fmt.Sprintf("mixed-%s-%d-%s", item.vendor, priority, uuid.NewString()),
		priority, []string{item.providerModel}, []string{"stream"},
	).Scan(&accountID); err != nil {
		t.Fatalf("插入 %s priority=%d 账号: %v", item.name, priority, err)
	}
	seedAdapterCredential(t, ctx, pgPool, seed.tenantID, accountID, item.vendor, item.authMode)
	return accountID
}
