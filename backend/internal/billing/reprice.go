package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const (
	RepriceMaxBatchLimit = 100

	RepriceStatusWouldApply      = "would_apply"
	RepriceStatusRepriced        = "repriced"
	RepriceStatusAlreadyRepriced = "already_repriced"
	RepriceStatusSkipped         = "skipped"
	RepriceStatusError           = "error"

	RepriceDefaultSource = "manual_reprice_current_pricing"
)

var ErrRepriceInvalidInput = errors.New("billing: invalid reprice input")

type RepricePricingRatioResolver interface {
	Resolve(context.Context, int64, int64) (decimal.Decimal, error)
}

type repricePricingRatioResolverWithSignal interface {
	ResolveWithSignal(context.Context, int64, int64) (decimal.Decimal, bool, error)
}

type RepriceService struct {
	pool                 *pgxpool.Pool
	RateTables           RateTableSource
	PricingRatioResolver RepricePricingRatioResolver
	BillingPolicyVersion string
	Now                  func() time.Time
}

type RepriceRequest struct {
	UsageRecordID int64
	TenantID      int64
	From          time.Time
	To            time.Time
	Limit         int
	DryRun        bool
	Source        string
	ReconciledAt  time.Time
}

type RepriceResult struct {
	DryRun  bool
	Items   []RepriceItem
	Summary RepriceSummary
}

type RepriceSummary struct {
	Total           int
	WouldApply      int
	Repriced        int
	AlreadyRepriced int
	Skipped         int
	Failed          int
}

type RepriceItem struct {
	UsageRecordID     int64
	TenantID          int64
	Status            string
	SkippedReason     string
	ErrorCode         string
	ErrorMessage      string
	OriginalCost      decimal.Decimal
	AuthoritativeCost decimal.Decimal
	CostDelta         decimal.Decimal
	PricingSource     string
}

type repriceUsageRecordRow struct {
	ID                     int64
	TenantID               int64
	ClaimID                int64
	PoolGroupID            int64
	ProviderCode           string
	ProtocolFamily         string
	TokensInput            int32
	TokensOutput           int32
	CacheCreationTokens    int32
	CacheReadTokens        int32
	CacheCreation5mTokens  int32
	CacheCreation1hTokens  int32
	ActualCost             decimal.Decimal
	PendingReconciliation  bool
	RequestedModel         string
	UpstreamModel          string
	ClaimRequestedModel    string
	HasReconciliationEvent bool
}

func NewPostgresRepriceService(pool *pgxpool.Pool, rateTables RateTableSource, ratioResolver RepricePricingRatioResolver, billingPolicyVersion string) *RepriceService {
	return &RepriceService{
		pool:                 pool,
		RateTables:           rateTables,
		PricingRatioResolver: ratioResolver,
		BillingPolicyVersion: strings.TrimSpace(billingPolicyVersion),
		Now:                  func() time.Time { return time.Now().UTC() },
	}
}

func (s *RepriceService) RepriceUsageRecords(ctx context.Context, req RepriceRequest) (RepriceResult, error) {
	if s == nil || s.pool == nil {
		return RepriceResult{}, ErrPoolNotConfigured
	}
	req, err := normalizeRepriceRequest(req)
	if err != nil {
		return RepriceResult{}, err
	}
	rows, err := s.loadRepriceRows(ctx, req)
	if err != nil {
		return RepriceResult{}, err
	}
	result := RepriceResult{DryRun: req.DryRun, Items: make([]RepriceItem, 0, len(rows))}
	var table RateTable
	tableLoaded := false
	for _, row := range rows {
		item := RepriceItem{
			UsageRecordID: row.ID,
			TenantID:      row.TenantID,
			OriginalCost:  normalizeRepriceMoney(row.ActualCost),
		}
		if row.HasReconciliationEvent {
			item.Status = RepriceStatusAlreadyRepriced
			result.addItem(item)
			continue
		}
		if !row.PendingReconciliation {
			item.Status = RepriceStatusSkipped
			item.SkippedReason = "already_reconciled"
			result.addItem(item)
			continue
		}
		if !tableLoaded {
			table, err = s.currentRateTable(ctx)
			if err != nil {
				item.Status = RepriceStatusError
				item.ErrorCode = "pricing_unavailable"
				item.ErrorMessage = err.Error()
				result.addItem(item)
				continue
			}
			tableLoaded = true
		}
		authoritative, source, err := s.authoritativeCost(ctx, table, row)
		if err != nil {
			item.Status = RepriceStatusError
			item.ErrorCode = "pricing_unavailable"
			item.ErrorMessage = err.Error()
			result.addItem(item)
			continue
		}
		item.AuthoritativeCost = authoritative
		item.CostDelta = normalizeRepriceMoney(authoritative.Sub(item.OriginalCost))
		item.PricingSource = source
		if req.DryRun {
			item.Status = RepriceStatusWouldApply
			result.addItem(item)
			continue
		}
		status, skipReason, err := s.applyRepriceItem(ctx, req, row, item)
		if err != nil {
			item.Status = RepriceStatusError
			item.ErrorCode = "reprice_apply_failed"
			item.ErrorMessage = err.Error()
			result.addItem(item)
			continue
		}
		item.Status = status
		if status == RepriceStatusSkipped {
			item.SkippedReason = skipReason
		}
		result.addItem(item)
	}
	return result, nil
}

func normalizeRepriceRequest(req RepriceRequest) (RepriceRequest, error) {
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = RepriceDefaultSource
	}
	if req.Limit == 0 {
		req.Limit = RepriceMaxBatchLimit
	}
	if req.Limit < 1 || req.Limit > RepriceMaxBatchLimit {
		return RepriceRequest{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrRepriceInvalidInput, RepriceMaxBatchLimit)
	}
	if req.ReconciledAt.IsZero() {
		req.ReconciledAt = time.Now().UTC()
	} else {
		req.ReconciledAt = req.ReconciledAt.UTC()
	}
	if req.UsageRecordID > 0 {
		return req, nil
	}
	if req.TenantID <= 0 || req.From.IsZero() || req.To.IsZero() {
		return RepriceRequest{}, fmt.Errorf("%w: usage_record_id or tenant_id/from/to is required", ErrRepriceInvalidInput)
	}
	req.From = req.From.UTC()
	req.To = req.To.UTC()
	if !req.From.Before(req.To) {
		return RepriceRequest{}, fmt.Errorf("%w: from must be before to", ErrRepriceInvalidInput)
	}
	return req, nil
}

func (s *RepriceService) currentRateTable(ctx context.Context) (RateTable, error) {
	if s.RateTables == nil {
		return RateTable{}, ErrPoolNotConfigured
	}
	version := strings.TrimSpace(s.BillingPolicyVersion)
	if version == "" {
		return RateTable{}, repricePricingUnavailable("billing policy version empty")
	}
	table, err := s.RateTables.GetRateTable(ctx, version)
	if err != nil {
		return RateTable{}, err
	}
	if strings.TrimSpace(table.Version) == "" {
		table.Version = version
	}
	return table, nil
}

func (s *RepriceService) authoritativeCost(ctx context.Context, table RateTable, row repriceUsageRecordRow) (decimal.Decimal, string, error) {
	ratio, err := s.groupRatio(ctx, row)
	if err != nil {
		return decimal.Zero, "", err
	}
	return repriceCostFromCurrentPricing(ctx, table, row, ratio)
}

func (s *RepriceService) groupRatio(ctx context.Context, row repriceUsageRecordRow) (decimal.Decimal, error) {
	if s.PricingRatioResolver == nil || row.TenantID <= 0 || row.PoolGroupID <= 0 {
		return decimal.Zero, nil
	}
	if resolver, ok := s.PricingRatioResolver.(repricePricingRatioResolverWithSignal); ok {
		ratio, pending, err := resolver.ResolveWithSignal(ctx, row.TenantID, row.PoolGroupID)
		if err != nil {
			return decimal.Zero, err
		}
		if pending {
			return decimal.Zero, repricePricingUnavailable("pricing ratio resolver served non-authoritative fallback")
		}
		return ratio, nil
	}
	return s.PricingRatioResolver.Resolve(ctx, row.TenantID, row.PoolGroupID)
}

func (s *RepriceService) loadRepriceRows(ctx context.Context, req RepriceRequest) ([]repriceUsageRecordRow, error) {
	var rows pgx.Rows
	var err error
	if req.UsageRecordID > 0 {
		rows, err = s.pool.Query(ctx, repriceUsageByIDSQL, req.UsageRecordID)
	} else {
		rows, err = s.pool.Query(ctx, repriceUsageByTenantWindowSQL, req.TenantID, req.From, req.To, req.Limit)
	}
	if err != nil {
		return nil, fmt.Errorf("billing: query reprice usage records: %w", err)
	}
	defer rows.Close()
	out := make([]repriceUsageRecordRow, 0, req.Limit)
	for rows.Next() {
		row, err := scanRepriceUsageRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: iterate reprice usage records: %w", err)
	}
	return out, nil
}

func scanRepriceUsageRecord(rows pgx.Rows) (repriceUsageRecordRow, error) {
	var row repriceUsageRecordRow
	var actualCost string
	if err := rows.Scan(
		&row.ID,
		&row.TenantID,
		&row.ClaimID,
		&row.PoolGroupID,
		&row.ProviderCode,
		&row.ProtocolFamily,
		&row.TokensInput,
		&row.TokensOutput,
		&row.CacheCreationTokens,
		&row.CacheReadTokens,
		&row.CacheCreation5mTokens,
		&row.CacheCreation1hTokens,
		&actualCost,
		&row.PendingReconciliation,
		&row.RequestedModel,
		&row.UpstreamModel,
		&row.ClaimRequestedModel,
		&row.HasReconciliationEvent,
	); err != nil {
		return repriceUsageRecordRow{}, fmt.Errorf("billing: scan reprice usage record: %w", err)
	}
	cost, err := decimal.NewFromString(strings.TrimSpace(actualCost))
	if err != nil {
		return repriceUsageRecordRow{}, fmt.Errorf("billing: parse usage actual_cost: %w", err)
	}
	row.ActualCost = cost
	return row, nil
}

func (s *RepriceService) applyRepriceItem(ctx context.Context, req RepriceRequest, row repriceUsageRecordRow, item RepriceItem) (string, string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", "", fmt.Errorf("begin reprice transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pending bool
	if err := tx.QueryRow(ctx, repriceLockUsageSQL, row.ID, row.TenantID).Scan(&pending); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RepriceStatusSkipped, "not_found", nil
		}
		return "", "", fmt.Errorf("lock usage record: %w", err)
	}
	if !pending {
		return RepriceStatusSkipped, "already_reconciled", tx.Commit(ctx)
	}
	var existing bool
	if err := tx.QueryRow(ctx, repriceExistingEventSQL, row.TenantID, row.ID).Scan(&existing); err != nil {
		return "", "", fmt.Errorf("check existing reconciliation event: %w", err)
	}
	if existing {
		return RepriceStatusAlreadyRepriced, "", tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, repriceInsertEventSQL,
		row.TenantID,
		row.ID,
		row.TokensInput,
		row.TokensOutput,
		item.AuthoritativeCost.StringFixed(8),
		item.CostDelta.StringFixed(8),
		req.Source,
		req.ReconciledAt,
	); err != nil {
		return "", "", fmt.Errorf("insert reconciliation event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit reprice transaction: %w", err)
	}
	return RepriceStatusRepriced, "", nil
}

func (r *RepriceResult) addItem(item RepriceItem) {
	r.Items = append(r.Items, item)
	r.Summary.Total++
	switch item.Status {
	case RepriceStatusWouldApply:
		r.Summary.WouldApply++
	case RepriceStatusRepriced:
		r.Summary.Repriced++
	case RepriceStatusAlreadyRepriced:
		r.Summary.AlreadyRepriced++
	case RepriceStatusSkipped:
		r.Summary.Skipped++
	case RepriceStatusError:
		r.Summary.Failed++
	}
}

func normalizeRepriceMoney(v decimal.Decimal) decimal.Decimal {
	return v.Round(8)
}

const repriceUsageSelectColumns = `
	ur.id,
	ur.tenant_id,
	ur.claim_id,
	COALESCE(blc.pooling_group_id, 0) AS pooling_group_id,
	COALESCE(p.code, '') AS provider_code,
	COALESCE(p.upstream_protocol, '') AS protocol_family,
	ur.tokens_input,
	ur.tokens_output,
	ur.cache_creation_tokens,
	ur.cache_read_tokens,
	ur.cache_creation_5m_tokens,
	ur.cache_creation_1h_tokens,
	ur.actual_cost::numeric(20,8)::text AS actual_cost,
	ur.pending_reconciliation,
	ur.requested_model,
	COALESCE(ur.upstream_model, '') AS upstream_model,
	blc.requested_model AS claim_requested_model,
	EXISTS (
		SELECT 1
		FROM usage_record_reconciliation_events re
		WHERE re.tenant_id = ur.tenant_id
		  AND re.original_usage_record_id = ur.id
	) AS has_reconciliation_event
FROM usage_records ur
INNER JOIN billing_ledger_claims blc
	ON blc.id = ur.claim_id
	AND blc.tenant_id = ur.tenant_id
LEFT JOIN provider_accounts pa
	ON pa.id = ur.provider_account_id
	AND pa.tenant_id = ur.tenant_id
LEFT JOIN providers p
	ON p.id = pa.provider_id
	AND p.tenant_id = pa.tenant_id
`

const repriceUsageByIDSQL = `
SELECT ` + repriceUsageSelectColumns + `
WHERE ur.id = $1
LIMIT 1`

const repriceUsageByTenantWindowSQL = `
SELECT ` + repriceUsageSelectColumns + `
WHERE ur.tenant_id = $1
  AND ur.settled_at >= $2
  AND ur.settled_at < $3
  AND ur.pending_reconciliation = true
  AND NOT EXISTS (
		SELECT 1
		FROM usage_record_reconciliation_events re
		WHERE re.tenant_id = ur.tenant_id
		  AND re.original_usage_record_id = ur.id
  )
ORDER BY ur.settled_at ASC, ur.id ASC
LIMIT $4`

const repriceLockUsageSQL = `
SELECT pending_reconciliation
FROM usage_records
WHERE id = $1 AND tenant_id = $2
FOR UPDATE`

const repriceExistingEventSQL = `
SELECT EXISTS (
	SELECT 1
	FROM usage_record_reconciliation_events
	WHERE tenant_id = $1
	  AND original_usage_record_id = $2
)
`

const repriceInsertEventSQL = `
INSERT INTO usage_record_reconciliation_events (
	tenant_id,
	original_usage_record_id,
	authoritative_tokens_input,
	authoritative_tokens_output,
	authoritative_cost,
	cost_delta,
	reconciliation_source,
	reconciled_at
) VALUES (
	$1, $2, $3, $4, $5::text::numeric(20,8), $6::text::numeric(20,8), $7, $8
)`
