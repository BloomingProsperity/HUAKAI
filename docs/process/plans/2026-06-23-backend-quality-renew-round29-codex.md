# 2026-06-23 backend quality renew round29

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；并遵守"不要触碰到另一个目标" |
| Scope | 本轮只审查 `backend/internal/cache*`、`backend/internal/cache_routing`、`backend/internal/cacheplan`、`backend/internal/cachemetrics` 与 L2 缓存命中后的 `CommitCacheHit` 相关生产链路、测试和注释纪律；不进入另一个 security scan 目标，不修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 找到并核实缓存命中后账本、配额、审计、provider account / acquisition token 不变量中的代码质量或测试质量债务；每条 finding 有真实 `file:line`、函数或类型、触发条件和可执行修法；若未发现 S1/S2，明确说明已读范围和残余风险。 |
| Time estimate | 本轮约 30-60 分钟代理时间，按源码阅读和测试覆盖核对推进。 |
| Blast radius | 只读审查加计划文件，业务代码无变更；风险主要是误判缓存命中钱路或重复既有 findings。 |
| Failure modes | 1. 文档状态陈旧导致误判：以 `.go` 真码和测试为准。2. 只读 handler 表层漏掉 billing 内部：沿 `CommitCacheHit` 调用链读到 `billing` 与调用方。3. 把纯安全问题展开：遇到跨租户或密钥泄露只标"转 security 专项"。4. 触碰另一个目标：保持只读且不打开 `backend-security-scan` 计划。 |
| Decision points | 若发现需要改 billing ledger、quota enforcement、数据库 schema 或真实部署配置，只作为 finding 交 Owner 确认，本轮不直接改。 |
| Pre-execution checklist | 1. 已重新读取目标文件。2. 已读取 `api-gateway-risk-review` skill。3. 已确认当前 worktree 存在另一个 security scan 计划文件并避开。4. 先用 `rg` 定位 `CommitCacheHit`、`response_cache_l2`、`provider_account_id`、`acquisition_token`。5. 核对测试是否真实覆盖缓存命中钱路。 |

## Concrete Execution Order

1. 定位 `CommitCacheHit` 定义和所有调用点，画出缓存命中到 billing settle 的实际路径。
2. 阅读缓存读取、写入、命中判定和 routing 相关代码，确认命中路径是否强制空 `provider_account_id/acquisition_token` 并保留审计证据。
3. 阅读 billing 的 `CommitCacheHit` 实现，确认幂等、金额、usage source、quota/ledger 交互。
4. 阅读相关测试，检查是否能让字段漂移、双记账、漏记账、错误 source 等 mutation 变红。
5. 输出中文 findings，并把未跑测试原因如实说明。
