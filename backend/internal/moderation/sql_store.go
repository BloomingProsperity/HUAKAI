package moderation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

type SQLStore struct {
	q     dbmoderation.Querier
	begin interface {
		Begin(context.Context) (pgx.Tx, error)
	}
}

func NewSQLStore(q dbmoderation.Querier) *SQLStore {
	return &SQLStore{q: q}
}

func NewSQLStoreWithPool(pool *pgxpool.Pool) *SQLStore {
	if pool == nil {
		return &SQLStore{}
	}
	return &SQLStore{q: dbmoderation.New(pool), begin: pool}
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
	return configFromGetRow(row), nil
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
	// 违规费链路尚未启用，历史列固定写零且不接 billing。
	return s.q.InsertModerationLog(ctx, dbmoderation.InsertModerationLogParams{
		TenantID:         event.TenantID,
		APIKeyID:         event.APIKeyID,
		UserID:           event.UserID,
		RequestID:        stringPtr(event.RequestID),
		InputExcerpt:     event.InputExcerpt,
		Decision:         string(event.Decision),
		ReasonCode:       safeReasonCode(event.ReasonCode, event.Decision),
		MatchedKeywordID: event.MatchedKeywordID,
		MatchedHashID:    event.MatchedHashID,
		ActorID:          stringPtr(event.ActorID),
		ActorRole:        stringPtr(event.ActorRole),
	})
}

func (s *SQLStore) ListModerationLogs(ctx context.Context, tenantID int64, apiKeyID *int64, limit int32, offset int32) ([]ModerationLog, error) {
	rows, err := s.q.ListModerationLog(ctx, dbmoderation.ListModerationLogParams{
		TenantID:   tenantID,
		APIKeyID:   apiKeyID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ModerationLog, 0, len(rows))
	for _, row := range rows {
		out = append(out, moderationLogFromDB(row))
	}
	return out, nil
}

func (s *SQLStore) ListModerationViolations(ctx context.Context, tenantID int64, apiKeyID *int64, userID *int64, limit int32, offset int32) ([]ModerationViolation, error) {
	rows, err := s.q.ListModerationViolations(ctx, dbmoderation.ListModerationViolationsParams{
		TenantID: tenantID, APIKeyID: apiKeyID, UserID: userID,
		PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ModerationViolation, 0, len(rows))
	for _, row := range rows {
		out = append(out, moderationViolationFromDB(row))
	}
	return out, nil
}

func (s *SQLStore) RecordModerationViolation(ctx context.Context, event ModerationEvent, cfg ModerationConfig) (BanResult, error) {
	if s == nil || s.begin == nil {
		return BanResult{}, ErrTransactionMissing
	}
	if event.TenantID <= 0 || event.APIKeyID <= 0 || event.UserID <= 0 ||
		strings.TrimSpace(event.RequestID) == "" || cfg.BanWindowSeconds <= 0 ||
		!isCountedViolation(event.Decision) {
		return BanResult{}, ErrInvalidEvent
	}
	tx, err := s.begin.Begin(ctx)
	if err != nil {
		return BanResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbmoderation.New(tx)

	existing, err := q.GetModerationViolationByRequest(ctx, dbmoderation.GetModerationViolationByRequestParams{
		TenantID: event.TenantID, APIKeyID: event.APIKeyID, RequestID: event.RequestID,
	})
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return BanResult{}, err
		}
		return banResultFromViolation(existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BanResult{}, err
	}

	key, err := q.LockModerationAPIKey(ctx, dbmoderation.LockModerationAPIKeyParams{
		TenantID: event.TenantID,
		APIKeyID: event.APIKeyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return BanResult{}, ErrNotFound
	}
	if err != nil {
		return BanResult{}, err
	}
	if key.UserID != event.UserID {
		return BanResult{}, ErrStateConflict
	}

	eventID, err := q.InsertModerationViolationEvent(ctx, dbmoderation.InsertModerationViolationEventParams{
		TenantID:                 event.TenantID,
		APIKeyID:                 event.APIKeyID,
		UserID:                   event.UserID,
		RequestID:                event.RequestID,
		Decision:                 string(event.Decision),
		ReasonCode:               safeReasonCode(event.ReasonCode, event.Decision),
		MatchedKeywordID:         event.MatchedKeywordID,
		MatchedHashID:            event.MatchedHashID,
		BanThresholdSnapshot:     cfg.BanThreshold,
		BanWindowSecondsSnapshot: cfg.BanWindowSeconds,
		AutoDisableEnabled:       cfg.AutoDisableKeyOnBan,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := q.GetModerationViolationByRequest(ctx, dbmoderation.GetModerationViolationByRequestParams{
			TenantID: event.TenantID, APIKeyID: event.APIKeyID, RequestID: event.RequestID,
		})
		if getErr != nil {
			return BanResult{}, getErr
		}
		if err := tx.Commit(ctx); err != nil {
			return BanResult{}, err
		}
		return banResultFromViolation(existing), nil
	}
	if err != nil {
		return BanResult{}, err
	}

	count, err := q.CountModerationBlocksInWindow(ctx, dbmoderation.CountModerationBlocksInWindowParams{
		TenantID: event.TenantID, APIKeyID: event.APIKeyID, WindowSeconds: cfg.BanWindowSeconds,
	})
	if err != nil {
		return BanResult{}, err
	}
	thresholdReached := cfg.BanThreshold > 0 && count >= int64(cfg.BanThreshold)
	dispositionSource := "none"
	dispositionResult := "unchanged"
	disabled := false
	if thresholdReached && cfg.AutoDisableKeyOnBan {
		dispositionSource = "auto"
		if key.Status != "active" {
			dispositionResult = "already_non_active"
		} else {
			disabledKey, disableErr := q.DisableModerationAPIKey(ctx, dbmoderation.DisableModerationAPIKeyParams{
				TenantID: event.TenantID, APIKeyID: event.APIKeyID,
			})
			if disableErr != nil {
				return BanResult{}, disableErr
			}
			if err := q.UpsertModerationKeyState(ctx, dbmoderation.UpsertModerationKeyStateParams{
				TenantID: event.TenantID, APIKeyID: event.APIKeyID,
				Source: "auto", TriggerEventID: eventID,
				ReasonCode: safeReasonCode(event.ReasonCode, event.Decision),
				ActorID:    "system:moderation", ActorRole: "system",
				DisableGeneration: disabledKey.StatusGeneration,
			}); err != nil {
				return BanResult{}, err
			}
			dispositionResult = "disabled"
			disabled = true
		}
	}
	if _, err := q.FinalizeModerationViolationEvent(ctx, dbmoderation.FinalizeModerationViolationEventParams{
		TenantID: event.TenantID, ViolationEventID: eventID,
		ViolationCount: count, ThresholdReached: thresholdReached,
		DispositionSource: dispositionSource, DispositionResult: dispositionResult,
	}); err != nil {
		return BanResult{}, err
	}
	if _, err := q.InsertModerationLog(ctx, dbmoderation.InsertModerationLogParams{
		TenantID: event.TenantID, APIKeyID: event.APIKeyID, UserID: event.UserID,
		RequestID: stringPtr(event.RequestID), ViolationEventID: &eventID,
		InputExcerpt: event.InputExcerpt, Decision: string(event.Decision),
		ReasonCode:       safeReasonCode(event.ReasonCode, event.Decision),
		MatchedKeywordID: event.MatchedKeywordID, MatchedHashID: event.MatchedHashID,
		ViolationCount: count, ThresholdReached: thresholdReached, KeyDisabled: disabled,
	}); err != nil {
		return BanResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BanResult{}, err
	}
	return BanResult{
		EventID: eventID, Disabled: disabled, Count: count,
		ThresholdReached: thresholdReached,
	}, nil
}

func banResultFromViolation(row dbmoderation.GetModerationViolationByRequestRow) BanResult {
	return BanResult{
		EventID:          row.ID,
		Disabled:         row.DispositionResult == "disabled",
		Count:            row.ViolationCount,
		ThresholdReached: row.ThresholdReached,
		Idempotent:       true,
	}
}

func (s *SQLStore) ListBannedAPIKeys(ctx context.Context, tenantID int64, limit int32, offset int32) ([]BannedAPIKey, error) {
	rows, err := s.q.ListBannedKeys(ctx, dbmoderation.ListBannedKeysParams{
		TenantID:   tenantID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]BannedAPIKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, bannedAPIKeyFromDB(row))
	}
	return out, nil
}

func (s *SQLStore) UnbanAPIKey(ctx context.Context, req UnbanAPIKeyRequest) (UnbanAPIKeyResult, error) {
	if s == nil || s.begin == nil {
		return UnbanAPIKeyResult{}, ErrTransactionMissing
	}
	input, err := newModerationKeyOperationInput(
		"unban", req.TenantID, req.APIKeyID, 0, req.IdempotencyKey,
		req.ActorID, req.ActorRole, req.Reason,
	)
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	tx, err := s.begin.Begin(ctx)
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbmoderation.New(tx)
	if existing, found, err := loadModerationKeyOperation(ctx, q, input); err != nil {
		return UnbanAPIKeyResult{}, err
	} else if found {
		return unbanResultFromOperation(existing), nil
	}
	state, err := q.LockModerationKeyState(ctx, dbmoderation.LockModerationKeyStateParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UnbanAPIKeyResult{}, ErrNotFound
	}
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	if state.Status != "disabled" || state.StatusGeneration != state.DisableGeneration {
		return UnbanAPIKeyResult{}, ErrStateConflict
	}
	row, err := q.EnableModerationAPIKeyCAS(ctx, dbmoderation.EnableModerationAPIKeyCASParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID,
		ExpectedGeneration: state.DisableGeneration,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UnbanAPIKeyResult{}, ErrStateConflict
	}
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	reasonCode := adminUnbanReasonCode(req.Reason)
	changed, err := q.MarkModerationKeyStateActive(ctx, dbmoderation.MarkModerationKeyStateActiveParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID,
		ExpectedGeneration: state.DisableGeneration,
		ActorID:            req.ActorID, ActorRole: req.ActorRole, ReasonCode: reasonCode,
	})
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	if changed != 1 {
		return UnbanAPIKeyResult{}, ErrStateConflict
	}
	auditLogID, err := q.InsertModerationLog(ctx, dbmoderation.InsertModerationLogParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID, UserID: state.UserID,
		RequestID: stringPtr(input.idempotencyKey),
		Decision:  string(DecisionAdminUnban), ReasonCode: reasonCode,
		ActorID: stringPtr(req.ActorID), ActorRole: stringPtr(req.ActorRole),
	})
	if err != nil {
		return UnbanAPIKeyResult{}, err
	}
	result := UnbanAPIKeyResult{
		APIKeyID:   req.APIKeyID,
		TenantID:   req.TenantID,
		Status:     row.Status,
		AuditLogID: auditLogID,
		UpdatedAt:  timeFromPG(row.UpdatedAt),
	}
	if err := insertModerationKeyOperation(ctx, q, input, auditLogID,
		row.Status, row.StatusGeneration, result.UpdatedAt); err != nil {
		return UnbanAPIKeyResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UnbanAPIKeyResult{}, err
	}
	return result, nil
}

func (s *SQLStore) DisableAPIKey(ctx context.Context, req DisableAPIKeyRequest) (DisableAPIKeyResult, error) {
	if s == nil || s.begin == nil {
		return DisableAPIKeyResult{}, ErrTransactionMissing
	}
	if req.TenantID <= 0 || req.APIKeyID <= 0 || req.ViolationEventID <= 0 ||
		strings.TrimSpace(req.ActorID) == "" {
		return DisableAPIKeyResult{}, ErrInvalidEvent
	}
	input, err := newModerationKeyOperationInput(
		"disable", req.TenantID, req.APIKeyID, req.ViolationEventID,
		req.IdempotencyKey, req.ActorID, req.ActorRole, req.Reason,
	)
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	tx, err := s.begin.Begin(ctx)
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbmoderation.New(tx)
	if existing, found, err := loadModerationKeyOperation(ctx, q, input); err != nil {
		return DisableAPIKeyResult{}, err
	} else if found {
		return disableResultFromOperation(existing), nil
	}

	event, err := q.LockThresholdModerationViolation(ctx, dbmoderation.LockThresholdModerationViolationParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID, ViolationEventID: req.ViolationEventID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DisableAPIKeyResult{}, ErrNotFound
	}
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	if event.Status != "active" {
		return DisableAPIKeyResult{}, ErrStateConflict
	}
	disabled, err := q.DisableModerationAPIKey(ctx, dbmoderation.DisableModerationAPIKeyParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DisableAPIKeyResult{}, ErrStateConflict
	}
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	reasonCode := adminDisableReasonCode(req.Reason, event.ReasonCode)
	if err := q.UpsertModerationKeyState(ctx, dbmoderation.UpsertModerationKeyStateParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID,
		Source: "manual", TriggerEventID: req.ViolationEventID,
		ReasonCode: reasonCode, ActorID: req.ActorID, ActorRole: req.ActorRole,
		DisableGeneration: disabled.StatusGeneration,
	}); err != nil {
		return DisableAPIKeyResult{}, err
	}
	changed, err := q.SetManualModerationDisposition(ctx, dbmoderation.SetManualModerationDispositionParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID, ViolationEventID: req.ViolationEventID,
	})
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	if changed != 1 {
		return DisableAPIKeyResult{}, ErrStateConflict
	}
	logID, err := q.InsertModerationLog(ctx, dbmoderation.InsertModerationLogParams{
		TenantID: req.TenantID, APIKeyID: req.APIKeyID, UserID: event.UserID,
		RequestID:        stringPtr(input.idempotencyKey),
		ViolationEventID: &req.ViolationEventID,
		Decision:         string(DecisionAdminDisable), ReasonCode: reasonCode,
		ViolationCount: event.ViolationCount, ThresholdReached: true, KeyDisabled: true,
		ActorID: stringPtr(req.ActorID), ActorRole: stringPtr(req.ActorRole),
	})
	if err != nil {
		return DisableAPIKeyResult{}, err
	}
	result := DisableAPIKeyResult{
		APIKeyID: req.APIKeyID, TenantID: req.TenantID, Status: "disabled",
		LogID: logID, UpdatedAt: timeFromPG(disabled.UpdatedAt),
	}
	if err := insertModerationKeyOperation(ctx, q, input, logID,
		result.Status, disabled.StatusGeneration, result.UpdatedAt); err != nil {
		return DisableAPIKeyResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DisableAPIKeyResult{}, err
	}
	return result, nil
}

func moderationLogFromDB(row dbmoderation.ListModerationLogRow) ModerationLog {
	log := ModerationLog{
		ID:               row.ID,
		TenantID:         row.TenantID,
		APIKeyID:         row.APIKeyID,
		UserID:           row.UserID,
		ViolationEventID: row.ViolationEventID,
		InputExcerpt:     row.InputExcerpt,
		Decision:         Decision(row.Decision),
		ReasonCode:       row.ReasonCode,
		MatchedKeywordID: row.MatchedKeywordID,
		MatchedHashID:    row.MatchedHashID,
		ViolationCount:   row.ViolationCount,
		ThresholdReached: row.ThresholdReached,
		KeyDisabled:      row.KeyDisabled,
		OccurredAt:       timeFromPG(row.OccurredAt),
	}
	if row.RequestID != nil {
		log.RequestID = *row.RequestID
	}
	if row.ActorID != nil {
		log.ActorID = *row.ActorID
	}
	if row.ActorRole != nil {
		log.ActorRole = *row.ActorRole
	}
	return log
}

func moderationViolationFromDB(row dbmoderation.ListModerationViolationsRow) ModerationViolation {
	return ModerationViolation{
		ID: row.ID, TenantID: row.TenantID, APIKeyID: row.APIKeyID, UserID: row.UserID,
		RequestID: row.RequestID, Decision: Decision(row.Decision), ReasonCode: row.ReasonCode,
		MatchedKeywordID: row.MatchedKeywordID, MatchedHashID: row.MatchedHashID,
		BanThresholdSnapshot:     row.BanThresholdSnapshot,
		BanWindowSecondsSnapshot: row.BanWindowSecondsSnapshot,
		ViolationCount:           row.ViolationCount, ThresholdReached: row.ThresholdReached,
		AutoDisableEnabled: row.AutoDisableEnabled,
		DispositionSource:  row.DispositionSource, DispositionResult: row.DispositionResult,
		InputExcerpt: row.InputExcerpt, KeyDisabled: row.KeyDisabled,
		OccurredAt: timeFromPG(row.OccurredAt),
	}
}

func bannedAPIKeyFromDB(row dbmoderation.ListBannedKeysRow) BannedAPIKey {
	return BannedAPIKey{
		ID:                row.ID,
		TenantID:          row.TenantID,
		UserID:            row.UserID,
		Name:              row.Name,
		KeyPrefix:         row.KeyPrefix,
		Status:            row.Status,
		Source:            row.Source,
		ReasonCode:        row.ReasonCode,
		DisableGeneration: row.DisableGeneration,
		ViolationCount:    row.ViolationCount,
		LastViolationAt:   timeFromPG(row.LastViolationAt),
		CreatedAt:         timeFromPG(row.CreatedAt),
		UpdatedAt:         timeFromPG(row.UpdatedAt),
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

func adminUnbanReasonCode(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "admin_unban"
	}
	return safeReasonCode("admin_unban:"+reason, DecisionPass)
}

func adminDisableReasonCode(reason string, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = strings.TrimSpace(fallback)
	}
	if reason == "" {
		return "admin_disable"
	}
	return safeReasonCode("admin_disable:"+reason, DecisionAdminDisable)
}
