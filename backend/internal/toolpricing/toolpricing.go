package toolpricing

import "github.com/shopspring/decimal"

// ToolCallCounts 持有单次 completion 中发生的服务端内置工具调用次数。
// 所有字段默认为零,表示不收附加费。
//
// 上游(OpenAI / Anthropic)对内置工具(web_search、file_search、
// image_generation)按 per-CALL 收取一笔与 token 无关的固定费用。
// 本 struct 携带抽取出的调用次数,供附加费步骤计算增量。
type ToolCallCounts struct {
	WebSearch       int64
	FileSearch      int64
	ImageGeneration int64
}

// IsZero 报告所有调用次数是否都为零(没有工具调用 = 不收附加费)。
func (c ToolCallCounts) IsZero() bool {
	return c.WebSearch == 0 && c.FileSearch == 0 && c.ImageGeneration == 0
}

// ToolPrices 持有单个 (tenant, model) 配置条目下每个工具按每 1000 次调用
// 计的美元单价。零价表示该工具未定价(对该工具不收附加费)。
type ToolPrices struct {
	// WebSearchPer1000 是每 1000 次 web_search 工具调用的美元收费。
	WebSearchPer1000 decimal.Decimal
	// FileSearchPer1000 是每 1000 次 file_search 工具调用的美元收费。
	FileSearchPer1000 decimal.Decimal
	// ImageGenerationPer1000 是每 1000 次 image_generation 调用的美元收费。
	ImageGenerationPer1000 decimal.Decimal
}

// IsZero 报告所有价格是否都为零(不会加任何附加费)。
func (p ToolPrices) IsZero() bool {
	return p.WebSearchPer1000.IsZero() &&
		p.FileSearchPer1000.IsZero() &&
		p.ImageGenerationPer1000.IsZero()
}

// Table 把 (tenantID, modelID) 映射到 ToolPrices。空 Table(nil 或无条目)
// 对每次查询都返回零价,保证默认行为是不收附加费。
type Table map[tableKey]ToolPrices

type tableKey struct {
	TenantID int64
	ModelID  string
}

// Lookup 返回给定 tenant 和 model 的 ToolPrices。若找不到条目,
// 返回零价(安全默认:不收附加费)。
func (t Table) Lookup(tenantID int64, modelID string) ToolPrices {
	if t == nil {
		return ToolPrices{}
	}
	return t[tableKey{TenantID: tenantID, ModelID: modelID}]
}

// Source 是工具附加费价表的查询契约。给定 (tenantID, modelID) 返回一组 ToolPrices;
// 找不到配置时必须返回零价(安全默认 = 不加附加费)。
//
// 抽出接口的目的:让 gatewayhttp 的消费点既能吃裸 Table(测试 / 纯 override 配置),
// 也能吃带平台默认回落的 platformSource(生产装配)。注意 Table 已有值接收者方法
// Lookup,天然满足 Source,无需任何改动即可作为 Source 传入。
type Source interface {
	Lookup(tenantID int64, modelID string) ToolPrices
}

// platformSource 是带「平台默认价 + 可选 per-(tenant,model) override」两层回落的 Source 实现。
//
// 解析顺序:
//  1. 若 overrides 命中且该条目非零价 → 用 override 价(运营者为特定租户/模型定的覆盖价)。
//  2. 否则 → 回落 defaults(平台默认价,通常来自 DefaultToolPrices())。
//
// 这样未配置任何 override 的生产环境也能按平台默认价对工具调用计费,
// 而不是像之前价表恒 nil 那样漏收(加 $0)。
type platformSource struct {
	// defaults 是无 override 命中时使用的平台默认价。
	defaults ToolPrices
	// overrides 是可选的 per-(tenant,model) 覆盖价;nil / 空 = 始终用 defaults。
	overrides Table
}

// Lookup 实现 Source:先查 overrides,命中非零价用之,否则回落 defaults。
func (s platformSource) Lookup(tenantID int64, modelID string) ToolPrices {
	if s.overrides != nil {
		if override := s.overrides.Lookup(tenantID, modelID); !override.IsZero() {
			return override
		}
	}
	return s.defaults
}

// NewPlatformSource 构造一个带平台默认价 + 可选 override 的 Source。
//
// defaults 通常传 DefaultToolPrices();overrides 传 nil 表示「全部走平台默认价」。
// 返回的 Source 永不为 nil:调用方据此判断「附加费已启用」。
func NewPlatformSource(defaults ToolPrices, overrides Table) Source {
	return platformSource{defaults: defaults, overrides: overrides}
}

// Set 为给定 tenant 和 model 存储价格。用于测试与加载配置。
func (t Table) Set(tenantID int64, modelID string, prices ToolPrices) {
	t[tableKey{TenantID: tenantID, ModelID: modelID}] = prices
}

var per1000 = decimal.NewFromInt(1000)

// Surcharge 计算工具调用附加费总额(美元)。
//
// 每个工具的公式:price_per_1000 / 1000 * count * groupRatio
//
// 保证「默认为零」安全属性的不变量:
//   - 次数为零  -> 附加费为零(无调用 = 不收费)
//   - 价格为零  -> 附加费为零(未配置 = 不收费)
//   - 空 Table  -> 附加费为零(无配置 = 不收费)
//
// groupRatio 与 pricingeval 的 GroupRatio 语义一致:零被当作
// 1.0(恒等),与 pricingeval.pricingGroupRatio 的行为相同。
func Surcharge(prices ToolPrices, counts ToolCallCounts, groupRatio decimal.Decimal) decimal.Decimal {
	if prices.IsZero() || counts.IsZero() {
		return decimal.Zero
	}
	ratio := effectiveGroupRatio(groupRatio)
	total := decimal.Zero
	total = total.Add(toolFee(prices.WebSearchPer1000, counts.WebSearch))
	total = total.Add(toolFee(prices.FileSearchPer1000, counts.FileSearch))
	total = total.Add(toolFee(prices.ImageGenerationPer1000, counts.ImageGeneration))
	return total.Mul(ratio)
}

// toolFee 为单个工具计算 price_per_1000 / 1000 * count。
// 当 count <= 0 或价格为零时返回零。
func toolFee(pricePer1000 decimal.Decimal, count int64) decimal.Decimal {
	if count <= 0 || pricePer1000.IsZero() {
		return decimal.Zero
	}
	return pricePer1000.Div(per1000).Mul(decimal.NewFromInt(count))
}

// effectiveGroupRatio 返回一个安全的 group ratio:零 -> 1(恒等)。
func effectiveGroupRatio(ratio decimal.Decimal) decimal.Decimal {
	if ratio.IsZero() {
		return decimal.NewFromInt(1)
	}
	return ratio
}

// DefaultToolPrices 返回平台级默认 ToolPrices,对应官方上游的计费表
//(美元每 1000 次调用,与 HUAKAI 计费 1:1 —— 无需 QuotaPerUnit 换算):
//
//   - WebSearchPer1000:  $10.00  (web_search 默认 10.0)
//   - FileSearchPer1000: $ 2.50  (file_search 默认 2.5)
//   - ImageGenerationPer1000: $0  (推迟到 Stage D —— 上游图像生成
//     价格随 model / size 变化,需要单独的价表)
//
// 想要 tenant 级 override 的调用方应使用 Table.Lookup;本函数是
// 零配置回落常量。
func DefaultToolPrices() ToolPrices {
	return ToolPrices{
		WebSearchPer1000:       decimal.NewFromFloat(10.0),
		FileSearchPer1000:      decimal.NewFromFloat(2.5),
		ImageGenerationPer1000: decimal.Zero, // Stage D:基于 model/size 的定价推迟
	}
}
