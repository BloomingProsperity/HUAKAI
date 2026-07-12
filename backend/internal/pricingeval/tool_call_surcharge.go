package pricingeval

import (
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

// ApplyToolCallSurcharge 把内置的工具调用附加费叠加到一个已有的
// 计费 Result 上。它是一个对 Total 做加法调整的器件,形态与
// ApplyCacheCostOverride 一致:未配置时严格空操作。
//
// 默认为零的安全不变量(要使附加费非零,以下全部必须成立):
//   - Usage.ToolCallCounts 必须非零(至少调用了一个工具)
//   - Table 查表返回的 prices 必须非零(租户已选择启用)
//
// 任一条件不成立时,函数原样返回 res ——
// Total 与 Resolve() 产出的仅 token 结果逐字节相同。
//
// groupRatio 透传给 toolpricing.Surcharge;零被当作 1.0
// (与 pricingeval.pricingGroupRatio 语义一致)。
func ApplyToolCallSurcharge(res Result, prices toolpricing.ToolPrices, counts toolpricing.ToolCallCounts, groupRatio decimal.Decimal) Result {
	if prices.IsZero() || counts.IsZero() {
		return res
	}
	delta := toolpricing.Surcharge(prices, counts, groupRatio)
	if delta.IsZero() {
		return res
	}
	res.Total = res.Total.Add(delta)
	return res
}
