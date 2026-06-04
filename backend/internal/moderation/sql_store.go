package moderation

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

type SQLStore struct {
	q dbmoderation.Querier
}

func NewSQLStore(q dbmoderation.Querier) *SQLStore {
	return &SQLStore{q: q}
}

func (s *SQLStore) GetConfig(ctx context.Context, tenantID int64) (ModerationConfig, error) {
	if s == nil || s.q == nil {
		return DefaultConfig(tenantID), nil
	}
	row, err := s.q.GetModerationConfig(ctx, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultConfig(tenantID), nil
	}
	if err != nil {
		return ModerationConfig{}, err
	}
	return configFromDB(row), nil
}

func (s *SQLStore) ListEnabled(ctx context.Context, tenantID int64) ([]KeywordRule, error) {
	rows, err := s.q.ListEnabledModerationKeywords(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]KeywordRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, keywordFromEnabledRow(row))
	}
	return out, nil
}

func (s *SQLStore) Contains(ctx context.Context, tenantID int64, hashHex string) (HashMatch, error) {
	row, err := s.q.FindEnabledModerationHash(ctx, dbmoderation.FindEnabledModerationHashParams{
		TenantID: tenantID,
		HashHex:  hashHex,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return HashMatch{}, nil
	}
	if err != nil {
		return HashMatch{}, err
	}
	return HashMatch{Matched: true, ID: row.ID, ReasonCode: row.ReasonCode}, nil
}

func (s *SQLStore) InsertModerationLog(ctx context.Context, event ModerationEvent) (int64, error) {
	return s.q.InsertModerationLog(ctx, dbmoderation.InsertModerationLogParams{
		TenantID:         event.TenantID,
		APIKeyID:         event.APIKeyID,
		UserID:           event.UserID,
		RequestID:        stringPtr(event.RequestID),
		PayloadHash:      event.PayloadHash,
		Decision:         string(event.Decision),
		ReasonCode:       safeReasonCode(event.ReasonCode, event.Decision),
		MatchedKeywordID: event.MatchedKeywordID,
		MatchedHashID:    event.MatchedHashID,
		ViolationFeeUsd:  numericFromDecimal(event.ViolationFeeUSD),
		BillingEventID:   event.BillingEventID,
	})
}

func (s *SQLStore) RecordModerationViolationEvent(ctx context.Context, event ModerationEvent) error {
	_, err := s.q.InsertModerationViolationEvent(ctx, dbmoderation.InsertModerationViolationEventParams{
		TenantID:         event.TenantID,
		APIKeyID:         event.APIKeyID,
		UserID:           event.UserID,
		RequestID:        stringPtr(event.RequestID),
		PayloadHash:      event.PayloadHash,
		Decision:         string(event.Decision),
		ReasonCode:       safeReasonCode(event.ReasonCode, event.Decision),
		MatchedKeywordID: event.MatchedKeywordID,
		MatchedHashID:    event.MatchedHashID,
	})
	return err
}

func (s *SQLStore) CountBlocksInWindow(ctx context.Context, tenantID int64, apiKeyID int64, seconds int32) (int64, error) {
	return s.q.CountModerationBlocksInWindow(ctx, dbmoderation.CountModerationBlocksInWindowParams{
		TenantID:      tenantID,
		APIKeyID:      apiKeyID,
		WindowSeconds: seconds,
	})
}

func (s *SQLStore) DisableAPIKey(ctx context.Context, tenantID int64, apiKeyID int64) error {
	_, err := s.q.DisableModerationAPIKey(ctx, dbmoderation.DisableModerationAPIKeyParams{
		TenantID: tenantID,
		APIKeyID: apiKeyID,
	})
	return err
}

func (s *SQLStore) CreateKeyword(ctx context.Context, req CreateKeywordRequest) (KeywordRule, error) {
	row, err := s.q.CreateModerationKeyword(ctx, dbmoderation.CreateModerationKeywordParams{
		TenantID:   req.TenantID,
		Keyword:    req.Keyword,
		ReasonCode: nonEmpty(req.ReasonCode, "keyword_match"),
		Enabled:    req.Enabled,
		UpdatedBy:  stringPtr(req.UpdatedBy),
	})
	if isUniqueViolation(err) {
		return KeywordRule{}, ErrKeywordExists
	}
	if err != nil {
		return KeywordRule{}, err
	}
	return keywordFromCreateRow(row), nil
}

func (s *SQLStore) ListKeywords(ctx context.Context, tenantID int64, limit int32, offset int32) ([]KeywordRule, error) {
	rows, err := s.q.ListModerationKeywords(ctx, dbmoderation.ListModerationKeywordsParams{
		TenantID:   tenantID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]KeywordRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, keywordFromListRow(row))
	}
	return out, nil
}

func (s *SQLStore) DeleteKeyword(ctx context.Context, tenantID int64, id int64) error {
	rows, err := s.q.SoftDeleteModerationKeyword(ctx, dbmoderation.SoftDeleteModerationKeywordParams{
		TenantID: tenantID,
		ID:       id,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) UpsertConfig(ctx context.Context, cfg ModerationConfig) (ModerationConfig, error) {
	row, err := s.q.UpsertModerationConfig(ctx, dbmoderation.UpsertModerationConfigParams{
		TenantID:         cfg.TenantID,
		Enabled:          cfg.Enabled,
		FailClosed:       cfg.FailClosed,
		SampleRatePct:    cfg.SampleRatePct,
		BanThreshold:     cfg.BanThreshold,
		BanWindowSeconds: cfg.BanWindowSeconds,
		ViolationFeeUsd:  numericFromDecimal(cfg.ViolationFeeUSD),
		UpdatedBy:        stringPtr(cfg.UpdatedBy),
	})
	if err != nil {
		return ModerationConfig{}, err
	}
	return configFromDB(row), nil
}

func configFromDB(row dbmoderation.ModerationConfig) ModerationConfig {
	cfg := ModerationConfig{
		TenantID:         row.TenantID,
		Enabled:          row.Enabled,
		FailClosed:       row.FailClosed,
		SampleRatePct:    row.SampleRatePct,
		BanThreshold:     row.BanThreshold,
		BanWindowSeconds: row.BanWindowSeconds,
		ViolationFeeUSD:  row.ViolationFeeUsd,
		UpdatedAt:        timeFromPG(row.UpdatedAt),
	}
	if row.UpdatedBy != nil {
		cfg.UpdatedBy = *row.UpdatedBy
	}
	return cfg
}

func keywordFromEnabledRow(row dbmoderation.ListEnabledModerationKeywordsRow) KeywordRule {
	return KeywordRule{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Keyword:    row.Keyword,
		ReasonCode: row.ReasonCode,
		Enabled:    row.Enabled,
		CreatedAt:  timeFromPG(row.CreatedAt),
		UpdatedAt:  timeFromPG(row.UpdatedAt),
	}
}

func keywordFromCreateRow(row dbmoderation.CreateModerationKeywordRow) KeywordRule {
	return KeywordRule{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Keyword:    row.Keyword,
		ReasonCode: row.ReasonCode,
		Enabled:    row.Enabled,
		CreatedAt:  timeFromPG(row.CreatedAt),
		UpdatedAt:  timeFromPG(row.UpdatedAt),
	}
}

func keywordFromListRow(row dbmoderation.ListModerationKeywordsRow) KeywordRule {
	return KeywordRule{
		ID:         row.ID,
		TenantID:   row.TenantID,
		Keyword:    row.Keyword,
		ReasonCode: row.ReasonCode,
		Enabled:    row.Enabled,
		CreatedAt:  timeFromPG(row.CreatedAt),
		UpdatedAt:  timeFromPG(row.UpdatedAt),
	}
}

func numericFromDecimal(value decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{
		Int:   new(big.Int).Set(value.Coefficient()),
		Exp:   value.Exponent(),
		Valid: true,
	}
}

func timeFromPG(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
