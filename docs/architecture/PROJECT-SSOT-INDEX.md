# HUAKAI 项目 SSOT 主索引

> 建档：2026-07-15（UTC）  
> 原则：**代码是实现真相；领域 SSOT 是经代码核验后的导航和解释层。** 文档与代码不一致时，
> 先记 `DOC-CODE-DRIFT.md`，不得拿旧文档覆盖代码事实，也不得把代码疑似缺陷悄悄合理化。

## 1. 使用方法

1. 先从本索引进入对应领域 SSOT。
2. 需要判断“现在是否实现”时，沿 SSOT 的 `file:line` 回到实现和调用链复核。
3. 规范、决策、Owner-gated 计划可以约束未来，但除非 SSOT 明确标为已实现，不能当生产现状。
4. 已删除散文档可从 git history 恢复；删除理由和代码证据见删除日志。

## 2. 已建领域 SSOT

| 领域 | 唯一入口 | 状态 | 本轮核验范围 |
| --- | --- | --- | --- |
| frontend 前端 | [frontend-SSOT.md](frontend-SSOT.md) | 已建；含代码疑点 | 技术栈、路由、鉴权、API 层、账号创建、日志/备份/模块能力边界、SPA embed |
| observability-logging 可观测/日志 | [observability-logging-SSOT.md](observability-logging-SSOT.md) | 已建；含代码疑点与 Mandatory Roadmap | runtime sink、查询/清理、指标、告警、用量日志、隐私通道 |
| deployment 部署/运维 | [deployment-SSOT.md](deployment-SSOT.md) | 已建；含文档漂移 | 两种 compose、Caddy、构建、启动门、迁移与密钥要求 |
| egress-tls-mimicry 出口/TLS/指纹 | [egress-tls-mimicry-SSOT.md](egress-tls-mimicry-SSOT.md) | 已定稿、Owner 保护；本波未改 | 由既有专门 SSOT 管理 |

## 3. 审计与处置索引

| 工件 | 用途 |
| --- | --- |
| [DOC-CONSOLIDATION-MANIFEST.md](DOC-CONSOLIDATION-MANIFEST.md) | 第一波全库基线分类；不是实现真伪依据 |
| [DOC-CONSOLIDATION-DELETION-LOG.md](DOC-CONSOLIDATION-DELETION-LOG.md) | 每个删除文件、删除理由和亲读代码证据 |
| [DOC-CODE-DRIFT.md](DOC-CODE-DRIFT.md) | 文档过期、文档错误及代码疑似缺陷的独立分类表 |

## 4. 尚待建立的领域 SSOT

以下领域仍以 manifest 为工作队列，状态统一为 `PENDING-CODE-VERIFY`；没有领域可因本索引出现而被
默认为已核实：

- relay-gateway 转发链
- protocol-openapi-models 协议/契约/模型
- billing-pricing-payment 计费/定价/支付
- quota-rate-concurrency 配额/限流/并发
- auth-session-rbac 登录/鉴权/会话
- account-pool-dispatch 账号池/选号/调度
- credentials 凭证/OAuth/刷新
- provider-adapters 厂商适配
- reseller-distribution 分销/商户
- hermes 运维助手
- media 媒体
- database-schema 数据库/schema
- testing-release-quality 测试/评审/发布质量
- project-governance 项目治理与 clean-room

## 5. 永久保护边界

- trust-chain / receipt / audit-ledger 专门族：未经 Owner 单独开波，不归并、不删除。
- `docs/research/`、`docs/process/research/`、`docs/decompositions/`、`docs/reference_delta/`、
  `docs/process/evidence/`：原始证据，不因已有摘要而删除。
- `docs/architecture/egress-tls-mimicry-SSOT.md`：已定稿，本波不改。
- Owner 明确缓做、Owner-gated、Mandatory Roadmap 的功能：保留并标注，不以“未实现”为由删除。
