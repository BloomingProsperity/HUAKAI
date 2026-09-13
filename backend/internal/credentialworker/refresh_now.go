package credentialworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// RefreshNowStatus 是运营"立刻刷新"一个账号的类型化结论。
type RefreshNowStatus string

const (
	// RefreshNowRefreshed 三个 scope 放行且刷新成功(新凭据版本已落库)。
	RefreshNowRefreshed RefreshNowStatus = "refreshed"
	// RefreshNowInProgress 账号刷新槽已被另一副本 / 后台调度持有:不重复打上游,稍后可查结果。
	RefreshNowInProgress RefreshNowStatus = "in_progress"
	// RefreshNowDeferred 被风暴预算(账号槽 / 厂商端点 / 全局)推迟:后台调度会在预算恢复后补齐。
	RefreshNowDeferred RefreshNowStatus = "deferred"
	// RefreshNowNotApplicable 账号当前不具备刷新资格(禁用、已撤销、冷却未到期)或没有可刷新的凭据模式。
	RefreshNowNotApplicable RefreshNowStatus = "not_applicable"
	// RefreshNowNotFound 账号不存在于该租户(或已删除)。
	RefreshNowNotFound RefreshNowStatus = "not_found"
	// RefreshNowFailed 刷新已执行但失败(失败分类见 Detail;健康链已按分类处理)。
	RefreshNowFailed RefreshNowStatus = "failed"
)

// RefreshNowOutcome 是立刻刷新的结果:Scope 指出在哪一层得出结论,Detail 是刷新审计的结果码或失败分类。
type RefreshNowOutcome struct {
	Status RefreshNowStatus
	Scope  string
	Detail string
}

// refreshNowQueries 是按账号取刷新资格行的查询面。
type refreshNowQueries interface {
	GetAccountForRefreshByID(ctx context.Context, arg dbbilling.GetAccountForRefreshByIDParams) (dbbilling.GetAccountForRefreshByIDRow, error)
}

// RefreshAccountNow 同步对单个账号执行一次凭据刷新,与后台调度共用同一条准入链(账号槽互斥、厂商端点与
// 全局风暴预算、刷新审计),因此运营入口不可能绕过后台的互斥与预算。结论以类型化结果返回;只有基础设施
// 错误(查询失败、准入器错误)才作为 error 返回,刷新本身的失败进入 RefreshNowFailed。
func (s *Scheduler) RefreshAccountNow(ctx context.Context, tenantID, accountID int64) (RefreshNowOutcome, error) {
	if s == nil || s.acquirer == nil || s.Refresher == nil {
		return RefreshNowOutcome{}, errors.New("credentialworker: refresh-now scheduler not configured")
	}
	if tenantID <= 0 || accountID <= 0 {
		return RefreshNowOutcome{}, errors.New("credentialworker: refresh-now input invalid")
	}
	// 与后台调度同一查询面:生产为 sqlc Queries;测试可注入实现了按账号取行的替身。
	queries, ok := s.queryer.(refreshNowQueries)
	if !ok {
		if s.Queries == nil {
			return RefreshNowOutcome{}, errors.New("credentialworker: refresh-now queries not configured")
		}
		queries = s.Queries
	}
	row, err := queries.GetAccountForRefreshByID(ctx, dbbilling.GetAccountForRefreshByIDParams{ID: accountID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshNowOutcome{Status: RefreshNowNotFound}, nil
	}
	if err != nil {
		return RefreshNowOutcome{}, fmt.Errorf("credentialworker: load account for refresh-now: %w", err)
	}
	if !row.Refreshable {
		return RefreshNowOutcome{Status: RefreshNowNotApplicable, Scope: "eligibility", Detail: row.HealthState}, nil
	}
	if inspector, ok := s.Refresher.(refreshModeInspector); ok {
		refreshable, err := inspector.HasRefreshSemantics(ctx, tenantID, accountID)
		if err != nil {
			return RefreshNowOutcome{}, fmt.Errorf("credentialworker: inspect refresh mode: %w", err)
		}
		if !refreshable {
			return RefreshNowOutcome{Status: RefreshNowNotApplicable, Scope: "credential_mode", Detail: "static_credential"}, nil
		}
	}
	account := dbbilling.ListAccountsForRefreshRow{
		ID: row.ID, TenantID: row.TenantID, ProviderID: row.ProviderID, VendorName: row.VendorName, ExpiresAt: row.ExpiresAt,
	}
	outcome, err := s.admitAndRefresh(ctx, account)
	if err != nil && outcome.Status == RefreshNowFailed {
		// 刷新失败是业务结论,不是基础设施错误;错误文本进入 Detail 供运营阅读,原始 error 不上抛。
		if outcome.Detail == "" {
			outcome.Detail = err.Error()
		}
		return outcome, nil
	}
	if err != nil {
		// 准入推迟时 recordAudit 的失败才会走到这里:结论仍有效,但审计未落库要让调用方知道。
		return outcome, err
	}
	return outcome, nil
}

// refreshModeInspector 由能判断"该账号凭据模式是否有刷新语义"的刷新器实现;立刻刷新在消耗任何风暴预算之前
// 用它把静态凭据(如 API Key)直接判为不适用。
type refreshModeInspector interface {
	HasRefreshSemantics(ctx context.Context, tenantID, accountID int64) (bool, error)
}

type refreshModeMetaStore interface {
	InspectRefreshMode(ctx context.Context, tenantID, accountID int64) (vendor, authMode string, err error)
}

// HasRefreshSemantics 报告账号当前凭据的模式适配器是否会真正刷新:静态模式(API Key、Setup Token、Bedrock 等)
// 没有刷新材料,返回 false;没有适配器的模式、无凭据或他租户也返回 false(不得当基础设施错误)。
// 只读 vendor/auth_mode,不解密、不走到期扫描谓词。
func (r *AccountCredentialRefresher) HasRefreshSemantics(ctx context.Context, tenantID, accountID int64) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("credentialworker: account credential store missing")
	}
	if tenantID <= 0 || accountID <= 0 {
		return false, errors.New("credentialworker: inspect refresh mode input invalid")
	}
	meta, ok := r.store.(refreshModeMetaStore)
	if !ok {
		return false, errors.New("credentialworker: refresh mode metadata store missing")
	}
	vendor, authMode, err := meta.InspectRefreshMode(ctx, tenantID, accountID)
	if errors.Is(err, credentialstore.ErrCredentialNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	adapter, found := r.registry.Lookup(vendor, authMode)
	if !found {
		return false, nil
	}
	_, static := adapter.(staticModeAdapter)
	return !static, nil
}
