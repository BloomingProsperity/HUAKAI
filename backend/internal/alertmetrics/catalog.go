package alertmetrics

import "github.com/BloomingProsperity/HUAKAI/internal/otelbridge"

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

// bridgedMeta 是桥接指标的中文策展(标签/单位/描述)。键必须与 otelbridge 的桥接名一一对应,
// 完备性测试双向锁死:桥里新增指标漏策展、或策展了不存在的名字,都在测试期变红,不留到运维发现。
// 描述按辨识度标准写:说清什么事件驱动该值、值升高意味着什么。带 _total 的是进程启动以来累计值,
// 告警规则宜配合变化率语义使用;其余为当前值。
type bridgedCatalogMeta struct {
	Label       string
	Unit        string
	Description string
}

var bridgedMeta = map[string]bridgedCatalogMeta{
	"huakai_billing_resolver_db_fail_total":        {"计费配置:DB读失败", "次(累计)", "计费设置解析器读数据库失败的累计次数;升高=计费配置源不稳,可能回退陈旧缓存。"},
	"huakai_billing_resolver_stale_total":          {"计费配置:陈旧缓存兜底", "次(累计)", "计费设置刷新失败后用陈旧缓存应答的累计次数;持续升高=计费配置长期无法刷新。"},
	"huakai_dispatch_mode_default_total":           {"调度模式:default", "次(累计)", "以 default 模式完成选号调度的请求累计数。"},
	"huakai_dispatch_mode_shadow_total":            {"调度模式:shadow", "次(累计)", "以 shadow 影子模式完成选号调度的请求累计数。"},
	"huakai_dispatch_mode_canary_total":            {"调度模式:canary", "次(累计)", "以 canary 金丝雀模式完成选号调度的请求累计数。"},
	"huakai_dispatch_mode_pasr_primary_total":      {"调度模式:pasr_primary", "次(累计)", "以 PASR 主模式完成选号调度的请求累计数。"},
	"huakai_dispatch_mode_pasr_strict_total":       {"调度模式:pasr_strict", "次(累计)", "以 PASR 严格模式完成选号调度的请求累计数。"},
	"huakai_cache_creation_total":                  {"提示词缓存:写入token", "token(累计)", "上游提示词缓存创建 token 的累计量(缓存写入侧)。"},
	"huakai_cache_read_total":                      {"提示词缓存:命中token", "token(累计)", "上游提示词缓存读取 token 的累计量(缓存命中侧);与写入侧比值反映缓存收益。"},
	"huakai_group_policy_failclosed_total":         {"分组策略:真相不可用", "次(累计)", "订阅分组策略因存储故障停止选号的累计次数;升高=路由控制面异常，安全边界仍保持关闭。"},
	"huakai_budget_failopen_total":                 {"预算限流:fail-open放行", "次(累计)", "预算/速率执行因存储故障绕过限制放行请求的累计次数;升高=限流正在失守。"},
	"huakai_provider_error_total":                  {"渠道健康:错误转移", "次(累计)", "渠道健康因错误率/封禁进入 cooling_down 或 disabled 的累计转移次数;升高=上游账号在批量劣化。"},
	"huakai_provider_degraded_total":               {"渠道健康:降级转移", "次(累计)", "渠道健康进入 degraded 的累计转移次数。"},
	"huakai_dlq_pending_depth_high":                {"DLQ深度:高优先级", "行(当前)", "HIGH 车道待处理 DLQ 行数当前值;持续>0=钱相关恢复在积压。"},
	"huakai_dlq_pending_depth_med":                 {"DLQ深度:中优先级", "行(当前)", "MED 车道待处理 DLQ 行数当前值。"},
	"huakai_dlq_pending_depth_low":                 {"DLQ深度:低优先级", "行(当前)", "LOW 车道待处理 DLQ 行数当前值。"},
	"huakai_delivered_unsettled_count":             {"已交付未结算:行数", "行(当前)", "已把响应交付客户端但结算尚未闭合的持久恢复行数量;持续>0=有钱账悬挂待追平。"},
	"huakai_delivered_unsettled_age_seconds":       {"已交付未结算:最老滞留", "秒(当前)", "最老一条已交付未结算恢复行的滞留秒数;升高=结算恢复通道卡住。"},
	"huakai_runtime_heap_alloc_bytes":              {"运行时:堆内存", "字节(当前)", "Go 堆存活分配字节数当前值(进程内存预算信号)。"},
	"huakai_runtime_goroutines":                    {"运行时:goroutine数", "个(当前)", "存活 goroutine 数当前值;持续攀升=疑似泄漏。"},
	"huakai_runtime_uptime_seconds":                {"运行时:运行时长", "秒(当前)", "进程已运行秒数;频繁归零=崩溃循环/频繁重启。"},
	"huakai_billing_pricing_tiered_fallback_total": {"计价:阶梯降级平价", "次(累计)", "阶梯计价无法解析/求值而按平价兜底收费的累计次数;>0=有请求按兜底价收费待对账。"},
	"huakai_billing_pricing_flat_charged_total":    {"计价:按平价收费", "次(累计)", "按平价费率收费的请求累计数(阶梯降级比率分母之一)。"},
	"huakai_billing_pricing_tiered_charged_total":  {"计价:按阶梯收费", "次(累计)", "按阶梯费率收费的请求累计数(阶梯降级比率分母之一)。"},
	"huakai_cache_l2_hit_total":                    {"L2响应缓存:命中", "次(累计)", "L2 响应缓存命中累计次数(跨厂商/模型汇总)。"},
	"huakai_cache_l2_miss_total":                   {"L2响应缓存:未命中", "次(累计)", "L2 响应缓存未命中累计次数(跨厂商/模型汇总)。"},
	"huakai_cache_l2_size_bytes":                   {"L2响应缓存:体积", "字节(当前)", "L2 响应缓存总字节数当前值(跨厂商/模型汇总)。"},
	"huakai_egress_sidecar_dial_ok_total":          {"出口sidecar:隧道建立", "次(累计)", "出口 sidecar 成功建立隧道的累计次数(成功分子)。"},
	"huakai_egress_sidecar_dial_fail_total":        {"出口sidecar:拨号失败", "次(累计)", "拨 sidecar unix socket 失败的累计次数;升高=sidecar 进程不可用。"},
	"huakai_egress_sidecar_write_fail_total":       {"出口sidecar:控制帧写失败", "次(累计)", "向 sidecar 写控制帧失败的累计次数(sidecar_unavailable 的一种)。"},
	"huakai_egress_sidecar_read_fail_total":        {"出口sidecar:回执读失败", "次(累计)", "读 sidecar ack 帧失败的累计次数(sidecar_unavailable 的一种)。"},
	"huakai_egress_sidecar_rejected_total":         {"出口sidecar:拒绝", "次(累计)", "sidecar 明确拒绝(profile 不支持或上游/代理不可达)的累计次数。"},
}

// CatalogEntries 返回告警指标目录的副本:静态用量/健康条目 + 从 otelbridge 桥接规格派生的
// 运营指标条目(名字共用真相源,不会与真实指标漂移)。调用方不能改写进程内目录。
func CatalogEntries() []CatalogEntry {
	bridged := otelbridge.BridgedCounterCatalog()
	out := make([]CatalogEntry, 0, len(catalogEntries)+len(bridged))
	out = append(out, catalogEntries...)
	for _, info := range bridged {
		entry := CatalogEntry{Name: info.Name, Label: info.Name, Unit: "值", Description: info.Description}
		if meta, ok := bridgedMeta[info.Name]; ok {
			entry.Label, entry.Unit, entry.Description = meta.Label, meta.Unit, meta.Description
		}
		out = append(out, entry)
	}
	return out
}
