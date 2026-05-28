package balancehold

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

var ErrInsufficientBalance = errors.New("balancehold: insufficient balance")

// ReserveParams defines a single hold reservation request.
type ReserveParams struct {
	TenantID int64
	UserID   int64
	ClaimID  int64
	Cost     decimal.Decimal
}

// Snapshot mirrors the latest known balance/held pair for a user in Tx scope.
type Snapshot struct {
	Balance decimal.Decimal
	Held    decimal.Decimal
}

func newSnapshot(row dbbilling.ReserveBalanceHoldRow) Snapshot {
	return Snapshot{Balance: row.Balance, Held: row.Held}
}

func Reserve(ctx context.Context, tx pgx.Tx, p ReserveParams) (Snapshot, error) {
	var zero Snapshot
	q := dbbilling.New(tx)

	reserve, err := q.ReserveBalanceHold(ctx, dbbilling.ReserveBalanceHoldParams{
		TenantID: p.TenantID,
		UserID:   p.UserID,
		Cost:     p.Cost,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ReserveBalanceHold 返 0 行有两种情形:无余额行(用户未启用余额强制)
			// vs 行在但 (balance-held)<cost(余额不足)。opt-in 策略(Owner 2026-05-28
			// 选 A):无行=未 provision=放行(不建 hold);有行才在不足时 402。
			exists, exErr := q.UserBalanceExists(ctx, dbbilling.UserBalanceExistsParams{
				TenantID: p.TenantID,
				UserID:   p.UserID,
			})
			if exErr != nil {
				return zero, fmt.Errorf("check balance row existence: %w", exErr)
			}
			if exists {
				return zero, ErrInsufficientBalance
			}
			// 未 provision 余额行 → 不纳入余额强制 → 放行(返 nil,不建 hold);
			// claim_gate 现有逻辑遇 nil 即继续,不 402。后续 settle 的 Capture
			// 因无 hold 行而 no-op。
			return zero, nil
		}
		return zero, fmt.Errorf("reserve balance hold: %w", err)
	}

	if err := q.UpsertBalanceHold(ctx, dbbilling.UpsertBalanceHoldParams{
		ClaimID:  p.ClaimID,
		TenantID: p.TenantID,
		UserID:   p.UserID,
		Amount:   p.Cost,
	}); err != nil {
		return zero, fmt.Errorf("upsert hold: %w", err)
	}

	return newSnapshot(reserve), nil
}

func Capture(ctx context.Context, tx pgx.Tx, claimID int64, actualCost decimal.Decimal) (Snapshot, error) {
	var zero Snapshot
	q := dbbilling.New(tx)

	hold, err := q.GetBalanceHoldForUpdate(ctx, claimID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil
		}
		return zero, fmt.Errorf("get hold: %w", err)
	}
	if hold.State != "held" {
		balance, err := q.GetUserBalance(ctx, dbbilling.GetUserBalanceParams{
			TenantID: hold.TenantID,
			UserID:   hold.UserID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil
			}
			return zero, fmt.Errorf("get user balance: %w", err)
		}
		return Snapshot{Balance: balance.Balance, Held: balance.Held}, nil
	}

	if _, err := q.CaptureBalanceHold(ctx, dbbilling.CaptureBalanceHoldParams{
		ClaimID: claimID,
		Actual:  actualCost,
	}); err != nil {
		return zero, fmt.Errorf("capture hold state transition: %w", err)
	}

	snap, err := q.ApplyBalanceHoldCapture(ctx, dbbilling.ApplyBalanceHoldCaptureParams{
		TenantID: hold.TenantID,
		UserID:   hold.UserID,
		Amount:   hold.Amount,
		Actual:   actualCost,
	})
	if err != nil {
		return zero, fmt.Errorf("apply captured hold: %w", err)
	}
	return Snapshot{Balance: snap.Balance, Held: snap.Held}, nil
}

func Release(ctx context.Context, tx pgx.Tx, claimID int64) (Snapshot, error) {
	var zero Snapshot
	q := dbbilling.New(tx)

	hold, err := q.GetBalanceHoldForUpdate(ctx, claimID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil
		}
		return zero, fmt.Errorf("get hold: %w", err)
	}
	if hold.State != "held" {
		balance, err := q.GetUserBalance(ctx, dbbilling.GetUserBalanceParams{
			TenantID: hold.TenantID,
			UserID:   hold.UserID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil
			}
			return zero, fmt.Errorf("get user balance: %w", err)
		}
		return Snapshot{Balance: balance.Balance, Held: balance.Held}, nil
	}

	if _, err := q.ReleaseBalanceHold(ctx, claimID); err != nil {
		return zero, fmt.Errorf("release hold state transition: %w", err)
	}

	snap, err := q.ApplyBalanceHoldRelease(ctx, dbbilling.ApplyBalanceHoldReleaseParams{
		TenantID: hold.TenantID,
		UserID:   hold.UserID,
		Amount:   hold.Amount,
	})
	if err != nil {
		return zero, fmt.Errorf("apply released hold: %w", err)
	}
	return Snapshot{Balance: snap.Balance, Held: snap.Held}, nil
}
