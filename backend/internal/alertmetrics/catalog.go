package alertmetrics

// CatalogEntry 描述一个可供告警规则使用的生产指标。
// IsPrefix 为 true 时，Name 是指标名前缀，运营者需要补全具体后缀。
type CatalogEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
	IsPrefix    bool   `json:"is_prefix"`
}

var catalogEntries = []CatalogEntry{
	{Name: MetricUsageRequestCount, Label: "请求总数", Unit: "次", Description: "统计窗口内已结算的请求总数。"},
	{Name: MetricUsageRequestRatePerMinute, Label: "每分钟请求率", Unit: "次/分钟", Description: "统计窗口内请求总数折算出的每分钟平均请求数。"},
	{Name: MetricUsageSuccessCount, Label: "成功请求数", Unit: "次", Description: "统计窗口内已结算的成功请求数。"},
	{Name: MetricUsageSuccessRate, Label: "请求成功率", Unit: "比例", Description: "统计窗口内成功请求数占请求总数的比例，取值范围为 0 到 1。"},
	{Name: MetricUsageErrorCount, Label: "错误请求数", Unit: "次", Description: "统计窗口内已结算的错误请求数。"},
	{Name: MetricUsageErrorRate, Label: "请求错误率", Unit: "比例", Description: "统计窗口内错误请求数占请求总数的比例，取值范围为 0 到 1。"},
	{Name: MetricUsageTotalCostUSD, Label: "用量总成本", Unit: "美元", Description: "统计窗口内已结算请求的总成本。"},
	{Name: MetricUsageLatencyP95MS, Label: "首字节延迟 P95", Unit: "毫秒", Description: "统计窗口内请求首字节延迟的第 95 百分位值。"},
	{Name: MetricUsageLatencyP99MS, Label: "首字节延迟 P99", Unit: "毫秒", Description: "统计窗口内请求首字节延迟的第 99 百分位值。"},
	{Name: MetricAccountUnhealthyCount, Label: "异常账号总数", Unit: "个", Description: "当前被自动摘除且仍处于非健康状态的账号总数。"},
	{Name: MetricAccountUnhealthyPrefix, Label: "按状态统计异常账号", Unit: "个", Description: "后接健康状态，如 account.unhealthy_throttled。", IsPrefix: true},
}

// CatalogEntries 返回告警指标目录的副本，调用方不能改写进程内的单一真相源。
func CatalogEntries() []CatalogEntry {
	out := make([]CatalogEntry, len(catalogEntries))
	copy(out, catalogEntries)
	return out
}
