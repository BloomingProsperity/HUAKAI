# 2026-05-08 CPA / CLIProxyAPI 参考项目扫描

| 字段 | 值 |
| ---- | ---- |
| Owner directive | Owner 召回："和我们的也差不多，只是另一种 OAuth 授权来着" |
| Lane | reviewer-only / reference-miner（不读源码细节，仅 README + behavior 摘要） |
| 目标 | 为 HUAKAI 5-passthrough roadmap 锁定一份 OAuth 行为参考（取代被暂停的 Anthropic OAuth 反转决策） |
| Out | 不读 / 不复制 CPA 源码；不在本计划提交任何代码 |

## 项目识别（高置信度）

- 名称：**CLIProxyAPI**（社区简写 "CPA"）
- 仓库：https://github.com/router-for-me/CLIProxyAPI
- License：MIT（clean-room 兼容）
- 语言：Go 100%
- Stars：31.3k
- 最近 release：v6.10.9（2026-05-07）
- 旁证：
  - 姊妹仓库 `seakee/CPA-Manager`（MIT 223 stars，自述"WebUI for CLI-Proxy-API"）
  - 第三方仓库 `basketikun/chatgpt2api` README 出现"CPA、sub2api 号池"并列引用
- 排除项：`wenfxl/openai-cpa`（CC BY-NC-4.0，注册自动化，**不是 MIT，不是 gateway**）

## OAuth 行为差异 vs HUAKAI

| 维度 | HUAKAI 现状 | CPA 现状 |
| ---- | ---------- | ------- |
| Anthropic | OAuth 反转**暂停**（Owner 2026-05-06 directive） | `--claude-login` 浏览器 OAuth → 7 天 token + 自动 refresh |
| OpenAI Codex | apikey + 6 placeholder session adapters | `codex login` 浏览器 OAuth |
| Google Gemini | apikey | apikey / OAuth / Service Account（OAuth ~1h TTL + refresh） |
| Antigravity | 占位 placeholder | **真 OAuth 已实现** |
| Qwen | 未列入 | OAuth（含 Google Social Login） |
| 凭据存储 | 假定数据库 | 可插拔：file / Postgres / Git / Object Store（`storePersister` 抽象） |
| 调度 | （roadmap 中） | 轮询 + fill-first，自动 failover |

## CPA 验证的 3 个 OAuth 模式（HUAKAI 可借鉴）

1. **Pure browser-redirect OAuth** — 不是 cookie/session 反转。CLI 本地 listener 监听 callback，写盘到 `~/.cli-proxy-api/`。Anthropic Pro/Max + Codex + Antigravity + Qwen 都走这一套。
2. **OAuth + 自动 refresh** — token 7 天/1h 不等，但 refresh 完全自动（不需用户重新登录）。
3. **多账号统一 manager** — 一个 `coreauth.Manager` 抽象，所有 vendor OAuth 走同一形态接入，新增 vendor 只补 provider-specific OAuth flow。

## HUAKAI 缺口（按优先级）

1. **真 OAuth（browser-redirect）for Anthropic Pro/Max + Codex** — Owner 此前因风控顾虑暂停。CPA 31k stars 验证此路径成熟低风险。建议 Owner 重新评估解封。
2. **Antigravity OAuth real impl** — HUAKAI 当前 placeholder。CPA 已有，可作行为参考（不读源码，仅 OAuth flow 文档/抓包）。
3. **`storePersister` 多后端凭据存储** — 当前 HUAKAI 假定 DB；CPA 的 file/git/object-store 后端对小型部署更友好（Slice 5+ 评估）。
4. **`fill-first` 调度策略** — 与轮询并行，对预付费配额账号更有意义。

## 安全边界（clean-room）

- CPA 是 MIT 项目，paraphrase-style 行为参考完全合规。
- 仍按 HUAKAI clean-room policy（CLAUDE.md #11）：
  - lane = reviewer / specifier，不抄函数名 / 结构体字段 / 注释 / 文件路径 / 行级算法
  - 任何后续派 codex / 自实现 OAuth 流的 prompt 必须显式声明源 = CPA + commit 时间戳

## 决策点（待 Owner）

1. **是否解封 Anthropic OAuth 反转**？CPA 提供成熟参考。
2. **是否把 CPA 加入 reference-miner 的固定参考列表**（当前已有 sub2api / new-api / all-api-hub）？
3. **是否把 Antigravity 从 placeholder 推到 real OAuth**？这会让 HUAKAI 5-passthrough roadmap 第一个 vendor 落地，而不是继续等所有 6 家 session 反转。

## 来源

- https://github.com/router-for-me/CLIProxyAPI（仓库主页 / README）
- https://github.com/seakee/CPA-Manager（旁证简称 = CPA）
- https://github.com/basketikun/chatgpt2api（社区文献 "CPA、sub2api 号池"）
- https://help.router-for.me/（CPA 官方文档站）
- https://deepwiki.com/router-for-me/CLIProxyAPI（架构索引）

---

**研究 lane**：sonnet general-purpose subagent，2026-05-08 06:00 UTC，非读源码 reviewer-only。
**Status**：reviewer report only — 不触发任何代码改动；待 Owner 决策。
