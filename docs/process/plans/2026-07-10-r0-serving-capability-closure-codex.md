# 2026-07-10 R0 薄能力闭合闸（Codex 独立计划）

> 独立性声明：本计划仅依据 Owner 本次任务书、HUAKAI 权威规则与任务指定的 HUAKAI 内部生产路径起草；起草前未读取同主题 Claude 计划。当前状态为“待并行计划交叉讨论与 Owner 批准”，本文件本身不授权执行实现。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “实现 R0「薄能力闭合闸」……只做薄闸：建一个最小 `ServingCapabilityContract` + 闭合检查 + 判别测试 + 管理端接线。” |
| Scope | 新建 `backend/internal/servingcapability`；最小修改 `backend/internal/adminhttp/catalog.go`、`backend/internal/adminhttp/provider_catalog_mutation_handler.go` 及其测试/依赖装配；只读取现有 provider、adapter/handler、model、pricing、pool、transport、credential acquisition/finalizer/health 来源。 |
| Out of scope | 不改 schema、money、auth-core、生产价格、外部协议实现；不补 Claude OAuth serving；不修 Antigravity wire；不建第四张运行事实清单；不 commit、不 push。 |
| Success criteria | 五条不变量均输出逐站结构化结果；目录不再把 collect-only/未闭合 family 伪装 ready；enabled 写入只接受当前进程真实闭合 family；价格缺口在发布/绑定预检期形成 `sellable=false`；三环境矩阵和删站点变异均判别性变红；指定门全部通过。 |
| Time estimate | 8–12 小时工程量，最长不超过 1.5 工程日；若现有来源接口不足导致工作膨胀，停在最小查询接口与 blocker 报告，不扩成平台。 |
| Blast radius | 新包为只读判定层；管理目录响应与 provider catalog 写校验会更严格。错误判定可能隐藏合法目录项或拒绝合法配置，因此先保持 disabled 配置可保存、只对 enabled 写入 fail-closed，并用真实 registry 矩阵锁定。 |
| Failure modes | 形成重复清单；启动时误阻断 experimental；依赖环；测试用宽松 stub 假绿；价格检查误入线上 reserve；既有配置被破坏。缓解：contract 只存不可推导产品意图；运行站点由接口查询；Evaluate 纯函数化；experimental 只标红禁真流量；sellability 仅接发布/绑定预检；已有 scaffold/retired 配置只读保留。 |
| Decision points | 若必须改 schema/money/auth-core 才能读取权威状态，立即停止该项阻断，降为结构化 `report_only` 缺口并交 Owner；本轮不替 Owner 决定新 release 状态，也不把 experimental 升 released。 |
| Pre-execution checklist | 见下文顺序清单；未完成合成计划与 Owner 批准前不得进入实现。 |

## 默认镜子登记与 clean-room 边界

- `REFERENCE PROJECTS IN SCOPE`: CLIProxyAPI + sub2api + new-api。
- 本任务书明确 R0 是 HUAKAI 内部真实来源接线，当前计划不读取上述非 HUAKAI 源码、不作其行为断言，也不从其函数名、字段名、目录或算法提取实现；因此不存在需要转译的外部实现证据。
- 若后续 Owner 要求进行外部机制对照，必须另起符合 lane guard 的 specifier 工作单；当前实现会保持 HUAKAI 内部盲实现边界。

## 形态清单（开写前）

### 路径

1. 账号模式目录：`ModePlan` / acquisition disposition → finalizer → 至少一个 released/experimental contract → UI readiness。
2. Provider 配置：管理端 enabled 写入 → 当前进程 registry → marshal/parser/scanner → pool vendor → transport policy → 接受或精确 4xx。
3. 账号可选：family/auth mode → adapter 接受的 runtime credential kind → expiry → 权威 health → eligible。
4. 模型发布：active model → active binding → eligible account + wire model → pricing parse → usage settle capability → sellable/publish verdict。
5. Setting 广告：setting key → 生产 consumer → effective value observer → advertised verdict。

### 模式与状态

- 环境模式：默认 env、单一 opt-in env、全部 opt-in env。
- ReleaseState：`scaffold`、`experimental`、`released`、`retired`。
- 失败姿态：released+enabled 可 fail-loud/拒写；experimental 不挡启动但禁真流量；scaffold/retired 隐藏且请求 fail-closed；价格缺失只在 sellability/publish 预检阻断。
- 固定产品意图：Claude OAuth session = `collectable_not_serving`；Antigravity session = `experimental_wire_unverified`。

### Actor

- 平台启动器/装配器：读取闭合报告决定 fail-loud 信号。
- 管理员：查看目录 readiness、创建或更新 provider 配置、发布/绑定模型。
- 请求路由：只消费已经闭合且 eligible 的能力，不在请求期首次发现可预检缺口。
- 运维：从结构化站点缺口定位缺失 adapter/parser/marshal/scanner/vendor/transport/pricing/consumer。

## 最小设计

### Contract 边界

`ServingCapabilityContract` 只声明无法从运行对象可靠推导的产品关系：

- family ↔ vendor；
- 允许的 auth mode 与 runtime credential kind；
- 现有 request marshal / response parse / stream framing shape 标识；
- `ReleaseState`、`MustPriceToSell`、`ModelDiscoveryScope`；
- readiness reason，用于钉死 `collectable_not_serving` 与 `experimental_wire_unverified`，不拿它替代站点检查。

contract 登记表覆盖 `registrydefault.SupportedProtocolFamilies()` 与目录已声明 family 的并集；测试要求每个理论 family 恰有一份意图声明，但不得复制 adapter/vendor/model/pricing 的运行事实。

### Evaluate 输入

用窄接口/函数适配真实来源，生产装配由现有对象提供：

- provider `StaticRegistry` 的当前 `RegisteredProtocolFamilies()` 与 `For()`；
- 现有 marshal、response parser、stream scanner registry 的查询能力；
- `pool.VendorFromProtocolFamily`；
- 现有 transport policy resolver/registry；
- credential acquisition plan、finalizer registry、runtime credential/expiry/health 视图；
- model registry/binding/account wire model 查询；
- 复用现有 pricing 解析链的只读 probe 与 usage-settle capability；
- setting consumer/effective observer 的现有登记或最小查询适配。

若某来源目前没有可注入查询接口，只增加“查询是否存在”的窄方法或 adapter，不复制内容列表。

### 结构化结果

- `CheckResult`：不变量 ID、subject、release state、severity/action、`Ready`、逐站 `StationResult`。
- `StationResult`：站点 ID、`Present`、reason、是否阻断、来源类别。
- 聚合报告保留所有缺口，不短路在第一个失败；管理端错误从同一报告生成稳定 error code 与 readiness reason。

## acceptance-test-writer 覆盖行

| Test ID | 正常路径 | 失败路径 | 运维恢复 | 咬住的回归 |
| --- | --- | --- | --- | --- |
| AT-R0-CAP-001 | finalizer、acquisition 与 released/experimental contract 齐全时目录 ready | 仅 finalizer 齐全时为 `collect_only` | 补齐并启用 contract 后 ready | “能采集”等同“能 serving” |
| AT-R0-CAP-002 | env on 且六个 serving 站点齐全时 enabled 写入成功 | env off 或任一站点删除时精确 4xx | 恢复 env/站点后同写入成功 | 理论支持集被误当当前进程能力 |
| AT-R0-CAP-003 | credential kind、expiry、health 全部允许时 account eligible | kind 不符、过期、health 拒绝分别列站点缺口 | refresh/健康恢复后 eligible | 账号存在即被路由 |
| AT-R0-CAP-004 | model/binding/account/wire/pricing/settle 全闭合时 sellable | 删除价格 fixture 时 test-only 且 publish/bind 失败 | 恢复价格后预检通过 | discovered 被误当 sellable，延迟到 reserve 才 503 |
| AT-R0-CAP-005 | consumer 与 effective observer 均存在时 setting advertised | 删除 consumer 或 observer 时不 advertised | 恢复接线后 advertised | 配置项只有 UI/存储，没有生产效果 |
| AT-R0-CAP-006 | 默认/单开/全开 env 的 visible、enableable、serving 结果与真实 registry 一致 | 理论 family 在 env off 时仍被拒 | 打开对应 env 后仅闭合 family 转绿 | 环境门与管理端目录漂移 |

## 具体执行顺序

1. 验明分支/HEAD/dirty worktree；运行协调看板并认领所有计划修改的文件。
2. 读取指定 HUAKAI 生产路径及其直接依赖，画出真实 registry 查询表；先确定是否有依赖环和哪些检查只能 `report_only`。
3. 新建 `internal/servingcapability`：类型/枚举、最小静态 product-intent contract、真实来源接口、五条 Evaluate 与结果聚合。
4. 写包内自证测试：每条同时跑完整 fixture 与缺站 fixture，并断言结果发生目标变化；钉死两条产品事实。
5. 接 `adminhttp` 目录 readiness 与 provider enabled 写校验；disabled 写入继续允许只读保留；错误码与 reason 精确断言。
6. 只在现有 model publish/binding 预检边界接 sellability probe；若该边界会触及 money/schema，则不改路径，保留可调用 report 并登记 blocker。
7. 写默认/单开/全开 env 三矩阵；使用生产 `registrydefault.Build()`，不以手造“全有” stub 代替当前进程事实。
8. 先跑基线测试，再逐项临时删除 adapter、parser、marshal、scanner、vendor、price fixture；每次确认目标测试失败并记录输出，随后用反向补丁恢复并重跑绿色。不得用 `git checkout/reset` 覆盖他人改动。
9. 运行 `gofmt`、定向 test、`go build ./...`、`go vet ./...`、标准 codebudget unit gate；若安装了 staticcheck，设置 `GOFLAGS=-buildvcs=false` 后运行。
10. 检查 `git diff` 仅含本任务认领文件；释放协调锁；输出中文报告，不 commit、不 push。

## 预执行检查清单

- [ ] Claude 独立计划已存在，且 Codex 独立计划此前未读它。
- [ ] 两份计划已完成 agreements / conflicts / gaps 交叉讨论。
- [ ] Owner 已批准无后缀合成计划或带合成标头的权威计划。
- [ ] 当前分支为 `feat/fe-wire-users-mod`，基点仍可追溯到 `e8379873`。
- [ ] 工作树既有变更已盘点并隔离，不覆盖其他 agent 文件。
- [ ] 所有拟改文件已通过 `.coordination/check.sh` 并成功 claim。
- [ ] 不需要 schema/money/auth-core/新 runtime dependency。
- [ ] contract 没有复制可从真实 registry 推导的事实。
- [ ] 每个测试都写明实际破坏点与应失败断言。

## 当前结论

计划可以在薄闸边界内完成，但依规则尚不可执行。下一步必须先读取 Claude 独立计划、形成 agreements/conflicts/gaps，并由 Owner 批准合成计划。
