package accountbulkhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/provideraccountrecovery"
)

// AdminAuth 从请求派生部署者或租户管理员身份。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// RecoveryService 是清限流 / 完整恢复的逐账号原语(与单账号接口同一实现)。
type RecoveryService interface {
	ClearRateLimit(context.Context, provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error)
	RecoverAccountState(context.Context, provideraccountrecovery.RecoverAccountInput) (provideraccountrecovery.RecoverAccountResult, error)
}

// RefreshService 是与后台调度共用准入链的立刻刷新原语。
type RefreshService interface {
	RefreshAccountNow(ctx context.Context, tenantID, accountID int64) (credentialworker.RefreshNowOutcome, error)
}

// Deps 是接口依赖。缺失的依赖只让对应动作返回 dependency_not_configured,不影响其他动作。
type Deps struct {
	Auth           AdminAuth
	Store          Store
	Recovery       RecoveryService
	Refresher      RefreshService
	MaxIDs         int
	MaxRefreshIDs  int
	RefreshWorkers int
	BatchTimeout   time.Duration
	Now            func() time.Time
}

func (d Deps) normalized() Deps {
	if d.MaxIDs <= 0 {
		d.MaxIDs = DefaultMaxIDs
	}
	if d.MaxRefreshIDs <= 0 {
		d.MaxRefreshIDs = DefaultMaxRefreshIDs
	}
	if d.RefreshWorkers <= 0 {
		d.RefreshWorkers = DefaultRefreshWorkers
	}
	if d.BatchTimeout <= 0 {
		d.BatchTimeout = DefaultBatchTimeout
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return d
}

// MountRoutes 注册 POST /bulk(挂在 /admin/v1/provider-accounts 下),写操作受会话写类保护。
func MountRoutes(r chi.Router, d Deps) {
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/bulk", NewHandler(d))
}

const maxBodyBytes = 64 << 10

// NewHandler 构造批量运维入口。
func NewHandler(d Deps) http.HandlerFunc {
	d = d.normalized()
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			adminhttpcore.WriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account bulk operations are not configured")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			adminhttpcore.WriteTenantScopeAuthError(w, err)
			return
		}
		if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
			adminhttpcore.WriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
			return
		}
		// 写操作:租户管理员锁本租户,部署者必须显式给出 tenant_id(不允许跨租户批量写)。
		tenantID, ok := adminhttpcore.ResolveTenantScopeQuery(w, r, ident)
		if !ok {
			return
		}
		var req Request
		dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			adminhttpcore.WriteJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be a JSON object with known fields")
			return
		}
		ids, duplicates, verr := validateRequest(req, d)
		if verr != nil {
			adminhttpcore.WriteJSONError(w, http.StatusBadRequest, verr.code, verr.message)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d.BatchTimeout)
		defer cancel()
		exec := executor{
			deps: d, tenantID: tenantID, req: req, ids: ids,
			actorID: ident.AuditActor(), actorRole: ident.Role, requestID: middleware.GetReqID(r.Context()),
		}
		resp := exec.run(ctx)
		resp.DuplicateIDsRemoved = duplicates
		if !req.DryRun {
			// 批次日志在逐项之后落库:记录的是真实计数;失败只记日志不影响已完成的逐项结果。
			auditID, err := d.Store.InsertBatchAudit(r.Context(), BatchAudit{
				TenantID: tenantID, ActorID: ident.AuditActor(), ActorRole: ident.Role, RequestID: exec.requestID, Reason: req.Reason,
				Action: req.Action, Summary: map[string]any{
					"selection": "ids", "total": resp.Total, "succeeded": resp.Succeeded, "skipped": resp.Skipped,
					"deferred": resp.Deferred, "failed": resp.Failed, "cancelled": resp.Cancelled,
					"succeeded_ids": resp.SucceededIDs, "failed_ids": resp.FailedIDs,
					"confirm_mixed_risk": req.ConfirmMixedRisk,
				},
			})
			if err == nil {
				resp.BatchAuditID = &auditID
			}
		}
		// 与 bulk-by-tag 同一口径:存在失败 / 推迟 / 取消项时用 207 明示部分完成,禁止纯成功语义。
		status := http.StatusOK
		if resp.Failed > 0 || resp.Deferred > 0 || resp.Cancelled > 0 {
			status = http.StatusMultiStatus
		}
		adminhttpcore.WriteJSON(w, status, resp)
	}
}

type validationError struct {
	code, message string
}

// validateRequest 规范化选中 ID(去重、去非正数)、校验动作与参数、施加单批上限。
func validateRequest(req Request, d Deps) ([]int64, int, *validationError) {
	if len(req.IDs) == 0 {
		return nil, 0, &validationError{"ids_required", "ids must contain at least one provider account id"}
	}
	seen := make(map[int64]bool, len(req.IDs))
	ids := make([]int64, 0, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 {
			return nil, 0, &validationError{"invalid_id", "ids must be positive int64 values"}
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	duplicates := len(req.IDs) - len(ids)
	limit := d.MaxIDs
	switch req.Action {
	case ActionSetEnabled:
		if req.Enabled == nil {
			return nil, 0, &validationError{"enabled_required", "set_enabled requires the enabled field"}
		}
	case ActionSetPriority:
		if req.Priority == nil {
			return nil, 0, &validationError{"priority_required", "set_priority requires the priority field"}
		}
	case ActionSetStaticWeight:
		if req.StaticWeight == nil || *req.StaticWeight < 0 {
			return nil, 0, &validationError{"static_weight_required", "set_static_weight requires a non-negative static_weight"}
		}
	case ActionMoveChannel:
		if req.ChannelID == nil || *req.ChannelID <= 0 {
			return nil, 0, &validationError{"channel_id_required", "move_channel requires a positive channel_id"}
		}
	case ActionClearRateLimit, ActionRecover:
	case ActionRefreshCredential:
		limit = d.MaxRefreshIDs
	default:
		return nil, 0, &validationError{"invalid_action", "action must be one of set_enabled, set_priority, set_static_weight, clear_rate_limit, recover, refresh_credential, move_channel"}
	}
	if len(ids) > limit {
		return nil, 0, &validationError{"bulk_target_limit_exceeded", "too many ids for this action in one request"}
	}
	if destructiveReasonRequired(req) && strings.TrimSpace(req.Reason) == "" {
		return nil, 0, &validationError{"reason_required", "this action requires a non-empty reason"}
	}
	if len(strings.TrimSpace(req.Reason)) > 512 {
		return nil, 0, &validationError{"reason_too_long", "reason must be at most 512 characters"}
	}
	return ids, duplicates, nil
}

func destructiveReasonRequired(req Request) bool {
	switch req.Action {
	case ActionClearRateLimit, ActionRecover:
		return true
	case ActionSetEnabled:
		return req.Enabled != nil && !*req.Enabled
	default:
		return false
	}
}

// executor 按动作逐项执行并聚合结果。
type executor struct {
	deps      Deps
	tenantID  int64
	req       Request
	ids       []int64
	actorID   string
	actorRole string
	requestID string
}

func (e executor) run(ctx context.Context) Response {
	results := make([]ItemResult, len(e.ids))
	if e.req.Action == ActionRefreshCredential {
		e.runParallel(ctx, results)
	} else {
		for i, id := range e.ids {
			if ctx.Err() != nil {
				results[i] = cancelled(id)
				continue
			}
			results[i] = e.runOne(ctx, id)
		}
	}
	return aggregate(e.req, e.tenantID, results)
}

// runParallel 用有界工人池执行刷新:批次超时后未开始的项标 cancelled,不再打上游。
func (e executor) runParallel(ctx context.Context, results []ItemResult) {
	workers := e.deps.RefreshWorkers
	if workers > len(e.ids) {
		workers = len(e.ids)
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					results[i] = cancelled(e.ids[i])
					continue
				}
				results[i] = e.runOne(ctx, e.ids[i])
			}
		}()
	}
	for i := range e.ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func (e executor) runOne(ctx context.Context, id int64) ItemResult {
	switch e.req.Action {
	case ActionSetEnabled, ActionSetPriority, ActionSetStaticWeight:
		res, err := e.deps.Store.ApplyFieldUpdate(ctx, FieldUpdate{
			TenantID: e.tenantID, AccountID: id, ActorID: e.actorID, ActorRole: e.actorRole, RequestID: e.requestID, Reason: e.req.Reason,
			Enabled: e.enabledArg(), Priority: e.priorityArg(), StaticWeight: e.weightArg(), DryRun: e.req.DryRun,
		})
		return storeOutcome(id, res, err)
	case ActionMoveChannel:
		res, err := e.deps.Store.MoveChannel(ctx, ChannelMove{
			TenantID: e.tenantID, AccountID: id, TargetChannelID: *e.req.ChannelID, ConfirmMixedRisk: e.req.ConfirmMixedRisk,
			ActorID: e.actorID, ActorRole: e.actorRole, RequestID: e.requestID, Reason: e.req.Reason, DryRun: e.req.DryRun,
		})
		return storeOutcome(id, res, err)
	case ActionClearRateLimit, ActionRecover:
		return e.runRecovery(ctx, id)
	case ActionRefreshCredential:
		return e.runRefresh(ctx, id)
	default:
		return ItemResult{ID: id, Status: StatusFailed, Code: "invalid_action"}
	}
}

func (e executor) enabledArg() *bool {
	if e.req.Action == ActionSetEnabled {
		return e.req.Enabled
	}
	return nil
}

func (e executor) priorityArg() *int32 {
	if e.req.Action == ActionSetPriority {
		return e.req.Priority
	}
	return nil
}

func (e executor) weightArg() *int32 {
	if e.req.Action == ActionSetStaticWeight {
		return e.req.StaticWeight
	}
	return nil
}

func (e executor) runRecovery(ctx context.Context, id int64) ItemResult {
	if e.deps.Recovery == nil {
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeDependencyNotConfigured, Message: "recovery service is not configured"}
	}
	if e.req.DryRun {
		// 恢复类动作是幂等的状态复位,dry-run 只回显将执行,不预判账号是否存在(避免为预览付一次真相读)。
		return ItemResult{ID: id, Status: StatusWouldApply}
	}
	in := provideraccountrecovery.ClearRateLimitInput{
		TenantID: e.tenantID, AccountID: id, ActorID: e.actorID, ActorRole: e.actorRole, RequestID: e.requestID, Reason: e.req.Reason,
	}
	var (
		result provideraccountrecovery.ClearRateLimitResult
		err    error
	)
	if e.req.Action == ActionClearRateLimit {
		result, err = e.deps.Recovery.ClearRateLimit(ctx, in)
	} else {
		result, err = e.deps.Recovery.RecoverAccountState(ctx, in)
	}
	switch {
	case err == nil:
		return ItemResult{ID: id, Status: StatusSucceeded, Detail: map[string]any{"channel_changed": result.ChannelChanged, "channel_record_found": result.Channel != nil}}
	case errors.Is(err, provideraccountrecovery.ErrPartialRecovery):
		return ItemResult{ID: id, Status: StatusFailed, Code: CodePartialRecovery, Message: "account state cleared but channel recovery failed; retry this account", Retryable: true}
	case errors.Is(err, pgx.ErrNoRows):
		// 恢复原语对不存在 / 他租户 / 已删除账号返回 no rows,与字段写入的 not_found 同码。
		return notFound(id)
	default:
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeUpstreamStoreError, Message: "recovery failed; retry this account", Retryable: true}
	}
}

func (e executor) runRefresh(ctx context.Context, id int64) ItemResult {
	if e.deps.Refresher == nil {
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeDependencyNotConfigured, Message: "credential refresh service is not configured"}
	}
	if e.req.DryRun {
		return ItemResult{ID: id, Status: StatusWouldApply}
	}
	itemCtx, cancel := context.WithTimeout(ctx, DefaultRefreshItemLimit)
	defer cancel()
	outcome, err := e.deps.Refresher.RefreshAccountNow(itemCtx, e.tenantID, id)
	if err != nil && outcome.Status == "" {
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeUpstreamStoreError, Message: "refresh could not be admitted; retry this account", Retryable: true}
	}
	detail := map[string]any{"scope": outcome.Scope, "outcome": outcome.Detail}
	switch outcome.Status {
	case credentialworker.RefreshNowRefreshed:
		return ItemResult{ID: id, Status: StatusSucceeded, Detail: detail}
	case credentialworker.RefreshNowInProgress:
		return ItemResult{ID: id, Status: StatusDeferred, Code: CodeRefreshInProgress, Message: "a refresh for this account is already running; check the credential status shortly", Retryable: true, Detail: detail}
	case credentialworker.RefreshNowDeferred:
		return ItemResult{ID: id, Status: StatusDeferred, Code: CodeRefreshDeferred, Message: "refresh deferred by the storm budget; the background scheduler will retry", Retryable: true, Detail: detail}
	case credentialworker.RefreshNowNotApplicable:
		return ItemResult{ID: id, Status: StatusSkipped, Code: CodeRefreshNotApplicable, Message: "account has no refreshable credential or is not eligible for refresh", Detail: detail}
	case credentialworker.RefreshNowNotFound:
		return notFound(id)
	default:
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeRefreshFailed, Message: "credential refresh failed; see detail.outcome", Retryable: false, Detail: detail}
	}
}

func storeOutcome(id int64, res ItemResult, err error) ItemResult {
	if err != nil {
		return ItemResult{ID: id, Status: StatusFailed, Code: CodeUpstreamStoreError, Message: "store operation failed; retry this account", Retryable: true}
	}
	if res.ID == 0 {
		res.ID = id
	}
	return res
}

func cancelled(id int64) ItemResult {
	return ItemResult{ID: id, Status: StatusCancelled, Code: CodeBatchTimeout, Message: "batch timed out before this account was processed; resubmit it", Retryable: true}
}

func aggregate(req Request, tenantID int64, results []ItemResult) Response {
	resp := Response{
		Action: req.Action, DryRun: req.DryRun, TenantID: tenantID, Total: len(results),
		SucceededIDs: []int64{}, FailedIDs: []int64{}, SucceededNotRolledBack: true, Results: results,
	}
	for _, r := range results {
		switch r.Status {
		case StatusSucceeded, StatusWouldApply:
			resp.Succeeded++
			resp.SucceededIDs = append(resp.SucceededIDs, r.ID)
		case StatusSkipped:
			resp.Skipped++
		case StatusDeferred:
			resp.Deferred++
		case StatusCancelled:
			resp.Cancelled++
			resp.FailedIDs = append(resp.FailedIDs, r.ID)
		default:
			resp.Failed++
			resp.FailedIDs = append(resp.FailedIDs, r.ID)
		}
	}
	sort.Slice(resp.SucceededIDs, func(i, j int) bool { return resp.SucceededIDs[i] < resp.SucceededIDs[j] })
	sort.Slice(resp.FailedIDs, func(i, j int) bool { return resp.FailedIDs[i] < resp.FailedIDs[j] })
	resp.PartialSuccess = resp.Succeeded > 0 && (resp.Failed > 0 || resp.Deferred > 0 || resp.Cancelled > 0)
	return resp
}
