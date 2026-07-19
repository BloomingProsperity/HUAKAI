package dispatcher

import (
	"context"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// AccountRepository 是账号选择的 Phase 4.5 持久化接缝。
type AccountRepository interface {
	ListEligibleAccounts(ctx context.Context, arg dbbilling.ListEligibleAccountsParams) ([]dbbilling.ListEligibleAccountsRow, error)
	GetAccountForRevalidation(ctx context.Context, arg dbbilling.GetAccountForRevalidationParams) (dbbilling.GetAccountForRevalidationRow, error)
	IncrementInFlightCount(ctx context.Context, arg dbbilling.IncrementInFlightCountParams) (int64, error)
	DecrementInFlightCount(ctx context.Context, id int64) error
	GetModelRoutingForGroup(ctx context.Context, arg dbbilling.GetModelRoutingForGroupParams) ([]dbbilling.GetModelRoutingForGroupRow, error)
}

// SlotAcquisitionRepository 持久化已获取的 slot，以支持幂等释放。
type SlotAcquisitionRepository interface {
	InsertSlotAcquisition(ctx context.Context, arg dbbilling.InsertSlotAcquisitionParams) (int64, error)
	ReleaseSlotAcquisition(ctx context.Context, arg dbbilling.ReleaseSlotAcquisitionParams) error
	ListOrphanedAcquisitions(ctx context.Context) ([]dbbilling.ListOrphanedAcquisitionsRow, error)
}

// StickyBindingRepository 存储 session 到账号的亲和性(affinity)。
type StickyBindingRepository interface {
	GetStickyBinding(ctx context.Context, arg dbbilling.GetStickyBindingParams) (int64, error)
	UpsertStickyBinding(ctx context.Context, arg dbbilling.UpsertStickyBindingParams) error
	DeleteExpiredStickyBindings(ctx context.Context) error
}

// AuditRepository 写入 pool routing 审计事件。
type AuditRepository interface {
	InsertPoolRoutingAuditEvent(ctx context.Context, arg dbbilling.InsertPoolRoutingAuditEventParams) error
}

type DBRepository struct {
	q *dbbilling.Queries
}

func NewDBRepository(q *dbbilling.Queries) *DBRepository {
	return &DBRepository{q: q}
}

func (r *DBRepository) ListEligibleAccounts(ctx context.Context, arg dbbilling.ListEligibleAccountsParams) ([]dbbilling.ListEligibleAccountsRow, error) {
	return r.q.ListEligibleAccounts(ctx, arg)
}

func (r *DBRepository) GetAccountForRevalidation(ctx context.Context, arg dbbilling.GetAccountForRevalidationParams) (dbbilling.GetAccountForRevalidationRow, error) {
	return r.q.GetAccountForRevalidation(ctx, arg)
}

func (r *DBRepository) IncrementInFlightCount(ctx context.Context, arg dbbilling.IncrementInFlightCountParams) (int64, error) {
	return r.q.IncrementInFlightCount(ctx, arg)
}

func (r *DBRepository) DecrementInFlightCount(ctx context.Context, id int64) error {
	return r.q.DecrementInFlightCount(ctx, id)
}

func (r *DBRepository) GetModelRoutingForGroup(ctx context.Context, arg dbbilling.GetModelRoutingForGroupParams) ([]dbbilling.GetModelRoutingForGroupRow, error) {
	return r.q.GetModelRoutingForGroup(ctx, arg)
}

func (r *DBRepository) InsertSlotAcquisition(ctx context.Context, arg dbbilling.InsertSlotAcquisitionParams) (int64, error) {
	return r.q.InsertSlotAcquisition(ctx, arg)
}

func (r *DBRepository) ReleaseSlotAcquisition(ctx context.Context, arg dbbilling.ReleaseSlotAcquisitionParams) error {
	return r.q.ReleaseSlotAcquisition(ctx, arg)
}

func (r *DBRepository) ListOrphanedAcquisitions(ctx context.Context) ([]dbbilling.ListOrphanedAcquisitionsRow, error) {
	return r.q.ListOrphanedAcquisitions(ctx)
}

func (r *DBRepository) GetStickyBinding(ctx context.Context, arg dbbilling.GetStickyBindingParams) (int64, error) {
	return r.q.GetStickyBinding(ctx, arg)
}

func (r *DBRepository) UpsertStickyBinding(ctx context.Context, arg dbbilling.UpsertStickyBindingParams) error {
	return r.q.UpsertStickyBinding(ctx, arg)
}

func (r *DBRepository) DeleteExpiredStickyBindings(ctx context.Context) error {
	return r.q.DeleteExpiredStickyBindings(ctx)
}

func (r *DBRepository) InsertPoolRoutingAuditEvent(ctx context.Context, arg dbbilling.InsertPoolRoutingAuditEventParams) error {
	return r.q.InsertPoolRoutingAuditEvent(ctx, arg)
}

var _ AccountRepository = (*DBRepository)(nil)
var _ SlotAcquisitionRepository = (*DBRepository)(nil)
var _ StickyBindingRepository = (*DBRepository)(nil)
var _ AuditRepository = (*DBRepository)(nil)
