package accountmodeldiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func (s *Service) Sync(ctx context.Context, in SyncInput) (_ SyncResult, retErr error) {
	if s == nil || s.pool == nil {
		return SyncResult{}, &DiscoveryError{Kind: ErrorNotConfigured}
	}
	discovered, err := s.Discover(ctx, in.TenantID, in.AccountID)
	if err != nil {
		return SyncResult{}, err
	}
	// 发现成功后的持久化失败也要能按账号族辨识。
	defer func() {
		if retErr != nil {
			annotate(retErr, discovered.Vendor, discovered.AuthMode)
		}
	}()
	if discovered.AccountCredentialID <= 0 || discovered.CredentialVersion <= 0 {
		return SyncResult{}, &DiscoveryError{Kind: ErrorUnsupported, Err: errors.New("旧式明文凭据不能持久化账号级目录")}
	}

	modelIDs := discovered.ModelIDs()
	result := SyncResult{Result: discovered}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var previous []string
		if err := tx.QueryRow(ctx, `
SELECT model_allow_list
FROM provider_accounts
WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL
FOR UPDATE`, in.TenantID, in.AccountID).Scan(&previous); err != nil {
			return err
		}
		// 锁住发现时使用的凭据版本直到账号白名单提交。普通 EXISTS 检查与后续
		// UPDATE 之间仍可发生轮换，会把旧凭据看到的目录写回新凭据账号。
		var credentialID int64
		if err := tx.QueryRow(ctx, `
SELECT id
FROM account_credentials
WHERE id=$1
  AND tenant_id=$2
  AND provider_account_id=$3
  AND credential_version=$4
  AND deleted_at IS NULL
  AND (state='active' OR (state='refreshing_with_grace' AND (grace_until IS NULL OR grace_until>NOW())))
FOR SHARE`, discovered.AccountCredentialID, in.TenantID, in.AccountID, discovered.CredentialVersion).Scan(&credentialID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &DiscoveryError{Kind: ErrorCredentialChanged}
			}
			return err
		}

		previous = normalizedIDs(previous)
		result.PreviousCount = len(previous)
		changed := !equalStrings(previous, modelIDs)
		outcome := "unchanged"
		if changed {
			outcome = "updated"
		}
		actorID := strings.TrimSpace(in.ActorID)
		q := admindb.New(tx)
		if changed {
			if _, err := q.UpdateAdminProviderAccountRaw(ctx, admindb.UpdateAdminProviderAccountRawParams{
				SetModelAllowList: true, ModelAllowList: modelIDs, ActorID: optional(actorID),
				ID: in.AccountID, TenantID: in.TenantID,
			}); err != nil {
				return err
			}
		}
		payload, err := json.Marshal(map[string]any{
			"operation": "sync_account_models", "reason": strings.TrimSpace(in.Reason),
			"result": outcome,
			"protocol_family": discovered.ProtocolFamily, "vendor": discovered.Vendor,
			"auth_mode": discovered.AuthMode, "credential_id": discovered.AccountCredentialID,
			"credential_version": discovered.CredentialVersion,
			"before_count":       len(previous), "after_count": len(modelIDs), "models": modelIDs,
		})
		if err != nil {
			return err
		}
		actorRole := strings.TrimSpace(in.ActorRole)
		if actorRole == "" {
			actorRole = admin.RoleTenantOperator
		}
		targetID := in.AccountID
		if _, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &in.TenantID, ActorID: actorID, ActorRole: actorRole,
			Action: "update_provider_account", TargetType: "provider_account", TargetID: &targetID,
			RequestID: optional(strings.TrimSpace(in.RequestID)), Payload: payload,
		}); err != nil {
			return err
		}
		result.Changed = changed
		return nil
	})
	if err != nil {
		var discoveryErr *DiscoveryError
		if errors.As(err, &discoveryErr) {
			return SyncResult{}, err
		}
		return SyncResult{}, &DiscoveryError{Kind: ErrorPersistence, Err: err}
	}
	return result, nil
}

func normalizedIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
