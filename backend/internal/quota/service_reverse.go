package quota

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

const quotaAuditOperationCostReversed = "cost_reversed"

// ReverseCostRequest 描述一次成本冲减:把某 claim 已结算的成本从它命中的成本窗 settled_value 里
// 负向冲减 Amount(USD)。用途=修补"对账退款只退了钱包、配额计数没退":退款后按退款的实际金额把
// settled_value 同步降回,避免用户净花费已降却因旧计数提前撞成本上限被拒(过度强制)。
type ReverseCostRequest struct {
	TenantID int64
	ClaimID  int64
	Amount   decimal.Decimal // 要冲减的成本金额(USD);<=0 视为 no-op
	Actor    *string
}

// ReverseCostResult 报告冲减结果。
type ReverseCostResult struct {
	Reservation   Reservation
	ReversedValue decimal.Decimal // 各成本窗实际冲减额之和(按各窗当前 settled_value 钳制后)
	Skipped       bool            // 预留不存在 / 非 settled 态 / 无成本窗 / 钳制后无可冲减 → 跳过
}

// ReverseCost 把某已结算 claim 的成本从它命中的成本窗 settled_value 负向冲减 req.Amount。
//   - 本方法走 quota 自管事务，供无法共享事务的兼容调用方使用；正式退款链使用
//     ReverseCostInTx，把钱包、账单和配额冲减放进同一调用方事务。
//   - 仅对 ReservationSettled 的预留冲减(未结算的没有 settled_value 可退)。
//   - 逐个 MetricCostUSD 窗按其当前 settled_value 钳制冲减额:DB 有 CHECK settled_value>=0,绝不冲到
//     负数;若该窗已滚动重置/被清理,则当前 settled 较小、钳制后少冲或不冲——均无害(旧窗不再 gate
//     当前请求,过度计数本就只在"退款与结算落在同一窗"时才伤人,此时该窗仍在、会被正确冲减)。
//   - overage 是高水位追踪量、不参与强制门(强制看 reserved+settled vs limit),故不反冲。
//   - 预留已不存在(已被清理)→ 返回 Skipped,不当失败。
func (s *Service) ReverseCost(ctx context.Context, req ReverseCostRequest) (ReverseCostResult, error) {
	var result ReverseCostResult
	if req.TenantID <= 0 || req.ClaimID <= 0 || req.Amount.Sign() <= 0 {
		result.Skipped = true
		return result, nil
	}
	err := s.runQuotaFinalizationWithRetry(ctx, "reverse_cost", defaultFinalizationRetryPolicy, func(tx PGStore) error {
		next, err := reverseCostWithStore(ctx, tx, req)
		result = next
		return err
	})
	if err != nil {
		if errors.Is(err, ErrReservationNotFound) {
			return ReverseCostResult{Skipped: true}, nil
		}
		return result, err
	}
	return result, nil
}

// ReverseCostInTx 把配额成本冲减加入调用方事务；调用方负责提交、回滚和事务冲突重试。
func (s *Service) ReverseCostInTx(ctx context.Context, tx pgx.Tx, req ReverseCostRequest) (ReverseCostResult, error) {
	if s == nil || tx == nil {
		return ReverseCostResult{}, ErrStoreNotConfigured
	}
	return reverseCostWithStore(ctx, NewPostgresStore(tx), req)
}

func reverseCostWithStore(ctx context.Context, store PGStore, req ReverseCostRequest) (ReverseCostResult, error) {
	result := ReverseCostResult{}
	if req.TenantID <= 0 || req.ClaimID <= 0 || req.Amount.Sign() <= 0 {
		result.Skipped = true
		return result, nil
	}
	reservation, err := getFinalizationReservation(ctx, store, finalizationReservationInput{
		TenantID:  req.TenantID,
		ClaimID:   req.ClaimID,
		Operation: quotaAuditOperationCostReversed,
		Actor:     req.Actor,
	})
	if errors.Is(err, ErrReservationNotFound) {
		return ReverseCostResult{Skipped: true}, nil
	}
	if err != nil {
		return result, err
	}
	result.Reservation = reservation
	if reservation.Status != ReservationSettled {
		result.Skipped = true
		return result, nil
	}
	reversed, err := reverseCostSettlementWindows(ctx, store, reservation, req.Amount)
	if err != nil {
		return result, err
	}
	result.ReversedValue = reversed
	if reversed.Sign() <= 0 {
		result.Skipped = true
		return result, nil
	}
	// 复用 settled 事件族；负 amount_settled 与专属 decision_code 共同表达冲减。
	if err := insertQuotaFinalizationAudit(ctx, store, quotaFinalizationAudit{
		Reservation:   reservation,
		Operation:     quotaAuditOperationCostReversed,
		EventType:     "settled",
		DecisionCode:  "quota_cost_reversed",
		Metric:        MetricCostUSD,
		AmountSettled: reversed.Neg(),
		Actor:         req.Actor,
		ExtraPayload: map[string]any{
			"requested_reverse": req.Amount.String(),
			"applied_reverse":   reversed.String(),
		},
	}); err != nil {
		return result, err
	}
	return result, nil
}

// reverseCostSettlementWindows 对 reservation 命中的每个 MetricCostUSD 窗,把 settled_value 负向
// 冲减 amount(按各窗当前 settled_value 钳制,绝不为负)。返回各窗实际冲减额之和。
func reverseCostSettlementWindows(ctx context.Context, store PGStore, reservation Reservation, amount decimal.Decimal) (decimal.Decimal, error) {
	if amount.Sign() <= 0 {
		return decimal.Zero, nil
	}
	policies, err := snapshotFinalizationPolicies(reservation)
	if err != nil {
		return decimal.Zero, err
	}
	total := decimal.Zero
	for _, policy := range policies {
		if policy.Metric != MetricCostUSD {
			continue
		}
		counter, err := snapshotWindowForUpdate(ctx, store, reservation.TenantID, policy)
		if err != nil {
			return decimal.Zero, err
		}
		dec := amount
		if dec.GreaterThan(counter.SettledValue) {
			dec = counter.SettledValue // 钳制:最多冲到 0,满足 DB CHECK settled_value>=0
		}
		if dec.Sign() <= 0 {
			continue
		}
		if _, err := store.ApplyWindowSettlement(ctx, WindowSettlement{
			TenantID:             reservation.TenantID,
			WindowID:             counter.ID,
			ReservedReleaseValue: decimal.Zero, // 预留早在 settle 时已释放,不动
			SettledAddValue:      dec.Neg(),    // 负向冲减 settled_value
			OverageAddValue:      decimal.Zero, // overage 高水位、不 gate 强制,不反冲
		}); err != nil {
			return decimal.Zero, err
		}
		total = total.Add(dec)
	}
	return total, nil
}
