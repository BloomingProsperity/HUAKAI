package dispatcher

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// AccountRepository is the Phase 4.5 persistence seam for account selection.
type AccountRepository interface {
	ListEligibleAccounts(ctx context.Context, arg db.ListEligibleAccountsParams) ([]db.ListEligibleAccountsRow, error)
	GetAccountForRevalidation(ctx context.Context, arg db.GetAccountForRevalidationParams) (db.GetAccountForRevalidationRow, error)
	IncrementInFlightCount(ctx context.Context, arg db.IncrementInFlightCountParams) (int64, error)
	DecrementInFlightCount(ctx context.Context, id int64) error
	GetModelRoutingForGroup(ctx context.Context, arg db.GetModelRoutingForGroupParams) ([]db.GetModelRoutingForGroupRow, error)
}

// SlotAcquisitionRepository persists acquired slots for idempotent release.
type SlotAcquisitionRepository interface {
	InsertSlotAcquisition(ctx context.Context, arg db.InsertSlotAcquisitionParams) (int64, error)
	ReleaseSlotAcquisition(ctx context.Context, arg db.ReleaseSlotAcquisitionParams) error
	ListOrphanedAcquisitions(ctx context.Context) ([]db.PoolSlotAcquisition, error)
}

// StickyBindingRepository stores session-to-account affinity.
type StickyBindingRepository interface {
	GetStickyBinding(ctx context.Context, arg db.GetStickyBindingParams) (int64, error)
	UpsertStickyBinding(ctx context.Context, arg db.UpsertStickyBindingParams) error
	DeleteExpiredStickyBindings(ctx context.Context) error
}

// AuditRepository writes pool routing audit events.
type AuditRepository interface {
	InsertPoolRoutingAuditEvent(ctx context.Context, arg db.InsertPoolRoutingAuditEventParams) error
}

type DBRepository struct {
	q *db.Queries
}

func NewDBRepository(q *db.Queries) *DBRepository {
	return &DBRepository{q: q}
}

func (r *DBRepository) ListEligibleAccounts(ctx context.Context, arg db.ListEligibleAccountsParams) ([]db.ListEligibleAccountsRow, error) {
	return r.q.ListEligibleAccounts(ctx, arg)
}

func (r *DBRepository) GetAccountForRevalidation(ctx context.Context, arg db.GetAccountForRevalidationParams) (db.GetAccountForRevalidationRow, error) {
	return r.q.GetAccountForRevalidation(ctx, arg)
}

func (r *DBRepository) IncrementInFlightCount(ctx context.Context, arg db.IncrementInFlightCountParams) (int64, error) {
	return r.q.IncrementInFlightCount(ctx, arg)
}

func (r *DBRepository) DecrementInFlightCount(ctx context.Context, id int64) error {
	return r.q.DecrementInFlightCount(ctx, id)
}

func (r *DBRepository) GetModelRoutingForGroup(ctx context.Context, arg db.GetModelRoutingForGroupParams) ([]db.GetModelRoutingForGroupRow, error) {
	return r.q.GetModelRoutingForGroup(ctx, arg)
}

func (r *DBRepository) InsertSlotAcquisition(ctx context.Context, arg db.InsertSlotAcquisitionParams) (int64, error) {
	return r.q.InsertSlotAcquisition(ctx, arg)
}

func (r *DBRepository) ReleaseSlotAcquisition(ctx context.Context, arg db.ReleaseSlotAcquisitionParams) error {
	return r.q.ReleaseSlotAcquisition(ctx, arg)
}

func (r *DBRepository) ListOrphanedAcquisitions(ctx context.Context) ([]db.PoolSlotAcquisition, error) {
	return r.q.ListOrphanedAcquisitions(ctx)
}

func (r *DBRepository) GetStickyBinding(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
	return r.q.GetStickyBinding(ctx, arg)
}

func (r *DBRepository) UpsertStickyBinding(ctx context.Context, arg db.UpsertStickyBindingParams) error {
	return r.q.UpsertStickyBinding(ctx, arg)
}

func (r *DBRepository) DeleteExpiredStickyBindings(ctx context.Context) error {
	return r.q.DeleteExpiredStickyBindings(ctx)
}

func (r *DBRepository) InsertPoolRoutingAuditEvent(ctx context.Context, arg db.InsertPoolRoutingAuditEventParams) error {
	return r.q.InsertPoolRoutingAuditEvent(ctx, arg)
}

var _ AccountRepository = (*DBRepository)(nil)
var _ SlotAcquisitionRepository = (*DBRepository)(nil)
var _ StickyBindingRepository = (*DBRepository)(nil)
var _ AuditRepository = (*DBRepository)(nil)
