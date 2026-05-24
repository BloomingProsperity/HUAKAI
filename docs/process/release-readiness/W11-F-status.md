# W11-F 指纹波 status (2026-05-24, post F-2.2 + Kiro reason 修正)

> 跟踪 F-2 L1 缺口闭环 + F-1 L2 HTTP/2 接线 的状态 + Feature Preservation Mapping.
> 与 release gates / canary 解锁条件直接关联 (synthesis §4 Canary 政策 + D-S9).

## §1 Feature Preservation Mapping (synthesis §6 Codex D-F2-5)

| Profile | L1 wire 字节级 (boring builder) | L1 runtime preflight gate | L2 HTTP/2 接线 | Production dispatch | 真上游 capture | Canary 解锁? |
|---|---|---|---|---|---|---|
| **Anthropic** | ✅ verified (boring_wire test PASS) | ✅ NotRequired (baseline) | ❌ 缺 (F-1 待启) | ✅ AllowBoring (mimicry-boring feature) | ✅ historical (collected_at 2026-05-06) | ❌ 待 F-1 (D-S9) |
| **Codex CLI** | ✅ verified (boring_wire test PASS) | ⏳ Pending → 待 runtime gate (F-2.3+) | ❌ 缺 (F-1 待启) | ⚠️ AllowBoring per resolver (feature route), 待 dispatch gate 加 preflight gate (F-2.2 集成中) | ❌ 待 F-2.5 (chatgpt.com) | ❌ 待 F-1 + F-2.5 (D-S9) |
| **Kiro CLI** | ✅ verified (boring_wire test PASS) — corrected 2026-05-24 | ❌ Failed (KnownGap, 待 F-2.5 real-upstream capture verify) | ❌ 缺 (F-1 待启) | ⚠️ BlockKnownGap (cautious default) | ❌ 待 F-2.5 (q.us-east-1.amazonaws.com) | ❌ 待 F-1 + F-2.5 (D-S9) |
| **Gemini Advanced** | ✅ verified (boring_wire test PASS) | ⏳ Pending → 待 runtime gate (F-2.3+) | ❌ 缺 (F-1 待启) | ⚠️ AllowBoring per resolver (feature route), 待 dispatch gate 加 preflight gate (F-2.2 集成中) | ❌ 待 F-2.5 (cloudcode-pa.googleapis.com) | ❌ 待 F-1 + F-2.5 (D-S9) |

## §2 已完成 sub-phases

| sub-phase | 状态 | commit | 备注 |
|---|---|---|---|
| F-2.1 spec-dig | ✅ | `fbf28a7` | 4 profile JSON 实读, backend 分类, ML-KEM cross-cutting 标注 |
| F-2.2 l1_preflight 抽象 + per-profile gap | ✅ | `73fe8a5` | L1TlsPreflightStatus / Error 类型, dispatch 集成待 F-2.3+ |
| F-2.2 post-spec-dig 修正 | ✅ | (本 commit) | Kiro reason 改 "real_upstream_capture pending" (boring builder 已能匹配 rustls wire) |

## §3 剩余 sub-phases

| sub-phase | 状态 | 估时 | 依赖 |
|---|---|---|---|
| **F-2.3** dispatch gate 加 preflight check (try_build_http_client_with_profile fallible builder per Codex D-F2-1) | ⏳ 未启 | 0.5-1 codex-day | 无 |
| **F-2.5** 真上游 capture 验证 (per profile, 3 个 staging upstream) | ⏳ 未启 | 0.5 codex-day + ops 协调 | ops staging 环境就绪 |
| **F-2 close-out**: Anthropic baseline 验真上游 + Kiro/Codex/Gemini auto-clear KnownGap | ⏳ 未启 | 0.2 codex-day | F-2.5 完成 |

**ML-KEM PQ group 4588**: 不阻塞. boring 5.1 vendored 已自带 (`mlkem.rs` + `boring-pq.patch`); TLS handshake 默认 advertise X25519MLKEM768. OpenSSL 3.0.13 系统版本无关 (走的是 vendored boring builder, 不是 OpenSSL adapter).

## §4 Canary 解锁条件 (synthesis D-S9 复述)

每个 profile 上 production / canary 必须**同时**满足:

1. ✅ L1 wire byte-level builder 字节级 PASS (本会话 4/4 PASS)
2. ⏳ L1 runtime preflight gate 在 dispatch 路径上 (F-2.2 + F-2.3)
3. ⏳ L2 HTTP/2 接线完成 (F-1, 单独 plan, 5-8 codex-day)
4. ⏳ 真上游 capture 验证 (F-2.5, per profile)

**当前: 1/4 条件满足** (Anthropic / Codex / Kiro / Gemini 都只满足条件 1).

## §5 Owner 决策点

- **OD-W11F-1**: F-2.5 真上游 staging 环境是否就绪? (3 profile target host: chatgpt.com / q.us-east-1.amazonaws.com / cloudcode-pa.googleapis.com)
- **OD-W11F-2**: F-1 (L2 HTTP/2 接线) 是否立刻启动? — 5-8 codex-day, 最重最险, 但是 canary 必经
- **OD-W11F-3**: F-3 profile 模型升级 (sub2api 字段级 db 启发) — 是否进当下 roadmap 还是远期? 不阻塞 F-2/F-1

## §6 之前的错 + 修正

F-2.2 commit `73fe8a5` 中 Kiro `kiro_cli_known_gap_fields()` reason 写 "rustls wire-byte output cannot be precisely replicated". 实际 `boring_wire.rs::kiro_boring_client_hello_byte_level_matches_profile` 测试早就证明 boring builder 能字节级匹配 Kiro template 的 JA3 hash. 错误根源:

- 我没深查 HUAKAI 自家 vendored boring source (`mlkem.rs` / `client_hello_builder.rs`)
- 我没读 `boring_wire.rs` 既有的 4 profile wire 测试
- Synthesis "rustls 不可复刻" 的推理在 F-2.1 spec-dig 阶段没被 wire 测试反证

**修正 (本 commit)**: Kiro reason 改 "real_upstream_capture pending" — 这是真实的 gap (本地 capture PASS 但真上游 capture 未做). 测试 + comment 全更新.

CLAUDE.md #12 source-must-read 教训: 哪怕已读 spec, 自家 vendored library + existing 测试也必须看一遍. Owner 这次 push "有没有看借鉴项目？" 救了一条假 KnownGap.
