package pricingeval

import "github.com/shopspring/decimal"

// CacheCostOverride 描述对一次计费结果里"缓存读/写"两段成本施加的相对官方价倍率。
//
// 倍率是相对官方价的乘数(multiplier),默认 1.0 = 不覆盖 = 官方价。
// 该结构故意只触碰 cache 两段(CacheCreationCost / CacheReadCost),
// 不动 input/output 价,以保证非缓存路径计费零变化。
type CacheCostOverride struct {
	// Multiplier 是相对官方缓存价的倍率;<=0 视为未设置(按 1.0 处理),
	// 保证 money 路径保守:任何异常输入都退回官方价而非把成本清零。
	Multiplier decimal.Decimal
}

// effectiveMultiplier 返回安全可用的倍率:未设置/非正一律按 1.0。
func (o CacheCostOverride) effectiveMultiplier() decimal.Decimal {
	if o.Multiplier.IsPositive() {
		return o.Multiplier
	}
	return decimal.NewFromInt(1)
}

// IsIdentity 报告该覆盖是否等价于"不覆盖"(倍率为 1 或未设置)。
func (o CacheCostOverride) IsIdentity() bool {
	return o.effectiveMultiplier().Equal(decimal.NewFromInt(1))
}

// ApplyCacheCostOverride 在既有计费结果之上,对缓存读/写成本施加倍率,
// 并按"缓存成本变化量"等额调整 Total。非缓存成本分量不受影响。
//
// 默认行为零变化:倍率 1.0(或未设置)时原样返回 res,Total 不动。
// 这是缓存价覆盖的唯一计费施加点,调用方应在拿到官方价 Result 后调用本函数。
func ApplyCacheCostOverride(res Result, override CacheCostOverride) Result {
	mult := override.effectiveMultiplier()
	if mult.Equal(decimal.NewFromInt(1)) {
		return res
	}
	newCacheCreation := res.CacheCreationCost.Mul(mult)
	newCacheRead := res.CacheReadCost.Mul(mult)
	delta := newCacheCreation.Sub(res.CacheCreationCost).Add(newCacheRead.Sub(res.CacheReadCost))
	res.CacheCreationCost = newCacheCreation
	res.CacheReadCost = newCacheRead
	res.Total = res.Total.Add(delta)
	return res
}
