# Plan — 模型能力绑定 upsert 写端点 (inert-gap 切片)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner 全权自主实现+合并)
- 基线: origin/feat/frontend-portal @ 883defa2
- 分支: feat/frontend-admin-capability-binding

## 背景 + 真 inert gap 核实 (禁止凭记忆 + 禁止假绿)

`PUT /v1/admin/models/{id}/capability-bindings` —— 补全此前仅 GET 的 capability-binding admin 面。
确认是【真 inert gap】(非死开关), 三要素齐全:
- **存储✓**: `model_registry_capabilities` 表 (tenant/global scope, capability+value+jsonb params+enabled+source)。
- **校验✓**: store 写方法 `upsertModelCapabilityBinding` (registry/model_capabilities.go:159) 已校验
  scope∈{tenant,global}、capability∈knownModelCapabilityBindings、tenant scope 需 tenant_id>0、model_id>0(ErrUnknownModel)。
- **消费者✓ (真 resolve 行为效果)**: resolve 时 `ListModelCapabilities` (postgres_registry.go:112, 读
  model_registry_capabilities WHERE enabled=true, 含 tenant + global) → `caps` → `out.Capabilities = append(...,
  c.Capability)` (postgres_registry.go:159-161) 真流入 `Resolved.Capabilities`。即 capability 名+enabled 有
  resolve 行为效果 (capability_value/params 目前仅 GET 显示=部分消费, 见 roadmap)。
- **缺口**: store 写方法零生产调用、GET sibling 已存在 (NewAdminModelCapabilityBindingsHandler), 仅缺 PUT 写路径。

### 本切片剔除的 2 个死字段 (真码核实, 非接线)
- `default_request_timeout_ms`: `.RequestTimeoutMS` 字段【零读取点】—— 赋值给 Resolved (postgres_registry.go:153)
  却无任何下游消费 (真实流超时来自 env buildGatewayTimeoutConfig, 非 per-model); 对照 sibling `.ContextWindow`
  有大量真消费者。→ 死开关, 不接线 (见 roadmap)。
- `threshold_type` (前一切片剔除): notifier 只读 BalanceThreshold 金额, 从不读 ThresholdType, 零消费者。

## #16 三镜像研究 (clean-room specifier lane)

「运营如何 per-model/tenant 覆盖一个模型对外宣告的能力 (vision/tools/reasoning 等)」:
- **new-api@1ac0f58**: 能力/支持端点信息存在 model 的定价/选项配置里 (model/pricing.go), 经 admin 定价设置编辑
  —— per-model 但**非租户作用域的能力绑定**, 粒度更粗 (无 tenant/global scope 分层)。
- **sub2api@e34ad2b**: `backend/internal/pkg/openai_compat/upstream_capability.go` 的 "capability" 是**上游
  provider 能力探测** (检测上游支持什么), 不是运营手动 per-model 覆盖。
- **CLIProxyAPI@2a050dc**: capability 仅出现在 pluginhost (插件能力), 纯 relay **无等价** model-capability admin 面。

→ **无直接等价** (三镜像的 "capability" 分别落在定价配置 / 上游探测 / 插件三个不同关注点)。HUAKAI delta =
**架构升级**: 独立 `model_registry_capabilities` 表带 (tenant|global) scope 分层 + source provenance, 与粗粒度
`models.capabilities` blob (PUT /capabilities, sub2api/new-api 式整体能力) 分离; 运营覆盖 (source=operator) 与
vendor-sync 行 (source=<vendor>) 经 source 字段共存/协调 (model_sync_writer.go:454)。这是本项目精度的自有设计。

## 实现范围 (success criteria)

后端 (controlhttp/model_admin_aliases_handler.go, 无新文件; routes.go 加 1 路由):
1. `adminModelAliasesStore` 接口加 `UpsertModelCapabilityBinding`; 请求体 `capabilityBindingUpsertRequest`
   {scope,capability,capability_value?,capability_params?,enabled(*bool 必填),tenant_id?} **无 source 字段**,
   DisallowUnknownFields。
2. `NewAdminModelCapabilityBindingUpsertHandler`: model_id 取 path, enabled nil→400, source 服务端强制 "operator",
   tenant_id 是目标租户 (admin 供), 调 store, 错误经 writeModelAliasStoreError (ErrUnknownModel→404 /
   ErrInvalidModelCapability→400)。routes.go: PUT 与 GET 同路径不同方法 (chi OK), adminGate platform_admin。

前端 (adminModelCapabilities.ts + model-capabilities-form.ts):
3. `CapabilityBindingInput` + `validateCapabilityBinding` (scope/tenant_id/capability) + `buildCapabilityBindingBody`
   (精确 key-set, **无 source**, enabled 永远显式带) + `upsertModelCapabilityBinding` (PUT, validate-first)。

强测试 (变异验证): 后端 handler (source 强制 operator / body source 伪装被 DisallowUnknownFields 拒 / enabled 必填 /
非法 model_id / store 错误映射) + 前端 (validate / builder no-source / enabled-always / 接线)。每条变异转红再还原。

## 关键安全设计
- **source 服务端强制 "operator", 不取 body** (DisallowUnknownFields 拒 body source): 防运营写入伪装成某
  vendor-sync 来源, 被 source-based 同步协调 (model_sync_writer.go:454) 误清/误保护。
- **enabled *bool 必填** (nil→400): upsert 的 ON CONFLICT DO UPDATE SET enabled=EXCLUDED.enabled 下, 省略 enabled
  按零值 false 会把已有 enabled 绑定【静默翻 disabled】(routes-enable 同款 read-omit-write footgun)。
- tenant_id 是【目标租户】(admin 供, scope=tenant 用), 非 actor 身份; binding 表无 actor 列故无 per-actor 审计
  (source=operator 是 provenance, 与 aliases 的 actor-from-identity 不同 —— 此表本就无 actor 列)。

## blast radius
- 一个 PUT handler + 接口 1 方法 + 路由 1 行。store 方法/schema/resolve **不改** (已存在已测已消费)。无 money 无避让。

## 无 snapshot bump (设计决策)
mirror 已接线的 sibling `updateModelCapabilities` (capabilities PUT, model_capabilities.go:116) —— 它仅 UPDATE 不
bump snapshot。resolve (ResolveModel) 每次现算 (fresh DB 查询), `SnapshotVersion` 是返回给 client 的 ETag/cache 提示。
故能力绑定写入下次 resolve **即时生效** (新 ListModelCapabilities 查询), 无需 bump。snapshot-staleness (写后
SnapshotVersion 不变, client 端按 version 缓存可能不刷新) 是**既有跨切面问题, 同时影响 updateModelCapabilities**,
非本切片回归 → 记 follow-up (见 DEFERRED-capability-binding-followups.md), 不在本切片处理以保持与既有 sibling 一致。

## 门禁
codex 401 → ultracode 对抗审查 (#8 替代门禁) 零 S0/S1 → squash 合并 → ff main。
