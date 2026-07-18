# HUAKAI（华凯）

> MIT clean-room AI 网关 + 账号枢纽 + 运营管理平台。

**语言：** [English](README.md) · [简体中文](README_CN.md) · [Tiếng Việt (TBD)](README_VI.md) · [日本語 (TBD)](README_JA.md) · [한국어 (TBD)](README_KO.md) · [Español (TBD)](README_ES.md)

---

## ⚠️ 免责声明

> **使用 HUAKAI 前请仔细阅读。**
>
> 🚨 **服务条款风险。** 使用 HUAKAI 访问上游 LLM 服务商，可能违反该服务商的服务条款（Anthropic、OpenAI、Google、AWS 等）。任何具体部署是否被允许，由 operator 自行判断；**使用本项目的一切风险，由 operator 自行承担。**
>
> 📖 **用途声明。** HUAKAI 仅供技术学习、安全研究与 operator 自托管使用。作者与贡献者**不对**因使用本项目导致的账户封禁、服务中断、财务损失或任何其它后果承担责任。
>
> 完整条款见下文「合规与责任」一节与 [LEGAL.md](LEGAL.md)。

## HUAKAI 是什么

HUAKAI 是一个 **operator 自托管的反向代理与账号路由器**，面向各家 LLM 服务商
账号（Anthropic、OpenAI、Google Vertex、AWS Bedrock、OpenRouter）。它运行在
operator 已合法持有的一个或多个上游账号之前，提供：

- 统一协议接口供下游客户端调用
- 健康度感知的账号派发
- 速率限制 / 冷却 / 重试处理
- 用量 / 计费会计
- 可选的应用层与传输层"伪装为上游官方 CLI 客户端"模块（仅在 operator 合规
  用例需要时启用 — 见下文"传输层伪装"）

HUAKAI 面向 **个人使用、小团队自托管、安全研究环境**。仓库以开源形式发布，
方便 operator 审计自己设备上运行的软件。

## HUAKAI 不是什么

- HUAKAI **不与任何上游 LLM 服务商存在合作、背书或合作关系**。"Claude Code"、
  "Claude"、"Anthropic"、"OpenAI"、"ChatGPT"、"Cursor"、"Vertex AI"、
  "Gemini"、"Bedrock" 等名称属于各自所有者，HUAKAI 引用仅用于描述互操作。
- HUAKAI 不附带任何预置凭据、采集到的指纹模板或其它运营产出物。所有配置由
  operator 自行提供。
- 项目维护者**不**以商业 SaaS 形态运营 HUAKAI。项目作为可由 operator 自行
  托管的软件发布。**如果 operator 选择把 HUAKAI 部署成 SaaS 或任何面向第三
  方的服务形态，该 operator 自行承担与每家上游服务商 ToS 以及其司法辖区
  适用法律的合规责任。** HUAKAI 维护者不保证任何具体部署形态被某家具体上游
  服务商许可。

## 适用场景

- operator 在自己拥有或完全控制的机器上，使用自己合法获得的上游账号运行 HUAKAI
- 小团队内部自托管 HUAKAI 共享团队成员合法持有的账号
- 安全研究人员 / 学生 / 开发者在受控环境下学习反代 / 多账号路由模式
- operator 也可以把 HUAKAI 部署成 SaaS 或服务形态，**operator 自负 ToS 与法律
  合规责任**

## 禁止场景

- 利用 HUAKAI 绕过任何上游服务商对面向公众付费服务的 Terms of Service，且未
  获得上游授权
- 钓鱼、中间人攻击或任何对非 operator 自身流量的未授权监听
- 对 operator 不合法持有的账号使用 HUAKAI
- 错误地把 HUAKAI 表述为某家上游服务商的产品或获其背书

## 合规与责任

**operator 自行确保其使用 HUAKAI 的方式符合每家上游服务商的 Terms of Service、
其司法辖区的适用法律以及任何相关第三方的权利。** HUAKAI 项目作者与贡献者：

- 以 "AS IS" 形式提供 HUAKAI，不附任何担保
- 不主张任何 HUAKAI 部署形态被某家具体上游服务商许可
- 不为 operator 的使用、误用、账号封禁、财务损失、法律暴露或其它任何后果承担
  责任

如果你不确定你的预期用例是否符合某家上游 ToS，**先直接查阅该上游服务商的官方
文档，并/或在部署 HUAKAI 前寻求独立的法律建议**。

## 传输层伪装（高敏，需 gate）

HUAKAI 提供一个可选的传输层伪装模块（内部代号 `R3`），可调整出站 TLS / HTTP-2
指纹以与上游官方 CLI 客户端一致。该模块：

- **默认关闭**。operator 必须按上游服务商粒度显式启用
- **不附带任何指纹模板**。operator 必须使用仓库自带的
  `tools/fingerprint-collector` 工具，在自己的机器上、对自己合法运行的客户端、
  在自己合规的网络环境下，自行采集
- 仅适用于 operator 合规用例确实要求出站在传输层与上游官方客户端不可区分的场景

启用 R3 即代表 operator 确认：

1. 自己有权采集并使用所采集到的源客户端指纹
2. 自己使用该伪装的方式不违反上游服务商的 ToS 或自己司法辖区的适用法律

抓包工具 [tools/fingerprint-collector/README.md](tools/fingerprint-collector/README.md)
有更严格的"工具能用 / 不能用"边界、以及"哪些文件能/不能离开 operator 机器"
规则。

---

## 项目状态

**状态：** Phase C / N+5b 进行中。后端已有工作可跑的 clean-room 网关核心切片，
不只是治理文档。当前实现的请求路径：

```text
Inbound Auth -> Model Registry -> Router Plan -> ClaimGate Reserve
-> Resource Pool Select -> Stream Forwarder -> Billing/Observability Settler
```

多 attempt 回退路由已落地（`backend/internal/gatewayhttp/chat_completions_handler.go:466`
按 `resolver.MaxDepth()` 多模型回退），真实 provider 适配器（`backend/internal/provider/registrydefault/default.go:177`
注册 Grok/Kimi/DeepSeek/Mistral 直通）、产线计费（`backend/internal/billing/public_price_table.go:166`
真实 micro-USD 定价）、admin API（`backend/cmd/gateway/routes.go:815` 起 `/admin/v1/*` 路由组）
均已实现并接线；仍在路上的是 `attempt_id` / `lease_id` schema 一等公民与前端控制台。强伪装
模块（R7 应用层 6-step body 变换、R3 传输层伪装）正在 feature flag 后开发。

## 使命

通过 clean-room 重写达到与高质量同类 AI 网关 / 账号枢纽项目同等或更优的功能
parity，且保持 MIT 兼容。参考项目仅作行为证据来源；任何参考项目的功能不会被
静默砍除，风险只改变实现方式而不改变范围。

## 仓库目录

| 路径 | 用途 |
| --- | --- |
| [backend/](backend/) | Go 后端核心：网关 HTTP 入口、入站鉴权、模型注册表、路由引擎、资源池、协议转换、流式转发器、计费/可观测账本、SQL 迁移、测试 |
| `frontend/` | 当前没有可信的前端工作区；运营控制台将按逐页规格与真实 API 合同重构，目前自托管只提供 API |
| [tools/](tools/) | operator 工具（如 `fingerprint-collector`，传输层伪装的前置准备）。每个工具自带 README 写明使用边界 |
| [CLAUDE.md](CLAUDE.md) / [GEMINI.md](GEMINI.md) / [AGENTS.md](AGENTS.md) | 各 agent 的运营章程 |
| [docs/](docs/) | 治理、契约、parity 矩阵、风险登记、release gate、spec、plan 的权威源 |
| [docs_zh/](docs_zh/) | Owner 中文摘要文档。除非有决策另行规定，英文文档为权威 |
| [docs/process/plans/](docs/process/plans/) | 实施切片的执行计划 + Claude/Codex 交叉讨论记录 |
| [backend/sql/migrations/](backend/sql/migrations/) | PostgreSQL 迁移：pool routing / 计费 / 入站鉴权 / 模型注册表等 |
| [LEGAL.md](LEGAL.md) | 商标声明、合规与责任条款、DMCA 联系、数据处理规则 |

## 现行后端切片

线上入站已注册 40+ 个不同 `/v1/*` 与 `/admin/v1/*` 路由前缀（含 `/v1/messages`、`/v1/embeddings`、`/v1/images`、`/v1/audio`、`/v1/responses`、`/v1/rerank` 等），远不止单一 `/v1/chat/completions`（`backend/cmd/gateway/routes.go:106` `/v1/messages`）。

已实现：

- 表驱动入站 API key 鉴权（`backend/internal/auth`）
- PostgreSQL 模型注册表（`backend/internal/registry`）
- L0 路由引擎（`backend/internal/router`）
- 资源池选择 + claim 回写（`backend/internal/pool`）
- 流式转发器 + 用量草稿提取（`backend/internal/gateway`）
- Tx1/Tx2 计费 + 可观测结算（`backend/internal/billing`）
- PostgreSQL 迁移到 `0093_billing_ledger_claims_listing_index`
- R7 应用层伪装原子（system rewrite / cache_control breakpoints / tool-name
  obfuscation / metadata user_id rewrite / 6-step composer）位于
  `backend/internal/gateway/`

已知限制：

- 路由仍为 L0：从 `PoolCandidates[0]` 取一次 primary attempt
- 网关 executor 逻辑仍嵌在 chat handler 内
- `attempt_id` 与 `lease_id` 已文档化但未成 schema 一等公民
- provider 适配器已产线化并按真实上游字节走（`backend/internal/provider/registrydefault/default.go:177` 注册 Grok/Kimi/DeepSeek/Mistral 直通；anthropic 出站经 uTLS mimicry `backend/cmd/gateway/wiring.go:807`），非 mock
- 成功请求按真实 micro-USD 定价结算（`backend/internal/billing/public_price_table.go:166` input/output_micro_usd），无固定 placeholder cost
- admin API 已实现并挂载（`backend/cmd/gateway/routes.go:815` 起 `/admin/v1/*` 路由组）；仅前端控制台尚未实现
- R3 传输层伪装仍在 plan 阶段，无 production-ready 代码

## 从哪里开始

1. 读 [docs/01_PROJECT_BRIEF.md](docs/01_PROJECT_BRIEF.md) 了解产品范围
2. 读 [AGENTS.md](AGENTS.md) 与 [docs/RULES.md](docs/RULES.md) 了解现行规则
3. 读当前目标唯一执行计划
4. 在动任何受外部参考驱动的代码前读 [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md)
5. 读 [docs/16_PHASED_DELIVERY_PLAN.md](docs/16_PHASED_DELIVERY_PLAN.md) 了解阶段划分
6. 后端核心工作前读 [docs/specs/_invariants/cross-module-boundaries.md](docs/specs/_invariants/cross-module-boundaries.md)
7. 当前请求路径起点：[backend/cmd/gateway/main.go](backend/cmd/gateway/main.go) 与 [backend/internal/gatewayhttp/chat_completions_handler.go](backend/internal/gatewayhttp/chat_completions_handler.go)
8. 前端工作前读 [docs/14_UI_CONTRACTS.md](docs/14_UI_CONTRACTS.md) 与 [docs/08_REAL_WORLD_SCENARIOS.md](docs/08_REAL_WORLD_SCENARIOS.md)

## 验证

在 `backend/` 目录：

```bash
go test ./...
go test -tags integration_pg ./...
go test -tags smoke ./cmd/gateway
```

`integration_pg` 与 `smoke` 需要 `HUAKAI_DATABASE_URL` 指向已迁移的 PostgreSQL。

## 参考项目

参考项目仅作行为证据来源，不作源代码提供方。许可证类型决定 clean-room 处理方式。
参考项目分层与许可证复核要求见 [docs/24_REFERENCE_TRACKING_POLICY.md](docs/24_REFERENCE_TRACKING_POLICY.md)。

## 如何做决策

当前被 Owner 指派的执行者负责一个工作单元的调研、实现、测试和收口。决策必须建立在 HUAKAI 真码、官方合同、匹配领域的源码证据、完整链路影响和运维恢复上；独立 reviewer 是质量门，不是第二套计划。只有 [docs/RULES.md](docs/RULES.md) 规定的决策停门才需要 Owner 拍板。历史 Decision Record 保留在 [docs/process/decisions/](docs/process/decisions/) 中。

## License

[MIT](LICENSE)。本仓库的贡献必须保持 MIT 兼容。学习外部项目时允许 / 禁止的行为
见 [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md)。

HUAKAI 使用的第三方库（utls / gopacket 等）按各自许可证。

## 法律

商标声明、合规与责任条款、DMCA 联系、数据处理规则见 [LEGAL.md](LEGAL.md)。

## 贡献

实施进行中。所有改动须由 Owner 指派，遵守 clean-room、唯一计划、独立 review
与跨模块边界不变量。贡献者条款尚未发布。

## 不附担保

```
本软件以 "AS IS" 形式提供，不附任何明示或暗示的担保，包括但不限于对适销性、
特定用途适用性以及非侵权的担保。在任何情况下作者或版权持有者均不对任何索赔、
损害或其它责任承担责任，无论是基于合同、侵权或其它行为，因软件或软件的使用、
其它处置而产生、由此产生或与之相关。
```
