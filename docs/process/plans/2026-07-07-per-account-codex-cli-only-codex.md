# 片2e:每账号 codex-cli-only 收紧开关 — Codex 独立计划(specifier lane)

> #10 平行拟计划:codex 未见 Claude 计划、独立起草。原文留档(轻编排,内容不改)。UTC 2026-07-07。

## Scope
每账号 opt-in 的 `codex_cli_only` 入站鉴真策略。只改后端小范围:`provider.AccountInfo`、`PostgresCredentialVault` 两条 Resolve 路径、`officialclient.GateDecision`、`gatewayhttp` 调用点与单测。不做 schema 迁移,不改 quota/billing/auth core,不改默认行为。

## 方案与路径选择
在 `provider.AccountInfo` 增布尔字段(`CodexCLIOnly` 或更泛的 `RequireOfficialClient`)。理由:门控点已只依赖 `ex.accInfo`,该字段是账号级策略、不属出站凭据;直接传 `AccountInfo` 避免 handler 再查库,也避免把策略塞进 `Credential.Extra`。证据:`enforceOfficialClient` 只用 `ex.accInfo.AccountType/Platform` 调 GateDecision(dispatch.go:582);AccountInfo 定义 adapter.go:57。

## 读取与串接
在 postgres_vault.go 从 `provider_accounts.extra` 解布尔填入 AccountInfo。新 v2 路径 resolveFromStore 已调 loadProviderAccountExtra,同处解析 codex_cli_only;legacy 路径当前只构造 AccountInfo,须补读 extra,否则老账号不生效。证据:legacy 构造 :131;新路径 :160-180 读 accountExtra。

## 泄漏控制
codex_cli_only 必须作为账号策略字段消费,不能进 cred.Extra(mergeCredentialAccountExtra 会把 extra 键并进出站语义的 Credential.Extra)。在合并函数过滤 HUAKAI 内部策略键(至少跳过 codex_cli_only)+ 测试证明 cred.Extra 不含该键。证据 postgres_vault.go:214-262。

## GateDecision 改法
签名扩为 `GateDecision(accountType, vendor, clientIdentity, forceOfficialClient bool)`。逻辑:①非 reverse 仍不拒;②vendor 默认强制(anthropic/claude)保持;③force=true 时即使 vendor 默认不强制也要求匹配 RequiredIdentity;④openai/codex/chatgpt 在 force=false 仍 ReasonVendorNotEnforced 放行;⑤无官方客户端映射仍 ReasonVendorNoOfficial 放行(不误杀)。不削弱 Anthropic(vendorEnforcesOfficialClient 独立触发),codex 默认放开(布尔缺省 false)。

## 两条 Resolve 路径
两条都填。漏 legacy→旧凭据账号 extra 开了也不 403(同租户"部分账号策略失效"假象);漏新路径→v2 主路径不生效基本不可用。**建议把"从 accountExtra 解析策略"抽小 helper 两路复用,减少分叉。**

## 测试
- officialclient:Anthropic+非CC 仍拒 / Anthropic+官方放行 / Codex+force=false 非CLI 放行 / Codex+force=true 非CLI 拒 / Codex+force=true CLI 放行 / **apikey 账号即使 force=true 也不拒**。
- vault:extra codex_cli_only=true→字段 true;缺省/false/非法/空→false。
- 泄漏:cred.Extra 不含 codex_cli_only,但原有可透传 account extra 仍按既有合并(不缩水)。
- gateway:knob 开+非CLI→403 且释放预扣;knob 开+CLI 放行;knob 关+非CLI 放行;Anthropic 不受影响。
- 变异点:去 extra 解析 / 漏 legacy 或 new 一条 / 忽略 force / 去过滤 / 去 reverse 判断,测试应红。

## 爆炸半径/风险/成本
小(credential resolve + 账号摘要 + 入站门)。风险:内部策略键透传上游 / 只覆盖一条路径 / 误把 codex 改成全局强制 / 误伤 Anthropic。缓解=helper 复用+过滤测试+矩阵测试+Anthropic 回归。无 schema/money/quota/auth core。成本约 1 工程日。
