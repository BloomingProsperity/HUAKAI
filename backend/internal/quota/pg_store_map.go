package quota

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

type scopeRecord struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func marshalScopes(scopes []Scope) ([]byte, error) {
	records := make([]scopeRecord, 0, len(scopes))
	for _, scope := range scopes {
		records = append(records, scopeRecord{
			Kind: string(scope.Kind),
			ID:   normalizeScopeID(scope.Kind, scope.ID),
		})
	}
	if records == nil {
		records = []scopeRecord{}
	}
	return json.Marshal(records)
}

func parseScopes(tenantID int64, data []byte) ([]Scope, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var records []scopeRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("quota: decode scope snapshot: %w", err)
	}
	scopes := make([]Scope, 0, len(records))
	for _, record := range records {
		scopes = append(scopes, Scope{
			TenantID: tenantID,
			Kind:     ScopeKind(record.Kind),
			ID:       record.ID,
		})
	}
	return scopes, nil
}

func normalizeScopeID(kind ScopeKind, id string) string {
	if kind == ScopeGlobal && id == "" {
		return "*"
	}
	return id
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func pgTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func pgNumeric(d decimal.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(d.String()); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("quota: encode numeric %s: %w", d.String(), err)
	}
	return n, nil
}

func decimalFromPG(n pgtype.Numeric) decimal.Decimal {
	if !n.Valid || n.Int == nil {
		return decimal.Zero
	}
	return decimal.NewFromBigInt(n.Int, n.Exp)
}

func requireAffected(rows int64, err error) error {
	if err != nil {
		return err
	}
	if rows == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func policyFromDB(row dbquota.ListActiveQuotaPoliciesForScopesRow) Policy {
	return Policy{
		TenantID: row.TenantID,
		ID:       row.ID,
		Scope: Scope{
			TenantID: row.TenantID,
			Kind:     ScopeKind(row.ScopeKind),
			ID:       row.ScopeID,
		},
		Metric: Metric(row.Metric),
		Window: Window{
			Kind:    WindowKind(row.WindowKind),
			Seconds: int64(row.WindowSeconds),
		},
		LimitValue: decimalFromPG(row.LimitValue),
		BurstValue: decimalFromPG(row.BurstValue),
		Mode:       Mode(row.Mode),
		Priority:   int(row.Priority),
		ValidFrom:  pgTime(row.ValidFrom),
		ValidUntil: pgTimePtr(row.ValidUntil),
	}
}

func reservationFromGet(row dbquota.GetQuotaReservationByClaimForUpdateRow) (Reservation, error) {
	scopes, err := parseScopes(row.TenantID, row.ScopeSnapshot)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{
		TenantID:           row.TenantID,
		ID:                 row.ID,
		ClaimID:            row.ClaimID,
		RequestFingerprint: row.RequestFingerprint,
		Scopes:             scopes,
		PredictedCost:      decimalFromPG(row.PredictedCost),
		ReservedUnits:      decimalFromPG(row.ReservedUnits),
		Status:             ReservationStatus(row.Status),
		LeaseExpiresAt:     pgTime(row.LeaseExpiresAt),
	}, nil
}

func reservationFromInsert(row dbquota.InsertQuotaReservationRow) (Reservation, error) {
	scopes, err := parseScopes(row.TenantID, row.ScopeSnapshot)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{
		TenantID:           row.TenantID,
		ID:                 row.ID,
		ClaimID:            row.ClaimID,
		RequestFingerprint: row.RequestFingerprint,
		Scopes:             scopes,
		PredictedCost:      decimalFromPG(row.PredictedCost),
		ReservedUnits:      decimalFromPG(row.ReservedUnits),
		Status:             ReservationStatus(row.Status),
		LeaseExpiresAt:     pgTime(row.LeaseExpiresAt),
		CreatedAt:          pgTime(row.CreatedAt),
		UpdatedAt:          pgTime(row.UpdatedAt),
	}, nil
}

func reservationFromReactivate(row dbquota.ReactivateQuotaReservationRow) (Reservation, error) {
	scopes, err := parseScopes(row.TenantID, row.ScopeSnapshot)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{
		TenantID:           row.TenantID,
		ID:                 row.ID,
		ClaimID:            row.ClaimID,
		RequestFingerprint: row.RequestFingerprint,
		Scopes:             scopes,
		PredictedCost:      decimalFromPG(row.PredictedCost),
		ReservedUnits:      decimalFromPG(row.ReservedUnits),
		Status:             ReservationStatus(row.Status),
		LeaseExpiresAt:     pgTime(row.LeaseExpiresAt),
		CreatedAt:          pgTime(row.CreatedAt),
		UpdatedAt:          pgTime(row.UpdatedAt),
	}, nil
}

func windowCounterFromUpsert(row dbquota.UpsertQuotaWindowRow) WindowCounter {
	return WindowCounter{
		TenantID: row.TenantID,
		ID:       row.ID,
		PolicyID: row.PolicyID,
		Window: Window{
			Kind:    WindowKind(row.WindowKind),
			Seconds: int64(row.WindowSeconds),
			Start:   pgTime(row.WindowStart),
			End:     pgTime(row.WindowEnd),
		},
		ReservedValue: decimalFromPG(row.ReservedValue),
		SettledValue:  decimalFromPG(row.SettledValue),
		OverageValue:  decimalFromPG(row.OverageValue),
		RequestCount:  row.RequestCount,
		Version:       int(row.Version),
	}
}

func windowCounterFromGet(row dbquota.GetQuotaWindowForUpdateRow) WindowCounter {
	return WindowCounter{
		TenantID: row.TenantID,
		ID:       row.ID,
		PolicyID: row.PolicyID,
		Window: Window{
			Start: pgTime(row.WindowStart),
			End:   pgTime(row.WindowEnd),
		},
		ReservedValue: decimalFromPG(row.ReservedValue),
		SettledValue:  decimalFromPG(row.SettledValue),
		OverageValue:  decimalFromPG(row.OverageValue),
		RequestCount:  row.RequestCount,
		Version:       int(row.Version),
	}
}

func windowCounterFromReserve(row dbquota.IncrementQuotaWindowReservedRow) WindowCounter {
	return WindowCounter{
		TenantID:      row.TenantID,
		ID:            row.ID,
		ReservedValue: decimalFromPG(row.ReservedValue),
		SettledValue:  decimalFromPG(row.SettledValue),
		OverageValue:  decimalFromPG(row.OverageValue),
		RequestCount:  row.RequestCount,
		Version:       int(row.Version),
	}
}

func windowCounterFromRequestCount(row dbquota.IncrementQuotaWindowRequestCountRow) WindowCounter {
	return WindowCounter{
		TenantID:      row.TenantID,
		ID:            row.ID,
		ReservedValue: decimalFromPG(row.ReservedValue),
		SettledValue:  decimalFromPG(row.SettledValue),
		OverageValue:  decimalFromPG(row.OverageValue),
		RequestCount:  row.RequestCount,
		Version:       int(row.Version),
	}
}

func windowCounterFromSettlement(row dbquota.ApplyQuotaWindowSettlementRow) WindowCounter {
	return WindowCounter{
		TenantID:      row.TenantID,
		ID:            row.ID,
		ReservedValue: decimalFromPG(row.ReservedValue),
		SettledValue:  decimalFromPG(row.SettledValue),
		OverageValue:  decimalFromPG(row.OverageValue),
		RequestCount:  row.RequestCount,
		Version:       int(row.Version),
	}
}
