// Package accountcreate 负责管理端账号创建时必须原子完成的协议兼容性与风险检查。
package accountcreate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

var (
	ErrPoolUnset                = errors.New("gatewayhttp: admin pool account adapter pgxpool unset")
	ErrMixedRiskConfirmRequired = errors.New("provider account mixed channel risk confirmation required")
	ErrProtocolIncompatible     = errors.New("provider account protocol and credential are incompatible")
)

type Params struct {
	Insert         admindb.InsertProviderAccountParams
	Candidate      mixedchannelrisk.Account
	ProviderFamily string
	Confirmed      bool
}

type Result struct {
	ID         int64
	RiskReport mixedchannelrisk.Report
}

// ValidateProtocolCompatibility 校验 account 的 vendor/auth_mode/runtime 与 provider
// family 契约一致(G1):防特权误配把 A 厂 key 绑到 B 厂 family,导致错投密钥、错误
// transport/health 标签、计价归因分裂。无契约的 family 保守跳过(不误拒 R0 未覆盖族)。
func ValidateProtocolCompatibility(family, accountType, vendor, authMode string) error {
	// session 族额外硬约束:account_type 必须 oauth/session(非 api_key)。
	if family == registrydefault.ProtocolAnthropicClaudeSession {
		if accountType != "oauth" && accountType != "session" {
			return fmt.Errorf("%w: Claude session provider requires oauth/session account_type", ErrProtocolIncompatible)
		}
	}
	if !servingcapability.HasContract(family) {
		return nil
	}
	// account-first 模式:裸账号先建、凭据后加,create 期无 vendor/auth。此时无从校验,
	// 推迟到凭据创建期(credential-create 守卫)兜住 account-first 的错配。credential-first
	// (create 期已带 vendor/auth)在此 fail-fast。
	if strings.TrimSpace(vendor) == "" && strings.TrimSpace(authMode) == "" {
		return nil
	}
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(vendor, authMode)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProtocolIncompatible, err)
	}
	if err := servingcapability.ValidateAccountCompatibility(family, vendor, authMode, handler.RuntimeKind()); err != nil {
		return fmt.Errorf("%w: %v", ErrProtocolIncompatible, err)
	}
	return nil
}

// CredentialCompatLookup 是 credential 创建守卫所需的最小账号/协议查询接口。
// *db/admin.Queries(经 AdminPoolAccountStore)天然满足。
type CredentialCompatLookup interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error)
}

// ValidateCredentialCompatibility 校验将写入 account 的新凭据 vendor/auth 与账号 provider
// family 兼容。账号或协议查询失败时必须阻止写入，避免数据库故障或错误租户让兼容性守卫
// 静默失效。
func ValidateCredentialCompatibility(ctx context.Context, lookup CredentialCompatLookup, tenantID, accountID int64, vendor, authMode string) error {
	if lookup == nil || tenantID <= 0 || accountID <= 0 {
		return errors.New("provider account credential compatibility lookup input invalid")
	}
	acct, err := lookup.GetAdminProviderAccount(ctx, admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("load provider account for credential compatibility: %w", err)
	}
	family, err := lookup.GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{TenantID: tenantID, ProviderID: acct.ProviderID})
	if err != nil {
		return fmt.Errorf("load provider protocol for credential compatibility: %w", err)
	}
	return ValidateProtocolCompatibility(family, acct.AccountType, vendor, authMode)
}

// Insert 在同一事务内锁定 provider 协议、串行化渠道风险检查并插入账号。
func Insert(ctx context.Context, pool *pgxpool.Pool, arg Params) (Result, error) {
	if pool == nil {
		return Result{}, ErrPoolUnset
	}
	var out Result
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		out, err = InsertTx(ctx, tx, arg)
		return err
	})
	if err != nil {
		return Result{RiskReport: out.RiskReport}, err
	}
	return out, nil
}

// InsertTx 在调用方事务内完成协议复核、渠道风险串行化和账号插入。
func InsertTx(ctx context.Context, tx pgx.Tx, arg Params) (Result, error) {
	if tx == nil {
		return Result{}, ErrPoolUnset
	}
	q := admindb.New(tx)
	family, err := q.GetProviderProtocolForAccountCreate(ctx, admindb.GetProviderProtocolForAccountCreateParams{
		TenantID: arg.Insert.TenantID, ProviderID: arg.Insert.ProviderID,
	})
	if err != nil {
		return Result{}, err
	}
	if family != arg.ProviderFamily {
		return Result{}, fmt.Errorf("%w: provider protocol changed during create", ErrProtocolIncompatible)
	}
	if err := ValidateProtocolCompatibility(family, arg.Candidate.AccountType, arg.Candidate.Vendor, arg.Candidate.AuthMode); err != nil {
		return Result{}, err
	}

	// 同一 tenant/channel 的检查与写入必须串行，避免两个空渠道并发绕过风险门。
	lockKey := fmt.Sprintf("provider-account-mixed-risk:%d:%d", arg.Insert.TenantID, arg.Insert.ChannelID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return Result{}, err
	}
	peers, err := q.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{
		TenantID: arg.Insert.TenantID, ChannelID: arg.Insert.ChannelID,
	})
	if err != nil {
		return Result{}, err
	}
	report := mixedchannelrisk.Evaluate(arg.Candidate, peerAccounts(peers))
	if report.HighRisk && !arg.Confirmed {
		return Result{RiskReport: report}, ErrMixedRiskConfirmRequired
	}
	id, err := q.InsertProviderAccount(ctx, arg.Insert)
	if err != nil {
		return Result{RiskReport: report}, err
	}
	return Result{ID: id, RiskReport: report}, nil
}

func peerAccounts(rows []admindb.ProviderAccountRiskPeerRow) []mixedchannelrisk.Account {
	out := make([]mixedchannelrisk.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedchannelrisk.Account{
			ID: row.ID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
			AccountType: row.AccountType, Vendor: row.CredentialVendor, AuthMode: row.CredentialAuthMode,
		})
	}
	return out
}
