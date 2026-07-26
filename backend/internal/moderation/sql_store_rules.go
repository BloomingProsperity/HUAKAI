package moderation

import (
	"context"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

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
		TenantID: tenantID, PageLimit: limit, PageOffset: offset,
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
		TenantID: tenantID, ID: id,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) CreateHash(ctx context.Context, req CreateHashRequest) (HashRule, error) {
	row, err := s.q.CreateModerationHash(ctx, dbmoderation.CreateModerationHashParams{
		TenantID:   req.TenantID,
		HashHex:    req.HashHex,
		ReasonCode: nonEmpty(req.ReasonCode, "hash_match"),
		Enabled:    req.Enabled,
		UpdatedBy:  stringPtr(req.UpdatedBy),
	})
	if isUniqueViolation(err) {
		return HashRule{}, ErrHashExists
	}
	if err != nil {
		return HashRule{}, err
	}
	return hashFromCreateRow(row), nil
}

func (s *SQLStore) ListHashes(ctx context.Context, tenantID int64, limit int32, offset int32) ([]HashRule, error) {
	rows, err := s.q.ListModerationHashes(ctx, dbmoderation.ListModerationHashesParams{
		TenantID: tenantID, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]HashRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, hashFromListRow(row))
	}
	return out, nil
}

func (s *SQLStore) DeleteHash(ctx context.Context, tenantID int64, id int64) error {
	rows, err := s.q.SoftDeleteModerationHash(ctx, dbmoderation.SoftDeleteModerationHashParams{
		TenantID: tenantID, ID: id,
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
	// 管理面不暴露违规费，兼容列固定写零。
	row, err := s.q.UpsertModerationConfig(ctx, dbmoderation.UpsertModerationConfigParams{
		TenantID:            cfg.TenantID,
		Enabled:             cfg.Enabled,
		FailClosed:          cfg.FailClosed,
		SampleRatePct:       cfg.SampleRatePct,
		BanThreshold:        cfg.BanThreshold,
		BanWindowSeconds:    cfg.BanWindowSeconds,
		AutoDisableKeyOnBan: cfg.AutoDisableKeyOnBan,
		ViolationFeeUsd:     numericFromDecimal(decimal.Zero),
		UpdatedBy:           stringPtr(cfg.UpdatedBy),
	})
	if err != nil {
		return ModerationConfig{}, err
	}
	return configFromUpsertRow(row), nil
}

func configFromGetRow(row dbmoderation.GetModerationConfigRow) ModerationConfig {
	cfg := ModerationConfig{
		TenantID:            row.TenantID,
		Enabled:             row.Enabled,
		FailClosed:          row.FailClosed,
		SampleRatePct:       row.SampleRatePct,
		BanThreshold:        row.BanThreshold,
		BanWindowSeconds:    row.BanWindowSeconds,
		AutoDisableKeyOnBan: row.AutoDisableKeyOnBan,
		UpdatedAt:           timeFromPG(row.UpdatedAt),
	}
	if row.UpdatedBy != nil {
		cfg.UpdatedBy = *row.UpdatedBy
	}
	return cfg
}

func configFromUpsertRow(row dbmoderation.UpsertModerationConfigRow) ModerationConfig {
	cfg := ModerationConfig{
		TenantID: row.TenantID, Enabled: row.Enabled, FailClosed: row.FailClosed,
		SampleRatePct: row.SampleRatePct, BanThreshold: row.BanThreshold,
		BanWindowSeconds:    row.BanWindowSeconds,
		AutoDisableKeyOnBan: row.AutoDisableKeyOnBan,
		UpdatedAt:           timeFromPG(row.UpdatedAt),
	}
	if row.UpdatedBy != nil {
		cfg.UpdatedBy = *row.UpdatedBy
	}
	return cfg
}

func keywordFromEnabledRow(row dbmoderation.ListEnabledModerationKeywordsRow) KeywordRule {
	return KeywordRule{
		ID: row.ID, TenantID: row.TenantID, Keyword: row.Keyword,
		ReasonCode: row.ReasonCode, Enabled: row.Enabled,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}
}

func keywordFromCreateRow(row dbmoderation.CreateModerationKeywordRow) KeywordRule {
	return KeywordRule{
		ID: row.ID, TenantID: row.TenantID, Keyword: row.Keyword,
		ReasonCode: row.ReasonCode, Enabled: row.Enabled,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}
}

func keywordFromListRow(row dbmoderation.ListModerationKeywordsRow) KeywordRule {
	return KeywordRule{
		ID: row.ID, TenantID: row.TenantID, Keyword: row.Keyword,
		ReasonCode: row.ReasonCode, Enabled: row.Enabled,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}
}

func hashFromCreateRow(row dbmoderation.CreateModerationHashRow) HashRule {
	return HashRule{
		ID: row.ID, TenantID: row.TenantID, HashHex: row.HashHex,
		ReasonCode: row.ReasonCode, Enabled: row.Enabled,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}
}

func hashFromListRow(row dbmoderation.ListModerationHashesRow) HashRule {
	return HashRule{
		ID: row.ID, TenantID: row.TenantID, HashHex: row.HashHex,
		ReasonCode: row.ReasonCode, Enabled: row.Enabled,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}
}

func numericFromDecimal(value decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{
		Int: new(big.Int).Set(value.Coefficient()), Exp: value.Exponent(), Valid: true,
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
