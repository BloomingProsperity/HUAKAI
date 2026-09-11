// Package servicetier 把上游处理档收成唯一结算合同：按实档计价、只降不升，
// 未公布档不得发明倍率，也不得把暂记 1× 标成已结清。
package servicetier

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Decision 是一笔请求的处理档结算结论。字面量只保留规范化后的档名，不含秘密。
type Decision struct {
	Requested  string
	Actual     string
	Billed     string
	Multiplier decimal.Decimal
	Pending    bool
	Reason     string
}

type laneClass int

const (
	laneAuto laneClass = iota
	laneStandard
	laneFlex
	laneFast
	laneUnpublished
)

var (
	one      = decimal.NewFromInt(1)
	half     = decimal.RequireFromString("0.5")
	two      = decimal.NewFromInt(2)
	standard = "default"
	flexLane = "flex"
	fastLane = "fast"
	autoLane = "auto"
)

// Reserve 按客户请求档预留金额上限。auto/空/未公布档按 1×，避免把所有人预扣抬到 2×。
func Reserve(requested string) Decision {
	class, literal := classify(requested)
	out := Decision{Requested: literal, Multiplier: one, Billed: standard, Reason: "reserve_standard"}
	if class == laneFlex || class == laneFast || class == laneStandard {
		out.Multiplier = publishedRate(class)
		out.Billed = billedLiteral(class, literal)
		out.Reason = "reserve_requested"
	}
	return out
}

// Settle 按「客户上限 ∩ 上游实档」结算。请求为 auto/空时没有上限，跟实档走。
func Settle(requested, actual string) Decision {
	reqClass, reqLit := classify(requested)
	actClass, actLit := classify(actual)
	out := Decision{Requested: reqLit, Actual: actLit, Multiplier: one, Billed: standard}

	if actLit != "" && actClass == laneUnpublished {
		out.Billed = actLit
		out.Pending = true
		out.Reason = "unpublished_actual"
		if reqClass == laneFlex || reqClass == laneStandard {
			out.Multiplier = publishedRate(reqClass)
			out.Billed = billedLiteral(reqClass, reqLit)
			out.Reason = "capped_below_unpublished_actual"
		}
		return out
	}

	if actLit == "" || actClass == laneAuto {
		if reqClass == laneUnpublished {
			out.Billed = reqLit
			out.Pending = true
			out.Reason = "unpublished_requested_missing_actual"
			return out
		}
		if reqClass == laneAuto || reqClass == laneStandard {
			out.Reason = "missing_actual_standard"
			return out
		}
		out.Multiplier = publishedRate(reqClass)
		out.Billed = billedLiteral(reqClass, reqLit)
		out.Pending = true
		out.Reason = "missing_actual"
		return out
	}

	actRate := publishedRate(actClass)
	if reqClass == laneAuto {
		out.Billed = actLit
		out.Multiplier = actRate
		out.Reason = "actual"
		return out
	}
	if reqClass == laneUnpublished {
		out.Pending = true
		out.Reason = "unpublished_requested"
		if actRate.LessThan(one) {
			out.Billed = actLit
			out.Multiplier = actRate
			return out
		}
		out.Billed = standard
		out.Multiplier = one
		return out
	}

	reqRate := publishedRate(reqClass)
	if reqRate.LessThan(actRate) {
		out.Billed = billedLiteral(reqClass, reqLit)
		out.Multiplier = reqRate
		out.Reason = "capped_to_requested"
		return out
	}
	out.Billed = actLit
	out.Multiplier = actRate
	out.Reason = "actual"
	return out
}

func classify(raw string) (laneClass, string) {
	literal := normalizeLiteral(raw)
	switch literal {
	case "":
		return laneAuto, ""
	case autoLane:
		return laneAuto, autoLane
	case "default", "standard":
		return laneStandard, literal
	case flexLane:
		return laneFlex, flexLane
	case fastLane, "priority":
		return laneFast, literal
	default:
		return laneUnpublished, literal
	}
}

func publishedRate(class laneClass) decimal.Decimal {
	switch class {
	case laneFlex:
		return half
	case laneFast:
		return two
	default:
		return one
	}
}

func billedLiteral(class laneClass, literal string) string {
	if strings.TrimSpace(literal) != "" {
		return literal
	}
	switch class {
	case laneFlex:
		return flexLane
	case laneFast:
		return fastLane
	default:
		return standard
	}
}

func normalizeLiteral(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
