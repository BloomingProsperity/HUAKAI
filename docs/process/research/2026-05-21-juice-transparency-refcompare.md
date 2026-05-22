# 2026-05-21 juice 透明版模型映射/替换透明度参照对比

| 项 | 内容 |
|---|---|
| 任务 | 对比 sub2api 与 CLIProxyAPI 的模型映射、替换、用户透明度，作为 HUAKAI juice 透明版开工前 specifier-lane 调研 |
| Clean-room lane | specifier；只读本地源码，行为摘要用中文转述，不复制上游代码/注释/实现结构 |
| 观察区域 | 46 个源码/配置/前端/测试区域 |
| 合理推断 | 8 条，均标注为从已读区域推出 |
| Open questions | 5 条 |

## 1. 两 repo 版本口径

- sub2api：按 Owner 指定的本地最新版目录 `/home/codex/refs/sub2api-latest/` 作为版本口径；本次没有下载、拉取或执行 git 操作。
- CLIProxyAPI：按 Owner 指定的本地最新版目录 `/home/codex/refs/CLIProxyAPI-latest/` 作为版本口径；本次没有下载、拉取或执行 git 操作。
- 下文引用格式为 `repo@顶层目录名:path:line`。顶层目录名分别固定为 `sub2api-latest` 与 `CLIProxyAPI-latest`。

## 2. sub2api 模型映射/替换/透明度细节

### 2.1 模型名映射/别名

sub2api 有多层模型映射入口。账号层会从账号凭据中读取管理员配置的映射表，并缓存解析结果；Antigravity 账号在没有自定义映射时会自动落到内置默认映射。这个默认表包含直通项，也包含旧模型迁移、预览版到稳定目标、以及无同族支持时转向其他同级/近似模型的替代关系。证据：`sub2api@sub2api-latest:backend/internal/service/account.go:446`、`sub2api@sub2api-latest:backend/internal/service/account.go:479`、`sub2api@sub2api-latest:backend/internal/domain/constants.go:70`。

账号层匹配支持精确匹配和通配符匹配；没有映射表时允许请求模型原样通过，有映射表时只接受命中的请求模型，并返回映射目标。Gemini/Antigravity 还有一个请求名归一化分支，用于把特定变体按配置键匹配。证据：`sub2api@sub2api-latest:backend/internal/service/account.go:572`、`sub2api@sub2api-latest:backend/internal/service/account.go:586`、`sub2api@sub2api-latest:backend/internal/service/account.go:611`、`sub2api@sub2api-latest:backend/internal/service/account.go:625`。

除账号层外，sub2api 还有渠道层映射。渠道缓存把“分组 + 平台 + 请求模型”展开成快速查找结构，映射规则同样支持精确和通配符，并按平台隔离，避免不同平台同名模型互相污染。映射结果还携带渠道 ID、是否发生映射、以及计费模型来源。证据：`sub2api@sub2api-latest:backend/internal/service/channel_service.go:74`、`sub2api@sub2api-latest:backend/internal/service/channel_service.go:232`、`sub2api@sub2api-latest:backend/internal/service/channel_service.go:367`、`sub2api@sub2api-latest:backend/internal/service/channel_service.go:485`。

渠道映射命中后，请求进入上游前会把请求体中的模型改成渠道目标模型。也就是说，sub2api 的路由层确实会把“调用方请求的模型”改为“HUAKAI 类系统实际发给上游的模型”。证据：`sub2api@sub2api-latest:backend/internal/handler/gateway_handler.go:743`。

管理员配置面提供渠道映射规则编辑，表现为来源模型到目标模型的配置项；同一渠道还可以选择按请求模型、渠道映射模型或最终上游模型计费，并在保存时把映射与计费来源一起提交。前端还做了映射源模式冲突检查，避免规则重叠。证据：`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:372`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:713`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:1468`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:1518`。

还有两个易漏入口：一是特定 compact 响应路径有独立的模型映射，不影响普通响应路径；二是兼容 Anthropic Messages 的 OpenAI 分发场景有按模型族/精确模型的分发目标配置。前者证据：`sub2api@sub2api-latest:backend/internal/service/account.go:700`。后者本次在 `backend/internal/service/openai_messages_dispatch.go` 与 `frontend/src/views/admin/groupsMessagesDispatch.ts` 读到，但该功能与本报告核心“调用方透明显示实际模型链”只间接相关，归入吸收建议。

### 2.2 模型替换 / fallback

Antigravity 的 Claude 兼容入口会先保存原请求模型，再解析账号映射目标；如果映射目标为空，会直接返回模型不在允许范围内的错误；若思考模式开启，目标模型可能再追加思考版本后缀，然后转换为上游格式发出。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1353`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1362`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1367`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1398`。

Antigravity 原生入口还存在一次性 fallback：当上游返回模型不存在且管理员开启模型兜底时，会用平台兜底模型重试一次。证据只显示服务端日志和重试动作，未观察到向调用方注入专门的替换通知字段或响应头。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2212`。

### 2.3 对调用方的透明度

关键点：sub2api 的 Antigravity Claude 非流式转换响应会把最终响应中的模型设为原请求模型，而不是映射后的上游模型。请求转发结果也同时保存“展示模型/原请求模型”和“上游计费模型”，但响应给调用方的 body 在该路径上不是上游真实模型。证据：`sub2api@sub2api-latest:backend/internal/pkg/antigravity/response_transformer.go:257`、`sub2api@sub2api-latest:backend/internal/pkg/antigravity/response_transformer.go:299`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1748`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1758`。

Gemini 原生 Antigravity 路径的转发结果也保留原请求模型作为主展示模型，同时把映射/计费目标作为上游模型字段保存。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2448`、`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2453`。

因此，sub2api 不是“对最终调用方完全透明披露模型映射”。更准确的判断是：后台和使用记录里已经具备映射链与上游模型的观测基础，但普通用户/API 调用方默认看到的仍偏向请求模型或兼容展示模型。普通用户 DTO 会把模型展示为请求模型，并不带管理员侧的上游模型/映射链字段；测试也覆盖了“用户 JSON 不包含上游模型，管理员 JSON 包含上游模型”的行为。证据：`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:567`、`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:635`、`sub2api@sub2api-latest:backend/internal/handler/dto/mappers_usage_test.go:110`。

普通用户 usage 页面列和导出也只展示单一模型列，没有读到用户侧展示“请求 → 映射 → 上游”的链条。证据：`sub2api@sub2api-latest:frontend/src/views/user/UsageView.vue:593`、`sub2api@sub2api-latest:frontend/src/views/user/UsageView.vue:915`。

本次没有在 sub2api 已读区域观察到类似“模型已替换”专用响应头，或在普通 API 响应 body 中附加独立透明字段的机制。这个结论是基于已读 gateway、Antigravity 转换、usage DTO 与前端 usage 路径的观察；不能排除其他边缘路径有非核心字段。

### 2.4 管理员面板 / 日志展示

sub2api 的强项在管理员可观测性。使用记录 schema 存储请求模型、上游模型、渠道 ID 和映射链；写 usage 时会保留渠道映射链，并在上游模型不同于展示模型时保存上游模型。证据：`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:41`、`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:50`、`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:56`、`sub2api@sub2api-latest:backend/internal/service/openai_gateway_service.go:5471`、`sub2api@sub2api-latest:backend/internal/service/openai_gateway_service.go:5520`。

管理员 usage 页面已经能按请求模型、上游模型、映射模型三类来源切换模型分布统计；导出也包含上游模型列。证据：`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:26`、`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:156`、`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:364`、`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:489`。

运维错误日志表在请求模型与上游模型不一致时展示“请求 → 上游”的小链条，并用 tooltip 展示完整关系。证据：`sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue:99`、`sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue:234`。

### 2.5 易漏小功能

- 精确 + 通配符映射：账号层和渠道层都支持，渠道层还按平台隔离。证据：`sub2api@sub2api-latest:backend/internal/service/account.go:586`、`sub2api@sub2api-latest:backend/internal/service/channel_service.go:397`。
- 映射链持久化：能形成“请求 → 渠道目标 → 上游目标”或“请求 → 上游目标”的链。证据：`sub2api@sub2api-latest:backend/internal/service/channel_service.go:103`。
- 计费模型来源可选：管理员可以选择按请求、渠道映射或最终上游模型计费。证据：`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:713`。
- 用户/管理员可见性分层：管理员 DTO 有上游模型与映射链，普通用户 DTO 没有。证据：`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:567`、`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:635`。
- fallback 只在服务端路径可见，已读路径未观察到用户可见替换通知。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2212`。

## 3. CLIProxyAPI 模型映射/替换/透明度细节

### 3.1 模型名映射/别名

CLIProxyAPI 有两类相关机制：全局 OAuth 模型别名，以及 Amp CLI 专用模型映射。全局 OAuth 别名会影响受支持 OAuth/文件型账号通道的模型列表和请求路由，但不覆盖若干已有的每凭据别名机制。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:138`。

OAuth 别名语义是把上游模型暴露成客户端可见别名；如果配置为分叉展示，则模型列表保留原模型并额外加入别名，否则列表中可只出现别名。服务层会在模型列表中改写/增加模型 ID。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:243`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/service.go:1694`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/service.go:1750`。

OAuth 请求路由时，别名表会从客户端请求名反解出上游模型；匹配会处理思考后缀，若配置目标自带后缀则配置优先。证据：`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go:20`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go:73`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go:178`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go:264`。

Amp 映射是从 Amp 请求模型到本地可用模型的替代路由。配置支持精确规则和正则规则，目标模型必须在本地 provider 注册表里有可用执行者；规则匹配大小写不敏感，思考后缀会被保留，除非目标本身已经写死了后缀。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:253`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:16`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:45`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:111`。

配置示例把 Amp 映射描述为“请求的模型不可用时路由到本地可用替代模型”，并提示全局 OAuth 别名会同时影响模型列表和路由，且重叠别名会造成后端选择歧义。证据：`CLIProxyAPI@CLIProxyAPI-latest:config.example.yaml:313`、`CLIProxyAPI@CLIProxyAPI-latest:config.example.yaml:327`。

### 3.2 模型替换 / fallback

Amp 路由分四类：本地 provider、模型映射、Amp credits 上游、无 provider。路由日志会记录请求模型、解析后模型、provider、成本来源等，但这是服务端日志，不是调用方响应。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:18`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:35`。

Amp fallback 处理会先从请求体读出模型名，再根据配置决定顺序：默认模式先找本地 provider，找不到再尝试映射；强制模式先尝试映射，再找本地 provider。映射命中后会把请求体中的模型改为解析目标，并把解析目标放进请求上下文。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:149`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:186`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:207`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:298`。

如果本地 provider 和映射都不可用，Amp 路由可以转发到 Amp 上游；如果没有上游代理，则让正常 handler 返回错误。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:226`。

Amp provider alias 路由和 Google 模型路径桥接都会套上这层 fallback/映射包装；模型列表 GET 路径不套请求体 fallback。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/routes.go:232`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/routes.go:273`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/routes.go:305`。

### 3.3 对调用方的透明度

关键点：CLIProxyAPI 的 AMP 层会把响应里的模型字段改回客户端原请求模型。映射命中时，服务端 handler 看到的是映射后的目标模型，但响应 wrapper 会把多个常见位置上的模型字段改回原请求模型，并 flush 给调用方。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:251`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:124`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:226`。

测试覆盖了这个隐藏行为：handler 内看到替代目标，但响应体主模型回到原请求模型；非流式顶层、嵌套 response、SSE 事件、message 嵌套位置都被回写。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers_test.go:16`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers_test.go:67`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter_test.go:8`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter_test.go:20`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter_test.go:66`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter_test.go:92`。

更进一步，即使没有配置映射，只要走本地 provider 路径，CLIProxyAPI 也会套同一个响应改写器，因为上游代理可能返回不同模型名或缺少 Amp 需要的兼容字段。这个路径同样会把可见模型压回客户端请求模型。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:264`。

本次搜索没有观察到用于告诉调用方“模型已替换 / 实际上游模型是什么”的专用响应头。命中的模型相关响应头主要出现在测试或无关上游 header 透传场景，不构成 AMP 映射透明通知。相关搜索覆盖 `internal/` 与 `sdk/` 中的模型替换、上游、解析模型、请求模型关键词。

结论：CLIProxyAPI 在 AMP 层是“默认隐藏、并主动把响应模型改回请求名”的模式。这正是 HUAKAI juice 透明版应避免的“兼容展示优先于事实披露”的反例。

### 3.4 管理员配置面 / 日志 / 面板

CLIProxyAPI 暴露管理 API 读写 Amp 映射：可以读取全部规则、整体替换、按来源模型增改、删除部分或全部，并能读写“强制映射优先”开关。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1279`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1288`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1301`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1328`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1354`。

Amp 模块启动时从配置初始化映射器，配置更新时会比较新旧映射并热更新。配置 diff 也会记录映射条目数变化和强制映射开关变化。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:103`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:126`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:177`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:292`、`CLIProxyAPI@CLIProxyAPI-latest:internal/watcher/diff/config_diff.go:244`。

用量侧，CLIProxyAPI README 说明核心项目与管理中心从 v6.10 起不再内置 usage statistics，推荐外部 keeper/dashboard/manager。外部工具可以按 account、model、channel、latency、status、token 等维度展示，但本次在核心路径里没有观察到像 sub2api 那样持久化“请求 → 映射 → 上游”链条并直接向用户展示的面板。证据：`CLIProxyAPI@CLIProxyAPI-latest:README.md:73`、`CLIProxyAPI@CLIProxyAPI-latest:README.md:79`、`CLIProxyAPI@CLIProxyAPI-latest:README.md:83`、`CLIProxyAPI@CLIProxyAPI-latest:README.md:173`。

核心 usage 队列会输出模型、别名、provider、auth 类型、响应头、token 等，但没有在已读 payload 中看到独立的“实际上游模型链”字段。SDK 执行请求元数据会保存客户端请求模型，usage 测试也验证可从上下文拿到请求模型别名；这更像内部统计辅助，不是面向最终用户的验真展示。证据：`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/executor/types.go:10`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/conductor.go:1602`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/conductor_usage_test.go:11`、`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:33`、`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:89`。

### 3.5 易漏小功能

- 精确 + 正则映射，正则按配置顺序评估；目标必须有可用 provider，否则规则被跳过。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:68`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:88`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:130`。
- 思考后缀保留与配置优先：用户请求带后缀时可继承到目标，配置目标自带后缀时以配置为准。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:85`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:163`。
- 强制映射优先开关：可让映射先于本地 provider 生效。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:294`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:186`。
- OAuth 别名可影响模型列表和请求路由，分叉展示可以保留原模型并新增别名。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:243`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/service.go:1750`。
- 配置热更新和 diff 可见，但不是每请求级别的模型链验真。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:204`、`CLIProxyAPI@CLIProxyAPI-latest:internal/watcher/diff/config_diff.go:244`。

## 4. 对比表

| 细节项 | sub2api | CLIProxyAPI | HUAKAI 透明版应吸收/升级 |
|---|---|---|---|
| 映射配置位置 | 账号凭据、渠道配置、部分分发/compact 特殊配置；Antigravity 有默认表。证据：`sub2api@sub2api-latest:backend/internal/service/account.go:446`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:372` | 全局 OAuth alias 与 Amp 映射；Amp 规则可经管理 API 改。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:138`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1279` | 统一成“映射策略版本 + 规则 ID + 适用租户/Key/渠道/平台”，不要散在多个不可追踪入口。 |
| 匹配能力 | 精确 + 通配符；渠道按平台隔离。证据：`sub2api@sub2api-latest:backend/internal/service/channel_service.go:397` | 精确 + 正则；大小写归一；目标 provider 可用性校验。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:68`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:88` | 吸收通配符/正则，但加冲突检测、预览命中、dry-run 和规则优先级解释。 |
| 请求体改写 | 渠道映射命中后改写发往上游的模型。证据：`sub2api@sub2api-latest:backend/internal/handler/gateway_handler.go:743` | Amp 映射命中后改写请求体模型。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:193` | 可以改写，但必须同步产生用户可见 truth record。 |
| fallback / 替换 | Antigravity 模型不存在且开启兜底时重试一次；已读路径未见用户通知。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2212` | 默认本地 provider 优先，缺失再映射；强制模式映射优先；仍无用户透明通知。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:186` | 每次替换要显示原因：规则映射、目标不可用、fallback、管理员强制、上游报错后替换。 |
| 响应 body 的模型字段 | Antigravity Claude 转换响应把可见模型设为原请求模型。证据：`sub2api@sub2api-latest:backend/internal/pkg/antigravity/response_transformer.go:299` | AMP 响应改写器把多个位置上的模型字段改回原请求模型。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:124`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:226` | 不允许只回写请求名而无事实披露；若协议兼容必须保留原字段，则另加 truth 字段/响应头/面板链。 |
| 响应头透明通知 | 本次未观察到专用替换通知响应头。 | 本次未观察到专用替换通知响应头。 | 定义稳定响应头，例如请求模型、路由模型、上游返回模型、策略版本、是否替换；同时注意隐私和租户边界。 |
| 普通用户面板 | 普通 usage 只展示单一模型列，DTO 不带管理员上游字段。证据：`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:567`、`sub2api@sub2api-latest:frontend/src/views/user/UsageView.vue:593` | 核心项目不内置 usage stats，外部面板按模型等维度统计；未观察到每请求映射链。证据：`CLIProxyAPI@CLIProxyAPI-latest:README.md:73`、`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:89` | juice 模块首要差异化：普通用户每请求可见“请求 → HUAKAI 路由/映射 → 上游真实返回”。 |
| 管理员面板/日志 | 管理员 usage 支持请求/上游/映射统计，错误日志可显示请求到上游链。证据：`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:26`、`sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue:99` | 管理 API 可改配置，日志记录路由决策；核心面板不提供内置映射链展示。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:35`、`CLIProxyAPI@CLIProxyAPI-latest:README.md:73` | 吸收 sub2api 管理员视图，但把“仅管理员可见”升级为“用户也能验真”。 |
| usage/log 存储 | 存请求模型、上游模型、渠道和映射链。证据：`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:41`、`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:56` | usage 队列有模型/别名/provider/token/响应头，未见独立映射链字段。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:33`、`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:89` | 每请求存三段模型链、规则版本、替换原因、上游原始模型字段值和响应兼容字段值。 |
| 配置变更可见性 | 渠道规则保存和冲突检查存在；是否有完整审计本次未展开。证据：`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:1468` | 映射热更新和 diff 可显示规则数量/开关变化。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:177`、`CLIProxyAPI@CLIProxyAPI-latest:internal/watcher/diff/config_diff.go:244` | 增加不可抵赖审计：谁改了映射、何时生效、影响哪些请求、用户何时看到变化。 |
| 按 tenant/key 策略 | 账号、分组、渠道维度已能间接形成策略差异。证据：`sub2api@sub2api-latest:backend/internal/service/channel_service.go:485` | OAuth alias 按通道，Amp 映射全局；部分路由元数据保留请求模型。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go:138`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/executor/types.go:10` | 明确支持租户、API key、订阅、渠道、上游账号五级策略，并在 juice 展示中标出命中的策略范围。 |
| 版本/后缀回显 | Antigravity 会根据思考模式调整目标模型后缀。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:1367` | Amp/OAuth alias 均处理思考后缀保留和配置优先。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:85`、`CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go:178` | juice 需要显示“版本/后缀由谁决定”：用户请求、规则附加、上游返回、兼容层覆盖。 |

## 5. 关键判断回答

1. sub2api 是不是透明披露模型映射？
   - 不是完全透明。它在后台和管理员面板里已经具备比较好的透明基础：使用记录能存请求模型、上游模型和映射链，管理员统计/导出/错误日志可见这些信息。证据：`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:41`、`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:26`、`sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue:99`。
   - 但对普通调用方和普通用户 usage 来说，默认仍偏隐藏。Antigravity 转换响应会把响应模型设回原请求模型；普通用户 DTO 和普通 usage 页面不暴露上游模型/映射链。证据：`sub2api@sub2api-latest:backend/internal/pkg/antigravity/response_transformer.go:299`、`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:567`、`sub2api@sub2api-latest:frontend/src/views/user/UsageView.vue:593`。

2. CLIProxyAPI 是不是透明披露模型映射？
   - AMP 层不是透明披露，而是明确偏“兼容隐藏”。请求实际会改写到替代模型，handler 看到替代模型，但响应里的模型字段被改回客户端原请求名；测试覆盖了这种行为。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:251`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:226`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers_test.go:67`。
   - CLIProxyAPI 有管理 API、热更新、日志和外部 usage 生态，但本次没有观察到用户可见的“请求 → 映射 → 上游真实返回”验真链。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1279`、`CLIProxyAPI@CLIProxyAPI-latest:README.md:73`、`CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go:89`。

3. 两者有没有用户可见的模型验真/映射展示？
   - sub2api：管理员可见较强，普通用户/调用方可见不足。可以说“有后台透明能力，没有完整用户侧 juice 验真”。
   - CLIProxyAPI：本次未观察到用户侧验真展示；AMP 层还主动把响应模型恢复为请求名，是 HUAKAI 透明版要反向设计的参考。

4. HUAKAI 差异化空白是什么？
   - 明确把“事实模型链”给到用户：请求模型、HUAKAI 映射/路由模型、上游响应真实模型、响应兼容展示模型、替换原因、规则版本、生效管理员配置。两个参考项目都没有把这件事作为普通用户不可绕过的产品承诺。

## 6. 给 juice 透明版的吸收 + 升级建议

### 6.1 可吸收的细节

- 从 sub2api 吸收：每请求存储请求模型、上游模型、渠道映射链；管理员模型分布按请求/上游/映射来源切换；错误日志直接显示请求到上游的不一致；计费模型来源可选。证据：`sub2api@sub2api-latest:backend/ent/schema/usage_log.go:41`、`sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue:156`、`sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue:99`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:713`。
- 从 sub2api 吸收：映射规则的精确/通配符、平台隔离、冲突检查、默认平台映射、compact 特殊映射入口。证据：`sub2api@sub2api-latest:backend/internal/service/channel_service.go:232`、`sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue:1468`、`sub2api@sub2api-latest:backend/internal/service/account.go:700`。
- 从 CLIProxyAPI 吸收：Amp 映射的精确/正则、目标 provider 可用性校验、思考后缀保留、强制映射优先开关、管理 API 增删改查、热更新和配置 diff。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go:45`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:186`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go:1279`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:177`。

### 6.2 必须升级的地方

- 不把响应模型“悄悄改回请求名”作为最终事实。若协议兼容要求保留原字段，就额外输出 HUAKAI 自己的 truth fields / headers / panel record，并在 juice 面板标出“协议字段为兼容显示，不等于上游真实模型”。这是对 CLIProxyAPI AMP 行为的反向约束。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go:226`。
- 用户侧必须可见，不只管理员可见。sub2api 已经证明后台能存上游模型和映射链，但普通用户 DTO/页面仍隐藏这些字段；HUAKAI 透明版应把这部分升格为用户权益。证据：`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:567`、`sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go:635`。
- fallback 必须显式标记。每次替换都记录并展示原因：规则映射、目标不可用、上游模型不存在、管理员强制、provider 缺失、兜底重试。sub2api 和 CLIProxyAPI 都有替换/兜底动作，但已读路径没有用户可见通知。证据：`sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go:2212`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go:226`。
- 映射配置改动必须能追到请求。建议每条请求保存映射策略版本、规则 ID、命中层级、管理员配置快照哈希、响应时刻实际生效版本。CLIProxyAPI 的配置 diff 和 hot reload 可作为低配参考，但 HUAKAI 需要每请求级别验真。证据：`CLIProxyAPI@CLIProxyAPI-latest:internal/watcher/diff/config_diff.go:244`、`CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go:204`。

### 6.3 建议的 HUAKAI juice 展示字段

- 请求侧：用户请求模型、请求协议、请求 API key/租户策略范围。
- HUAKAI 决策侧：命中的映射规则、映射目标、路由渠道/账号、是否 fallback、fallback 原因、规则版本、管理员修改时间。
- 上游侧：实际发给上游的模型、上游响应 body 中出现的模型、上游错误触发的替换记录。
- 展示侧：最终返回给客户端的兼容模型字段、是否与上游真实模型不同、差异告警。
- 运营侧：按请求模型/路由模型/上游模型/响应展示模型四个来源统计 usage，避免只按一个“model”聚合掩盖替换。

### 6.4 Open questions

1. sub2api 普通流式响应路径是否还有其他兼容层会覆盖模型字段？本次重点读了 Antigravity Claude 非流式转换和 usage/DTO 路径，其他 provider 的原样透传路径未逐个展开。
2. sub2api 管理员配置改动是否有完整审计表？本次确认了配置保存、usage 与 ops 展示，未完整展开审计模块。
3. CLIProxyAPI 外部 dashboard/manager 是否能自定义展示映射链？本次只按本地核心 repo README 和核心代码判断，没有读取外部工具源码。
4. CLIProxyAPI 非 AMP 的 OAuth alias 路径是否在某些协议转换响应中也回写模型字段？本次确认了 alias 影响列表和路由，未逐个协议响应层展开。
5. 两者是否存在非核心插件通过 header 暴露模型替换？本次在核心 `internal/` 与 `sdk/` 相关关键词中未观察到，不能替代插件生态全量审计。

## 7. 结论

sub2api 更像“后台/管理员透明，但普通用户和调用方默认不透明”：它已经有上游模型、请求模型、映射链、管理员统计和错误日志展示，值得 HUAKAI 吸收；但响应 body 与普通用户 usage 仍可能让用户以为拿到的就是请求模型。CLIProxyAPI 的 AMP 层则是明确“隐藏映射”：它把请求改写到替代模型，又把响应模型字段改回请求名，适合作为 HUAKAI 透明版必须反向避免的反例。HUAKAI juice 透明版的差异化应是把 truth chain 作为产品基础设施：每请求都能让用户看到“请求模型 → HUAKAI 实际路由/映射模型 → 上游真实返回模型”，并把 fallback、规则变更、兼容回写全部标出来。

## Source Coverage Proof

### Source files read

- sub2api@sub2api-latest:backend/internal/service/account.go
- sub2api@sub2api-latest:backend/internal/domain/constants.go
- sub2api@sub2api-latest:backend/internal/service/antigravity_gateway_service.go
- sub2api@sub2api-latest:backend/internal/pkg/antigravity/response_transformer.go
- sub2api@sub2api-latest:backend/internal/service/channel_service.go
- sub2api@sub2api-latest:backend/internal/handler/gateway_handler.go
- sub2api@sub2api-latest:backend/internal/handler/dto/mappers.go
- sub2api@sub2api-latest:backend/internal/handler/dto/mappers_usage_test.go
- sub2api@sub2api-latest:backend/ent/schema/usage_log.go
- sub2api@sub2api-latest:backend/internal/service/openai_gateway_service.go
- sub2api@sub2api-latest:frontend/src/views/admin/ChannelsView.vue
- sub2api@sub2api-latest:frontend/src/views/admin/UsageView.vue
- sub2api@sub2api-latest:frontend/src/views/admin/ops/components/OpsErrorLogTable.vue
- sub2api@sub2api-latest:frontend/src/views/user/UsageView.vue
- sub2api@sub2api-latest:backend/internal/service/openai_messages_dispatch.go
- sub2api@sub2api-latest:frontend/src/views/admin/groupsMessagesDispatch.ts
- CLIProxyAPI@CLIProxyAPI-latest:internal/config/config.go
- CLIProxyAPI@CLIProxyAPI-latest:config.example.yaml
- CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/oauth_model_alias.go
- CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/service.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/model_mapping.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/fallback_handlers_test.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/response_rewriter_test.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/routes.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/handlers/management/config_lists.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/api/modules/amp/amp.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/watcher/diff/config_diff.go
- CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/executor/types.go
- CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/conductor.go
- CLIProxyAPI@CLIProxyAPI-latest:sdk/cliproxy/auth/conductor_usage_test.go
- CLIProxyAPI@CLIProxyAPI-latest:internal/redisqueue/plugin.go
- CLIProxyAPI@CLIProxyAPI-latest:README.md

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-21T14:47:48Z

中文摘要：本次真观察包括 sub2api 的账号/渠道映射、Antigravity 响应展示模型、usage 上游模型字段、管理员统计与运维日志，以及 CLIProxyAPI 的 OAuth alias、Amp 映射、fallback 顺序、响应模型回写、管理 API 和 usage 队列。合理推断集中在“已读核心路径未观察到用户可见替换通知，因此不能视为用户透明验真”。Open questions 共 5 个，主要是边缘 provider、审计模块、外部 dashboard 和插件生态是否另有透明展示。
