# 2026-05-24 Ref-Anchor Ledger (CLAUDE.md #12 First-Cite Recency Check)

UTC: 2026-05-24T07:25Z
Source of truth for any 2026-05-24-* plan citing reference projects.
Owner directive: "他们都持续更新,必须去官网拉最新的" — 拉 latest tarball 到 ~/refs-latest/<name>-extracted/。

## Anchor Table

| 项目 | 正确 GitHub owner/repo | 最新 SHA (HEAD) | committed_at (UTC) | License | 用途 | 本地路径 |
|---|---|---|---|---|---|---|
| CLIProxyAPI | router-for-me/CLIProxyAPI | 50d19e204fed | 2026-05-23T21:19:43Z | MIT | 主 ref:OAuth/PKCE/session adapter | ~/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/ |
| litellm | BerriAI/litellm | 414866767176 | 2026-05-23T23:57:14Z | Apache-2.0 | GitHub Copilot device-code / multi-vendor 适配 | ~/refs-latest/litellm-extracted/litellm-main/ |
| sub2api | **Wei-Shaw/sub2api** | 63b0631a5827 | 2026-05-23T06:40:10Z | **LGPL-3.0** | channel monitor / oauth service (paraphrase only) | ~/refs-latest/sub2api-extracted/sub2api-main/ |
| new-api | **QuantumNous/new-api** | ebbe31553309 | 2026-05-23T05:24:56Z | Apache-2.0 | multi-vendor gateway 模式 | ~/refs-latest/new-api-extracted/new-api-main/ |
| portkey-gateway | Portkey-AI/gateway | d2ea41f4e17c | 2026-05-18T07:43:22Z | MIT | vendor registry / endpoint catalog | ~/refs-latest/(本地 ~/refs/portkey-gateway 即 latest) |
| envoy-ai-gateway | envoyproxy/ai-gateway | 3d3d346d09e4 | 2026-05-23T19:46:38Z | Apache-2.0 | outbound config / per-vendor profile | ~/refs-latest/envoy-ai-gateway-extracted/ai-gateway-main/ |
| helicone | Helicone/helicone | 094b210b405a | 2026-05-18T23:17:54Z | Apache-2.0 | observability / latency dashboard | ~/refs-latest/(本地 ~/refs/helicone 即 latest) |
| llmgateway | theopenco/llmgateway | d4d67517cfac | 2026-05-23T02:27:14Z | MIT | rate limit / pricing config | ~/refs-latest/llmgateway-extracted/llmgateway-main/ |

## 校正项 (相对 2026-05-24-*-claude.md 起初引用)

- **sub2api**:Claude 之前的 plan 标注 owner 是 `BerriAI/sub2api`(错);正确是 `Wei-Shaw/sub2api`。所有 cite 必须用 `Wei-Shaw/sub2api@63b0631a5827`。
- **new-api**:原 owner `Calcium-Ion/new-api` 已改名为 `QuantumNous/new-api`(GitHub 301 redirect)。cite 用 `QuantumNous/new-api@ebbe31553309`。
- **CLIProxyAPI**:Claude plan 写的 `50d19e204fed` SHA 跟 latest 一致 ✓
- **litellm**:本地 ~/refs/litellm 是上轮 fetch (79b4578671);latest 是 414866767176。`~/refs-latest/litellm-extracted/litellm-main/` 是最新。
- **portkey / helicone**:本地 ~/refs/ 已经是 latest(同 SHA),不必重读 ~/refs-latest/。
- **envoy-ai-gateway / llmgateway**:本地落后 ~1-3 commits,以 ~/refs-latest/ 为准。

## Recency check pass (CLAUDE.md #12)

- ✓ archived=False / disabled=False
- ✓ pushed_at 均在 90 天内 (实际 6 天内)
- ✓ HEAD SHA + 时间戳记录 (本表)
- ✓ 验证 production code 在 main 包,不是只在 tests/ (CLIProxyAPI internal/auth/ / litellm/llms/ / 等已读)

## 给 codex 重派的指令模板

任何 2026-05-24-*-codex.md plan 重派时必须告诉 codex:

> 必读资源:
> - 引用 ref 项目用 ~/refs-latest/<name>-extracted/<repo-main>/ 不是 ~/refs/<name>/
> - cite 用本 ref-anchor 表里的最新 SHA + owner (例如 Wei-Shaw/sub2api@63b0631a5827)
> - 不要用 ~/refs/ 旧 clone 的 SHA (它们已 stale)

## 来源

- GitHub API v3 commits/repos endpoint (本日 fetch)
- 本地 ~/refs/<name>/.git origin remote 验证 owner 正确性
- codeload.github.com tarball 下载到 ~/refs-latest/
