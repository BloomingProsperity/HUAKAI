# 2026-05-17 Wave R-8: Owner 本机真上游 Smoke Release Gate — Claude

| 字段 | 内容 |
|---|---|
| 前置 | R-2-B + R-3-A-fix + R-3-C + R-3-D + R-4 + R-5 + R-6 + R-7 全闭环 (8-12 day codex 累计) |
| 闭环目标 | 4 vendor (Anthropic/OpenAI/Gemini/Codex CLI) Owner 本机真上游 smoke PASS → release v1 gate |
| 派工 | Owner 本机 (sandbox 不能跑真上游, 4 vendor 限定 per memory project_real_vendor_account_scope) |
| 估时 | Owner 2-4 hr 本机 |

---

## R-8 Owner 本机 checklist

### 前置环境

- 本机装 toolchain: clang + cmake + libclang-dev + LIBCLANG_PATH (per R-2-B-1 R-DEP-002 mitigated)
- 本机 git pull origin claude/phase-1 (最新 HEAD)
- 本机 cargo workspace build:
  ```
  cd exploratory/rust-core-gateway/merged
  CARGO_TARGET_DIR=$HOME/.cargo-target LIBCLANG_PATH=$(...) cargo build --features mimicry-boring --release
  ```
- 本机有 4 vendor 真账号: Anthropic API key + OpenAI Codex CLI session + Gemini Vertex / 2.5-pro key

### Smoke test 4 vendor

每 vendor 跑 smoke 步骤:

1. **Anthropic**:
   - `anthropic-claude-code` profile + BoringMimicry backend
   - 真发 1 个 messages 请求到 api.anthropic.com
   - 验: 200 OK + streaming response + 无 challenge
   - 验 byte-level JA3 = de88744b20558d50f03a5f0ea176ee98 (用 Wireshark / tcpdump 抓真包)
   
2. **OpenAI Codex CLI**:
   - `codex-cli` profile + BoringMimicry (R-3-A-fix 闭环后)
   - 真发 chat 请求到 chatgpt.com (Codex CLI 真 endpoint)
   - 验: 200 OK + JA3 跟 profile sample 一致

3. **Gemini**:
   - `gemini-advanced` profile + BoringMimicry
   - 真发 generateContent 到 generativelanguage.googleapis.com
   - 验: 200 OK + JA3 命中

4. **Kiro CLI** (Amazon Q Developer):
   - `kiro-cli` profile + BoringMimicry
   - 真发到 q.us-east-1.amazonaws.com (sigv4 + UDS)
   - 验: 200 OK + JA3 命中

### Smoke 验证产出

- 4 vendor 真上游响应 captured pcap (附本 plan 同 dir)
- 各 vendor JA3 比对: 真 wire == profile sample
- 各 vendor latency baseline (p50 / p95) 记录
- 各 vendor 真 cost (token + USD) 记录

### Smoke fail handling

- 任一 vendor 真上游拒接 (403 / challenge / cert mismatch) → 阻塞 release, 倒排查:
  - profile mismatch? → R-3-B 同步真 fingerprint sample 重写 profile JSON
  - boring patch wire 仍不对? → R-3-A-fix-5 重 verify
  - WAF 主动 challenge? → R-7 active anti-detection 启用 + retry
- 全 PASS → release v1 gate 闭环, 可 push 到 prod

### 输出

新 `docs/release-notes/v1-r8-smoke-results-2026-MM-DD.md` (Owner 写):
- 4 vendor smoke 结果矩阵
- pcap 文件路径
- 任一 fail → 详细 reproduce + blocker 编号

---

## Risks

| 编号 | 类型 | 严重度 | 描述 | Mitigation |
|---|---|---|---|---|
| R-R8-001 | reliability | HIGH | Sandbox 测试 PASS 不等于真上游 PASS (本地 mock 跟真 WAF 行为不一致) | 本 R-8 是唯一 release gate; R-3-A-fix wire match 是必要不充分条件 |
| R-R8-002 | cost | LOW | 4 vendor 真 smoke 消 small amount tokens (< $1 USD 累计) | 不阻塞, 但 audit Owner billing |

## 不动

- frontend / Go / LICENSE / 计费
- 不在 sandbox 跑真上游 (Owner 本机限定)

Plan: Claude Opus 4.7 直写
UTC: 2026-05-17T~13:25:00Z
