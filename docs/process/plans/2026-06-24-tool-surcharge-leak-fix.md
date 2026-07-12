# 工具附加费止漏装配(NAPI-BILLING-01 Stage A 收尾)

日期:2026-06-24
分支:feat/tool-surcharge-leak-fix(基于 origin/feat/frontend-portal @19806fbb)
核心特性域:billing

## 1. 范围(已亲核真码,范围明确,不擅自扩大)

工具附加费(web_search / file_search / image_generation 等服务端工具调用的上游附加成本)当前生产**漏钱(S1)**:
工具调用次数侧【已实时接通】,唯独价表在生产装配链路恒 nil,导致 `applyToolCallSurcharge` 提前返回、加 $0。

已核实事实:
- `backend/internal/gatewayhttp/chat_completions_pricing.go` 的 `usageFromBufferedEnvelope`(:334)/ `usageFromDraft`(:361)
  已把上游 usage 的 WebSearchCalls/FileSearchCalls/ImageGenerationCalls 填进 `ToolCallCounts`。**次数侧不动。**
- `backend/cmd/gateway/routes.go` 的 `chatHandlerDeps()`(:644-682)构造 `ChatHandlerDeps` 时
  **从不给 `ToolPricingTable` 赋值** → 生产恒 nil → `applyToolCallSurcharge`(chat_completions_pricing.go:250)
  在 `if ex.d.ToolPricingTable == nil { return result }` 提前返回 → 工具调用加 $0,我方付了上游钱却没向租户收。

本切片只补「价表装配」,不碰次数侧。

## 2. 改动清单(4 文件)

1. `backend/internal/toolpricing/toolpricing.go`(自有包,安全):
   - 新增接口 `type Source interface { Lookup(tenantID int64, modelID string) ToolPrices }`;现有 `Table` 已是值接收者 `Lookup`,天然满足 `Source`,不破坏。
   - 新增平台默认来源 `platformSource{ defaults ToolPrices; overrides Table }`:Lookup 先查 overrides(命中非零价用之),否则回落 defaults。
   - 导出构造器 `func NewPlatformSource(defaults ToolPrices, overrides Table) Source`。
   - defaults 用现有 `DefaultToolPrices()`(官方对齐:web_search $10/1000、file_search $2.5/1000、imggen $0)。
2. `backend/internal/gatewayhttp/chat_completions_handler.go:130`:
   - `ToolPricingTable toolpricing.Table` → `ToolPricingTable toolpricing.Source`。只改这一行字段类型,
     不新增文件到该结构纪律敏感包(codebudget 预算)。消费点 `== nil` 与 `.Lookup(...)` 对接口同样成立、无需改。
3. `backend/cmd/gateway/`(运维开关 + 生产装配):
   - 新增开关 `HUAKAI_TOOL_SURCHARGE_ENABLED`,**默认开**(unset → 启用),显式 "false"/"0" → 关闭(回到旧 $0 行为)。
     复用 wiring.go 现有 default-true 解析风格(`strconv.ParseBool`,malformed 即 fail-loud)。
   - `deps` 结构加字段 `toolPriceSource toolpricing.Source`;装配链路按开关构建:
     启用 → `toolpricing.NewPlatformSource(toolpricing.DefaultToolPrices(), nil)`;关闭 → nil。
   - `routes.go` 的 `chatHandlerDeps()` 接进:`ToolPricingTable: d.toolPriceSource`。

## 3. 成功标准

- `go build ./...`、`go vet`、受影响包 `go test -count=1` 全绿;codebudget 门绿。
- 现有 `ToolPricingTable: priceTable`(Table 满足 Source)与 `ToolPricingTable: nil` 测试仍编译、仍通过。
- 新增止漏测试矩阵 A-E 全绿,且逐条变异证伪(注入缺陷必变红)。

## 4. 止漏测试矩阵(每条先写正确路径,再注入缺陷验证变红)

- A. 漏装配回归守卫(最重要,直击原 bug):开关开时,真实生产装配构造出的 `ToolPricingTable` 非 nil。
     变异:删/改回 nil → 变红。
- B. 止漏端到端:开关开 + WebSearch>0 → applyToolCallSurcharge 后 Total 严格变大(按 $10/1000)。
     变异:source 改 nil/开关关 → Total 退回原值、变红。
- C. default-off 不破:开关关 → source nil → 字节等价原 $0 result;现有 :1325 default-off 测试仍绿。
- D. env 开关语义:unset→启用;"false"/"0"→关闭。变异:默认从 enabled 翻成 disabled → 变红。
- E. platformSource.Lookup:overrides 命中用 override 价、未命中回落 defaults;defaults 价正确($10/$2.5/$0)。
- 读 env 的 Go 测试用 `-count=1` 避免 test cache 假绿。

## 5. 计费默认翻转说明(重要)

本切片让工具调用从「$0」变成「按官方价计费」=**面向租户的计费默认翻转**。
Owner 已显式授权(「接吧」/「工具附加费 S1 坐实要做」),默认 ON 跟 new-api 范本走。
保留 `HUAKAI_TOOL_SURCHARGE_ENABLED=false`(或 "0")运维退路,关闭即恢复旧 $0 行为。
commit body 与报告显著标注此翻转。

## 6. blast radius

- toolpricing 是自有小包,新增接口/实现不动既有 Surcharge 计算逻辑。
- gatewayhttp 仅改一行字段类型(Table → Source),接口方法签名一致,消费点零改动。
- cmd/gateway 加一个 env 开关 + 一个 deps 字段 + 一处 chatHandlerDeps 赋值;关闭时退回 nil = 旧行为。
- 影响面仅限「带服务端工具调用的 completion 计费」;无工具调用的请求 ToolCallCounts 全零,Surcharge 恒 0,不受影响。

## 7. clean-room

- new-api 仅作价目表数值来源参照(DefaultToolPrices 注释已引)。禁止照搬任何 new-api 标识符/函数名/代码/注释。
- Source / platformSource / NewPlatformSource 均为 HUAKAI 自有命名。
