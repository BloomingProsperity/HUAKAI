# 2026-05-22 外部 AI 对比 critique — Claude+codex 交叉评估

> Owner 2026-05-22 转来一份外部 AI 对 HUAKAI vs sub2api / new-api / CLIProxyAPI 的
> 10 点对比。按 `feedback_owner_input_codex_eval`,Claude 与 codex 各自独立评估再交叉。
> codex lane:`bh7ja8zn6`(独立评估);Claude lane:本文合成。

## 结论

**这份 critique 不改变 12 波补救计划与当前优先级。** Claude 与 codex 独立评估**结论一致**:
约 70% 是外部观察者重新发现 HUAKAI 已知状态(已在 56 条深度审计 / 12 波计划 /
路线图,或 Owner 已拍板)。主线不变:`56 条审计补救 → §1-§16 状态树 → 商业化`。

## 10 点分类

| # | 断言 | 分类 | 依据 |
|---|---|---|---|
| 1 | 后台不够细 / 前端薄 | VALID-KNOWN + 已决 | `project_frontend_state_2026_05_21`,Owner 已决前端搁置到 Rust 四波后 |
| 2 | 商业闭环弱 | VALID-KNOWN | `billing.go:131` placeholder pricing TODO、`settler.go:207` outbox deferred;F-PAY 在路线图 P2 |
| 3 | provider 占位 | VALID-KNOWN | antigravity OCAW 占位 = 审计 C-16 |
| 4 | 协议转换不如 CLIProxyAPI | UNVERIFIED-REFERENCE-CLAIM | 外部 AI 对 CLIProxyAPI 零引用;按 CLAUDE.md #13 不读源码不采信 |
| 5 | Rust route_client.rs 跨平台编译失败 | VALID 但 WONTFIX | 诊断技术上成立(见下「分歧」),但 Owner 2026-05-22 明确:Windows 非交付目标,交付走 Docker/Linux → 实操零影响,不做 |
| 6 | BoringSSL 危险 → 剥 sidecar | ALREADY-DECIDED | `project_r3_rust_sidecar` 早已定 sidecar 路线 |
| 7 | 高并发 TODO | 混合 | storm panic=O-1(**W1 已修**)、slot retry=C-08(W7);`rate.go:75` 429-reset TODO = **VALID-NEW** |
| 8 | API namespace `/admin/v1` vs `/v1/admin` 混用 | **VALID-NEW** | `routes.go` 实测两套前缀并存,`/admin/v1/provider-accounts` 与 `/v1/admin/provider-accounts` 同资源 |
| 9 | 测试过≠没问题 | VALID-KNOWN | 正是 56 条审计 + `feedback_risk_based_testing` 已确立的 |
| 10 | 结构变重(exploratory 1694) | VALID-KNOWN(观察) | git ls-files 确认 exploratory 1694 / backend 816 / docs 627 / frontend 48 |

## 分歧及裁决 — Point 5

Claude 初评据 grep 推断 route_client.rs:682 在 cfg(unix) 块内,判「诊断大概率错」。
codex 实读块结构判「外部 AI 正确」。**Claude 复读源码定论:codex 对,Claude 初评错。**

证据:`route_client.rs:651` 的 `#[cfg(unix)]` 只罩 `parse_proc_status_id`(:651-666)。
`struct UnixSocketConnector`(:668)、`impl UnixSocketConnector`(:673)、
`impl Service<Uri> for UnixSocketConnector`(:681-698,内含 :682 `TokioIo<tokio::net::UnixStream>`
与 :693 `tokio::net::UnixStream::connect`)**均无 `#[cfg(unix)]`**。`tokio::net::UnixStream`
是 Unix-only → Rust 网关无法在 Windows 编译。外部 AI 诊断成立。

**裁决:WONTFIX。** Owner 2026-05-22 明确 Windows 非交付目标,交付走 Docker(Linux)。
`#[cfg(unix)]` 隔离对 Linux-only 交付零功能收益,按 `feedback_relax_self_constraints_for_project_benefit`
不做。诊断技术正确但实操无影响。

## 真正新增项(2 条,均不抢占主线)

| 项 | 去向 | 理由 |
|---|---|---|
| API namespace `/admin/v1` vs `/v1/admin` 混用 | **§状态树 API 收敛 section** | API 表面整理,非安全/钱缺陷,归状态树 |
| `rate.go:75` 多平台 429-reset 解析 TODO | **W7 rider 候选** | 429 reset 影响 cooldown/failover 精度,与 W7 routing 相邻 |

## 外部视角的价值

56 条深度审计按 package 分区时漏了 `backend/internal/rate/`。外部 AI 抓到 `rate.go`
的 429 TODO —— 这 30% 是外部视角的实际贡献,印证 `feedback_owner_input_codex_eval`
让 Owner 给的材料过 Claude+codex 双评是对的。

## 一处框架提醒

外部 AI 用「运营成熟度」标尺量 HUAKAI。HUAKAI 当前阶段优先级不是成为 sub2api,
而是先修干净 56 条审计缺陷让核心可靠。「周边没追上」属实,但周边本就排在核心
修复之后 —— 这是顺序,不是缺失。

---
Lane:Claude 合成 + codex 独立评估(`bh7ja8zn6`)。HUAKAI 内部代码,无 clean-room。
UTC:2026-05-22
