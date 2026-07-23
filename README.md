# HUAKAI（华凯）

HUAKAI 是一个可自托管的多租户 AI API 中转、上游账号管理和运营平台。它统一接入官方
API Key、OAuth/Session 订阅账号和受控代理，对下游提供 OpenAI、Anthropic、Gemini
等兼容协议，并完成身份识别、模型解析、池组路由、账号选择、协议转换、出站、计费、
配额、日志、健康反馈和失败恢复。

## 当前状态

- 后端、数据库迁移、生产容器和多类协议入口已经存在。
- 当前仓库没有可信的生产前端，部署形态是 API-only；旧页面不能作为完成证据。
- 代码存在不等于已经通过全部真实厂商账号、模型和生产拓扑验证。
- `/v1/realtime` 尚未实现。
- 项目尚未达到“直接上线”状态；以白皮书中的主线状态和发布门为准。

## 三类业务身份

1. **部署者**：管理系统、平台自有用户、上游账号、模型、池组、代理、资金和运维。
2. **下级租户管理员**：管理本租户用户、Key、余额和获授权的账号/池组能力。
3. **最终用户**：使用租户签发的 HUAKAI Key 调用模型并查看自身用量。

部署者可以调整下级租户钱包，但不得越级修改下级租户最终用户的余额或资料；租户管理员
不得跨租户访问。

## 核心链路

```text
客户端请求
  -> HTTPS 边缘
  -> HUAKAI Key 身份与租户
  -> 模型目录与能力检查
  -> 池组路由
  -> 账号健康、配额和并发选号
  -> 凭据与账号代理
  -> 协议适配与标准/特定 TLS 出站
  -> 上游响应或流式传递
  -> 计费、余额、配额、日志和健康反馈
  -> 重试、回退、DLQ 或人工恢复
```

完整产品思路、架构图、模块和运行链见
[《HUAKAI 项目与架构白皮书》](docs/HUAKAI项目与架构白皮书.md)。

## 生产组件

| 组件 | 责任 |
| --- | --- |
| Go Gateway | 数据面、控制面、运维面和后台任务 |
| PostgreSQL 16 | 业务事实、账本、配置、日志索引和恢复事实 |
| Redis 7 | 跨实例短期速率限制状态，不作为业务事实库 |
| Rust TLS Sidecar | 为特定账号转 API 提供客户端指纹出站；当前也被 CRS 安全源复用，并参与 Gateway 启动与就绪 |
| Caddy | HTTPS 终结和反向代理 |
| 官方 Hermes Runner | 可选运维助手，通过租户隔离的内部工具端点接入 |

官方 API Key 默认走 Go 标准网络栈；认证模式明确要求客户端指纹的会话账号进入 Rust Sidecar，
二者不得静默互相回退。长期目标是把 Sidecar 依赖收窄到确实需要客户端形态的账号转 API，
但当前主线仍让 CRS 安全源复用该进程，并在 Gateway 启动和 readiness 中全局探测它；部署时必须
按当前实现同时启动，不能按目标架构提前省略。

## 文档入口

| 需要了解什么 | 权威入口 |
| --- | --- |
| 产品、架构、模块、运行链和路线 | [项目与架构白皮书](docs/HUAKAI项目与架构白皮书.md) |
| 核心机制、算法、状态机、事务、幂等和失败恢复 | [工程设计手册](docs/HUAKAI工程设计手册.md) |
| 第一方生产源码文件、声明、功能归属和保守影响半径 | [源码责任索引](docs/源码责任索引.md) |
| 开发、clean-room、测试、PR 和发布规则 | [项目规则](docs/RULES.md) 与 [Agent 规则](AGENTS.md) |
| 开发参与方式 | [贡献指南](CONTRIBUTING.md) |
| HTTP API 合同 | [OpenAPI](docs/openapi/openapi.yaml) |
| 数据库合同 | [PostgreSQL migrations](backend/sql/migrations/) |
| 部署、升级、监控和故障恢复 | [运维手册](docs/runbooks/README.md) |
| 安全漏洞报告 | [安全报告政策](SECURITY.md) |
| 法律、商标和责任边界 | [LEGAL.md](LEGAL.md) 与 [LICENSE](LICENSE) |

Issue、PR、commit、CI 和 GitHub Release 是开发操作记录的权威载体。普通缺陷不再在
`docs/process` 新建单独报告；重大生产或安全事故才保留 RCA。

## 仓库结构

| 路径 | 责任 |
| --- | --- |
| `backend/cmd/gateway/` | 生产组合根、路由、依赖注入和生命周期 |
| `backend/internal/` | 身份、账号、模型、路由、协议、计费、运维和恢复等领域实现 |
| `backend/sql/` | 查询源和 PostgreSQL 迁移 |
| `exploratory/rust-core-gateway/merged/` | Rust 工作区；生产镜像只构建其中的 `tls-sidecar` |
| `docs/openapi/` | 机器可读 API 合同 |
| `docs/runbooks/` | 现行运维手册和故障 playbook |
| `.agents/skills/` | 项目工作流 Skill 的唯一来源 |

## 开发验证

从 `backend/` 开始：

```bash
go test ./...
./scripts/integration-pg.sh
go test -tags smoke ./cmd/gateway
```

集成、迁移、Rust、容器和真实账号验证按变更爆炸半径增加；局部单测不能冒充全链路或
上线证据。

## 合规与责任

HUAKAI 不隶属于任何上游 AI 厂商，也不附带账号、凭据或秘密。部署者必须确保其账号、
网络、数据处理和服务方式符合上游条款与所在地法律。项目按 `LICENSE` 和 `LEGAL.md`
提供，不对账号封禁、服务中断、资金损失或违规使用承担保证。
