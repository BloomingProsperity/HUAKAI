# HUAKAI（华凯）

> MIT clean-room AI 网关 + 账号枢纽 + 运营管理平台。

**语言：** [English](README.md) · [简体中文](README_CN.md) · [Tiếng Việt (TBD)](README_VI.md) · [日本語 (TBD)](README_JA.md) · [한국어 (TBD)](README_KO.md) · [Español (TBD)](README_ES.md)

---

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

项目仍处早期。多 attempt 回退路由、`attempt_id` / `lease_id` 一等公民、真实
provider 适配器、产线计费、admin API 与前端控制台仍是在路径上的工作。强伪装
模块（R7 应用层 6-step body 变换、R3 传输层伪装）正在 feature flag 后开发。

## 使命

通过 clean-room 重写达到与高质量同类 AI 网关 / 账号枢纽项目同等或更优的功能
parity，且保持 MIT 兼容。参考项目仅作行为证据来源；任何参考项目的功能不会被
静默砍除，风险只改变实现方式而不改变范围。

## 仓库目录

| 路径 | 用途 |
| --- | --- |
| [backend/](backend/) | Go 后端核心：网关 HTTP 入口、入站鉴权、模型注册表、路由引擎、资源池、协议转换、流式转发器、计费/可观测账本、SQL 迁移、测试 |
| [frontend/](frontend/) | 前端工作区占位，运营控制台尚未实现 |
| [tools/](tools/) | operator 工具（如 `fingerprint-collector`，传输层伪装的前置准备）。每个工具自带 README 写明使用边界 |
| [CLAUDE.md](CLAUDE.md) / [GEMINI.md](GEMINI.md) / [AGENTS.md](AGENTS.md) | 各 agent 的运营章程 |
| [docs/](docs/) | 治理、契约、parity 矩阵、风险登记、release gate、spec、plan 的权威源 |
| [docs_zh/](docs_zh/) | Owner 中文摘要文档。除非有决策另行规定，英文文档为权威 |
| [docs/process/plans/](docs/process/plans/) | 实施切片的执行计划 + Claude/Codex 交叉讨论记录 |
| [backend/sql/migrations/](backend/sql/migrations/) | PostgreSQL 迁移：pool routing / 计费 / 入站鉴权 / 模型注册表等 |
| [LEGAL.md](LEGAL.md) | 商标声明、合规与责任条款、DMCA 联系、数据处理规则 |

## 现行后端切片

当前线上路径：`POST /v1/chat/completions`。

已实现：

- 表驱动入站 API key 鉴权（`backend/internal/auth`）
- PostgreSQL 模型注册表（`backend/internal/registry`）
- L0 路由引擎（`backend/internal/router`）
- 资源池选择 + claim 回写（`backend/internal/pool`）
- 流式转发器 + 用量草稿提取（`backend/internal/gateway`）
- Tx1/Tx2 计费 + 可观测结算（`backend/internal/billing`）
- PostgreSQL 迁移到 `0008_model_registry`
- R7 应用层伪装原子（system rewrite / cache_control breakpoints / tool-name
  obfuscation / metadata user_id rewrite / 6-step composer）位于
  `backend/internal/gateway/`

已知限制：

- 路由仍为 L0：从 `PoolCandidates[0]` 取一次 primary attempt
- 网关 executor 逻辑仍嵌在 chat handler 内
- `attempt_id` 与 `lease_id` 已文档化但未成 schema 一等公民
- provider 适配器未产线完整；当前 happy path 用 mock 上游字节 + Anthropic SSE 解析
- 成功请求仍以固定 placeholder cost 结算
- admin API 与前端控制台尚未实现
- R3 传输层伪装仍在 plan 阶段，无 production-ready 代码

## 从哪里开始

1. 读 [docs/01_PROJECT_BRIEF.md](docs/01_PROJECT_BRIEF.md) 了解产品范围
2. 读 [docs/00_PM_OPERATING_SYSTEM.md](docs/00_PM_OPERATING_SYSTEM.md) 了解运营循环
3. 在动任何受外部参考驱动的代码前读 [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md)
4. 读 [docs/16_PHASED_DELIVERY_PLAN.md](docs/16_PHASED_DELIVERY_PLAN.md) 了解阶段划分
5. 后端核心工作前读 [docs/specs/_invariants/cross-module-boundaries.md](docs/specs/_invariants/cross-module-boundaries.md)
6. 当前请求路径起点：[backend/cmd/gateway/main.go](backend/cmd/gateway/main.go) 与 [backend/internal/gatewayhttp/chat_completions_handler.go](backend/internal/gatewayhttp/chat_completions_handler.go)
7. 前端工作前读 [docs/14_UI_CONTRACTS.md](docs/14_UI_CONTRACTS.md) 与 [docs/08_REAL_WORLD_SCENARIOS.md](docs/08_REAL_WORLD_SCENARIOS.md)

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
已核证许可证状态见 [docs/06_REFERENCE_PROJECTS.md](docs/06_REFERENCE_PROJECTS.md)。

## Agent 如何讨论决策

常规工作走 [docs/12_AGENT_WORKFLOW.md](docs/12_AGENT_WORKFLOW.md) 的 Standard Flow。
需要多视角独立意见后再由 Owner 拍板的决策走 [docs/21_DECISION_PROCESS.md](docs/21_DECISION_PROCESS.md)
的 Round-Table 模式。Round-Table 决策落在 [docs/process/decisions/](docs/process/decisions/)。

## License

[MIT](LICENSE)。本仓库的贡献必须保持 MIT 兼容。学习外部项目时允许 / 禁止的行为
见 [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md)。

HUAKAI 使用的第三方库（utls / gopacket 等）按各自许可证。

## 法律

商标声明、合规与责任条款、DMCA 联系、数据处理规则见 [LEGAL.md](LEGAL.md)。

## 贡献

实施进行中。所有改动须 owner-directed，遵守 clean-room 政策、plan-before-execute
纪律、交叉评审协议与跨模块边界不变量。详见 [CONTRIBUTING.md](CONTRIBUTING.md)（待补）。

## 不附担保

```
本软件以 "AS IS" 形式提供，不附任何明示或暗示的担保，包括但不限于对适销性、
特定用途适用性以及非侵权的担保。在任何情况下作者或版权持有者均不对任何索赔、
损害或其它责任承担责任，无论是基于合同、侵权或其它行为，因软件或软件的使用、
其它处置而产生、由此产生或与之相关。
```
