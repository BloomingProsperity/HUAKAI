# 2026-05-17 Wave R-3 6 vendor mimicry + stream parser — Claude

| 字段 | 内容 |
|---|---|
| 前置 | Wave R-2-B 闭环 ([5529c2b](../../../tree/5529c2b)), Anthropic byte-level JA3 wire = de88744b20558d50f03a5f0ea176ee98 |
| 闭环目标 | 让其它 5 个 vendor (Codex CLI / Kiro / Gemini / OpenAI ChatGPT / GitHub Copilot / Cursor) 都享受 R-2-B-2-extend 路径的 byte-level wire control; 加 SSE stream parser |
| 派工 | Claude 写 plan + spec (反代敏感); codex 写中性 Rust 代码 |
| 估时 | 8-12 hr codex 拆 4-6 sub-phase |

---

## 1. 现状

- BuiltinProfile enum 已 4 vendor: CodexCli, KiroCli, GeminiAdvanced, AnthropicClaudeCode
- backend_resolver 当前只把 Anthropic 绑 Boring (R-2-B-5 commit 5529c2b); 其它 3 个还是 OpenSSL adapter 或 KnownGapBlocked
- 缺 3 vendor profile: OpenAI ChatGPT (ChatGPT 网页/桌面 client) / GitHub Copilot / Cursor
- HUAKAI tools/fingerprint-collector/templates/ 已有 codex-cli.json / kiro-cli.json / gemini-advanced.json / anthropic-claude-code.json (R-1+R-2-B 数据基础)

## 2. R-3 Sub-phase 拆分

### R-3-A (1.5 hr): 3 vendor backend_resolver → Boring extend

scope:
- mimicry/backend_resolver.rs `resolve_profile_mimicry_backend()`:
  - match profile.vendor:
    - Anthropic → 现有 Boring > Openssl > KnownGapBlocked (R-2-B-5)
    - OpenAi → 同优先级 (Boring > Openssl > KnownGapBlocked) [新]
    - Kiro → 同 [新]
    - Gemini → 同 [新]
- anthropic_test.rs 加 3 个 vendor 等价 test case (each: prefers_boring / falls_back_to_openssl / blocked)
- boring_wire.rs 加 3 个 vendor byte-level wire test (复制 anthropic_boring_client_hello_byte_level_matches_profile pattern, 用各 vendor profile.tls.ja3_hash 校准)
- 注意: 各 vendor profile 的 JA3 sample 可能跟 Anthropic 不同 (cipher list / extension 顺序), 但 R-2-B-2-extend 路径同样适用 (HUAKAI ja3_wire 算法是通用 JA3 hash, ECH/OCSP/SCT injection 跟 vendor 无关)
- cargo test --features mimicry-boring --lib 期望 85+ PASS

风险: 某 vendor profile 缺关键字段 (e.g. profile.tls.extensions 为空), 这时 backend_resolver 应该 fallback OpenSSL 而非 panic. R-3-A codex 必须先 Read profile.tls.extensions 校验.

### R-3-B (3-4 hr): 3 new vendor profile JSON + builtin enum 扩展 (Owner 本机 fingerprint-collector 配合)

scope:
- BuiltinProfile enum 加 ChatGptWeb / GitHubCopilotIde / CursorIde 3 variant
- ProfileVendor enum 加 GitHubCopilot / Cursor (OpenAI 已有, ChatGptWeb 走 OpenAI vendor)
- ProfileMode enum 加 chat_gpt_web / github_copilot_ide / cursor_ide
- tools/fingerprint-collector/templates/ 加 3 个 JSON:
  - chat-gpt-web.json (真采样 chatgpt.com web client)
  - github-copilot-ide.json (真采样 GitHub Copilot 桌面 IDE plugin)
  - cursor-ide.json (真采样 Cursor IDE)
- 三个 JSON 字段 schema 跟 anthropic-claude-code.json 一致 (TLS + h2 + sec-ch-ua)

**Owner 本机配合 (R-DEP-003 新 risk)**:
- HUAKAI tools/fingerprint-collector 是 Go 工具 (pcap + JA3/JA4 抽取), Owner 在本机跑:
  - openssl s_client + wireshark 或 fingerprint-collector 抓 ChatGPT 网页 / Copilot / Cursor 各 1 个 ClientHello pcap
  - tools/fingerprint-collector/cmd/fpc parse pcap → 输出 template JSON
  - JSON 放 templates/ 即可
- Sandbox 不能跑真采样 (没真账号 + 沙箱网络受限); 这是 Owner 必须本机做的 1 步

如果 Owner 不本机采样, R-3-B blocked, 跳到 R-3-C.

### R-3-C (3-4 hr): SSE stream parser

scope:
- 新 crates/core_gateway/src/proxy_engine/sse_parser.rs:
  - 解析 SSE response body (text/event-stream)
  - 协议: lines `event: <name>\n` + `data: <json>\n\n` (RFC eventsource spec)
  - HUAKAI 自写 parser, 不读 reqwest-eventsource / hyper-tungstenite source
- 接 backend_resolver: Boring backend → 走自家 sse_parser; 其它 backend 路径不动
- 加 mimicry-aware response parse: 真还原 sec-ch-ua / Server / 各 vendor 特定 header (per R-3-A profile 的 http_profile)
- 单测: 5 vendor 的 SSE response sample → parser PASS

依赖: R-3-B 完成新 profile JSON 提供 SSE format reference, 否则只能跑 Anthropic + 现有 3 vendor.

### R-3-D (1 hr): backend_resolver dispatch end-to-end wire

scope:
- mimicry/dispatch.rs: 把 Boring + Anthropic profile → BoringTlsConnector + build_http_client_with_profile (R-2-B-3 commit 6f2cbe9 公开) 真接到生产 path
- 现在 R-2-B-5 (commit 5529c2b) backend_resolver 返 Boring 但 dispatch 还是 select 层, 没走到真 HTTP client. 本 sub 闭环
- 加 e2e test (mock TLS server + 验真发出 ClientHello via Boring path) — R-2-B-4 boring_wire 已有 wire test fixture, 本 sub 加一层 e2e 业务 test

## 3. 优先级 + 顺序

```
R-3-A (1.5 hr, 不依赖 Owner) ← FIRST
  ↓
R-3-D (1 hr, dispatch end-to-end wire, 不依赖 Owner) ← SECOND
  ↓
R-3-C (3-4 hr, SSE parser, 不依赖 Owner) ← THIRD
  ↓
R-3-B (3-4 hr, 需 Owner 本机 fingerprint-collector 采样) ← LAST (依赖 Owner)
```

R-3-A → R-3-D → R-3-C 都 sandbox 内可做. R-3-B 需要 Owner 本机配合, surface 决策点.

## 4. Owner Decision Point

- R-3-B 是否本 wave 内做? Owner 本机跑 fingerprint-collector 抓 ChatGPT/Copilot/Cursor 真采样?
  - 选 yes: R-3-B 跑完, R-3 完整闭环 6 vendor
  - 选 defer: R-3 跑 A+D+C 3 个 sub, 6 vendor 缺 3 留 R-4 或后续 wave (HUAKAI 已 cover 4 个 vendor: Anthropic + Codex CLI + Kiro + Gemini, 是 release v1 主力)

## 5. Risks

| 编号 | 类型 | 严重度 | 描述 | Mitigation |
|---|---|---|---|---|
| R-DEP-003 | dep (新) | LOW | R-3-B 需 Owner 本机 fingerprint-collector 跑 ChatGPT/Copilot/Cursor 真采样 | surface Owner 决策, R-3-A/C/D 不阻塞 |
| R-MIMICRY-003 | algorithm (新) | MED | 某 vendor profile.tls.extensions 跟 Anthropic 不一样 (e.g. 无 ECH), 但 client_hello_builder.rs 默认开 ECH → 真发出比 profile 多 1 个 ext, byte-level FAIL | R-3-A codex 加 profile-aware ECH/OCSP/SCT gate (按 profile.tls.extensions 是否包含 65037/5/18 决定是否启用) |

## 6. 派工

| 角色 | 任务 |
|---|---|
| Claude | 本 plan + R-3-A 后 surface 决策 R-3-B |
| codex | R-3-A → R-3-D → R-3-C 顺序 dispatch |
| Owner | R-3-B 本机采样 (可选) + R-3 end commit 验证 |

## 7. 不动

- frontend / Go backend / LICENSE / 计费 / control plane tonic+rustls
- Anthropic profile (R-2-B 闭环, 不动)
- R-2-B-1..5 已写代码 (除 backend_resolver extend + dispatch + 新 profile 接入)

---

Plan: Claude Opus 4.7 直写, 反代敏感 spec
UTC: 2026-05-17T~12:00:00Z
