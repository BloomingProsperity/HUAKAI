package hermesops

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
)

// CredentialDiagnoseDeps 是 credential_diagnose 工具包装的只读依赖。每一项都是已有的网关读取;
// 工具自身不重新实现任何逻辑。
//
//   - DryRun 包装 credentialworker.DryRunProviderAccountCredential —— 一次
//     非持久化的凭证校验(它会显式清零明文,且从不调用 SaveRefreshSuccess/SaveRefreshFailure)。
//   - RenewStore 包装 credentialstore.Store.ListRenewStatus —— 对凭证续期元数据
//     (状态、失败分类、计数)的 SELECT-only 读取。
type CredentialDiagnoseDeps struct {
	// DryRun 以注入方式提供(而非具体函数),这样未接线时工具会 fail-closed,
	// 且可用 fake 做单元测试。
	DryRun func(ctx context.Context, store credentialworker.ProviderAccountCredentialTestStore, registry *credentialworker.ModeAdapterRegistry, tenantID, accountID int64, now time.Time) (credentialworker.ProviderAccountCredentialTestResult, error)
	// TestStore 是 dry-run 读取所用的凭证测试 store。
	TestStore credentialworker.ProviderAccountCredentialTestStore
	// Registry 是 mode-adapter registry;底层函数容忍 nil(会回退到默认值),
	// 因此 nil 不算接线失败。
	Registry *credentialworker.ModeAdapterRegistry
	// RenewStatus 包装 SELECT-only 的续期状态读取。
	RenewStatus func(ctx context.Context, params credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error)
}

// CredentialDiagnoseSpec 构建只读 credential_diagnose 工具。它在不持久化任何东西的前提下
// (dry-run)校验某个 provider account 已存储的凭证,并把该租户的 SELECT-only 续期状态合并进来。
//
// 入参: { "account_id": <int> }  (必填)
//
// 结果 summary(仅系统诊断):dry-run 的 ok/error_class,以及指定 account 的续期 state /
// 失败分类 / 失败计数 —— 不含任何密钥、凭证字节、refresh token。
func CredentialDiagnoseSpec(deps CredentialDiagnoseDeps) ToolSpec {
	return ToolSpec{
		Name:         ToolCredentialDiagnose,
		Category:     CategoryDiagnostic,
		Description:  "Validate a provider account's stored credential (non-persistent dry-run) and report its renew status.",
		ReadOnly:     true,
		RequiredRole: RoleTenantOperator,
		InputSchema:  map[string]string{"account_id": "provider account id (positive integer, required)"},
		Run: func(ctx context.Context, req ToolRequest) (ToolResult, error) {
			if deps.DryRun == nil || deps.TestStore == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return ToolResult{}, err
			}

			dr, err := deps.DryRun(ctx, deps.TestStore, deps.Registry, req.TenantID, accountID, time.Time{})
			if err != nil {
				return ToolResult{}, err
			}

			summary := map[string]any{
				"account_id":             accountID,
				"credential_ok":          dr.OK,
				"credential_error_class": emptyToNil(dr.ErrorClass),
				// dr.Message 是按 error class 取的固定运营指引字符串(不含用户数据),
				// 因此可以安全地暴露出去。
				"credential_detail": dr.Message,
			}

			errorClass := ""
			if !dr.OK {
				errorClass = dr.ErrorClass
			}

			// 可选:当 SELECT-only 读取已接线时,把指定 account 的续期状态合并进来。
			// 缺失续期依赖不是致命错误 —— dry-run 才是主要诊断。
			if deps.RenewStatus != nil {
				tenant := req.TenantID
				rows, rerr := deps.RenewStatus(ctx, credentialstore.ListRenewStatusParams{
					TenantID: &tenant,
					Limit:    200,
				})
				if rerr != nil {
					summary["renew_status_error"] = "renew_status_read_failed"
				} else {
					summary["renew_status"] = renewStatusForAccount(rows, accountID)
				}
			}

			return ToolResult{Summary: summary, ErrorClass: errorClass}, nil
		},
	}
}

// renewStatusForAccount 把某个 account 的续期状态行投影成仅诊断用的结构
// (状态 / 分类 / 计数 / ids)。它丢弃每一个非严格诊断用的自由文本 / 身份字段。
// 当该 account 没有匹配的凭证行时返回 nil。
func renewStatusForAccount(rows []credentialstore.RenewStatusMetadata, accountID int64) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r.AccountID != accountID {
			continue
		}
		out = append(out, map[string]any{
			"credential_id":        r.CredentialID,
			"vendor":               r.Vendor,
			"auth_mode":            r.AuthMode,
			"state":                r.State,
			"credential_version":   r.CredentialVersion,
			"access_expires_at":    timePtrAny(r.AccessExpiresAt),
			"refresh_before_at":    timePtrAny(r.RefreshBeforeAt),
			"last_refresh_at":      timePtrAny(r.LastRefreshAt),
			"last_refresh_outcome": deref(r.LastRefreshOutcome),
			"failure_class":        deref(r.FailureClass),
			"failure_count":        r.FailureCount,
		})
	}
	return out
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// timePtrAny 为诊断投影对 *time.Time 做 nil 保护:nil 保持 nil,
// 否则把该时刻归一化到 UTC。这些是凭证时序时间戳
// (到期 / 刷新截止 / 上次刷新)—— 绝不是密钥材料。
func timePtrAny(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
