# W11-F F-1 — L2 HTTP/2 fork → ProxyEngine 真接线 (synthesis)

**Synthesis lead**: Claude (PM-Orchestrator)
**Date**: 2026-05-25 UTC
**Inputs**:
- `docs/process/plans/2026-05-25-w11f-f1-l2-http2-jiexian-claude.md` (Claude 独立 draft)
- `docs/process/plans/2026-05-25-w11f-f1-l2-http2-jiexian-codex.md` (Codex 独立 draft, 144k tokens, 真读了 `0x676e67/http2@a33b27e` 的 LICENSE/Cargo.toml/client.rs/frame/share.rs)

**Methodology**: CLAUDE.md #10 plan-trio — 两份 draft 独立成稿 (互不参考), 现合成对照
agree / conflict / gaps, 由 Owner 拍板执行版.

**Bottom line**: 两份 plan 整体方向高度一致 (5-7 sub-phase 分解, L2 typed gate 镜像 F-2.3+
L1 模式, build_mimicry_action 错误映射沿用 F-2.3+ round 2 pattern). 主要差异在
**真上游证据的优先级**: Codex 第一件事就是把"Anthropic 模板根本没有 H2 capture data,
F-1 不能在没真证据前 Released" 这个事实摆出来 (Claude 漏看 anthropic_claude_code.json
的 h2_settings_frame.available=false). 这个差异决定了 F-1 实际执行的第一步.

---

## §1 Agreement (两份 draft 共识)

| 维度 | 共识 |
|---|---|
| **目标** | 把 `HttpTwoMimicryAdapter` 从 in-memory duplex 测试脚手架升级为 ProxyEngine 真接线; mimicry-boring + mimicry-http2-fork 双 feature build 出来的 gateway 要 byte-level match Anthropic 上游 H2 SETTINGS + pseudo-header order |
| **typed gate 复用** | 新 L2 typed gate 镜像 F-2.3+ 的 `L1TlsPreflightStatus`/`Error` 结构, 复用 `MimicryProductionCanaryError`, 拒 Pending |
| **dispatch 错误映射** | `build_mimicry_action` 沿用 F-2.3+ round 2 pattern (commit a358b70): match Err → `MimicryAction::Block*`, 不 panic |
| **mutation discipline** | 每 sub-phase 列具体 mutation discriminator (改 X 则 Y 测试红), 不只是"加测试" |
| **clean-room** | http2 fork 是 MIT (Codex 实读 LICENSE 证), 引用 OK; 不抄代码; 每 commit `Clean-room-attestation:` |
| **行 phase 拆 commit** | 每 sub-phase 独立可 commit + 独立 codex review 闭环 (CLAUDE.md #8) |
| **Out-of-scope** | 不动 Go backend / auth / billing / quota / schema; 不动 stock hyper h1 路径; 不解决 H2 GREASE (RFC 8701) |

---

## §2 Conflicts (差异点 + winner)

### C-1 第一步是什么 (最重要分歧)

| | Claude draft | Codex draft |
|---|---|---|
| 第一步 | F-1a: http2 fork connector spike (写代码) | F-1a: Evidence and fixture contract (写证据规范, 先看现状有没有真数据) |
| 假设 | Anthropic 模板已有可用 H2 字段 | 实读 `anthropic_claude_code.json:113-127` 发现 `h2_settings_frame.available=false` |

**Winner: Codex.** Codex 实读了 profile JSON, 发现 HUAKAI **目前不持有任何真实 Anthropic
H2 SETTINGS / pseudo-header bytes**. 这是 Claude 漏看的关键事实. 没有真证据, 后续
"byte-byte match upstream" 的全部测试都是 mock-vs-mock — 不算 mimicry parity, 只算
adapter 自测.

**合并执行**: Codex 的 sub-phase a (Evidence and fixture contract) 作为 F-1 第一步;
Claude 的 F-1a (connector spike) 后移为 sub-phase b.

### C-2 transport seam 拆 / 不拆

| | Claude draft | Codex draft |
|---|---|---|
| sub-phase 数 | 1 个 (F-1c, 内部说"拆 2 commit c.1/c.2") | 2 个独立 sub-phase (F-1d transport boundary + F-1e fork client) |
| blast radius | 标记最险 | 拆分后 d 行为保守, e 是真接线 |

**Winner: Codex.** 拆成 2 sub-phase 比"1 sub-phase 内部 2 commit"更显式; codex review
每 commit 闭环 (CLAUDE.md #8); 出红时回退粒度更细.

### C-3 现成基础设施复用 (Codex 发现的盲点)

Codex 实读 `proxy_engine/boring_tls_connector.rs:163-178/249-258` 发现:
- `BoringTlsConnector` 已能区分 https (TLS) vs http (plain TCP)
- 已暴露 `negotiated_h2` ALPN 结果
- 已记录 hyper connection metadata

**Claude 漏看了**这些. 后果: Claude F-1c 估时 2 codex-day (assumes 全新 wiring);
Codex F-1d+e 估时 0.8 + 1.2 = 2 codex-day (assumes 复用 boring_tls_connector 的
连接拆分逻辑). 实际时间应接近 Codex 估值.

**合并执行**: F-1 sub-phase e (HTTP/2 fork outbound client) 复用现有
boring_tls_connector 的 TLS 握手 + ALPN 检测, 仅在 ALPN=h2 后接管 H2 protocol layer.

### C-4 时间估算

| | Claude | Codex |
|---|---|---|
| nominal | 7 codex-day | 4.8-5.8 codex-day |
| upper | 10.5 (+50% buffer) | 不显式标 upper |

**Winner: 取中, 加 buffer.** Codex 的 4.8-5.8 day 假设理想路径 (boring_tls_connector
复用 / transport seam 一刀切干净); Claude 的 7 day nom 把 relay 适配高估. 现实
**5-6 codex-day nom, +30% buffer → 6.5-7.8 codex-day upper**. 在 Owner OD 给的
5-8 day 区间内偏中后段.

### C-5 Owner decision points 合并

去重后 9 项, 标 \[CL\]=Claude 提, \[CX\]=Codex 提, \[CL+CX\]=双方都提:

| # | choice | options | 推荐 | 来源 |
|---|---|---|---|---|
| **D1** | 第一步是抓数据还是写代码 | A: 先 fixture contract + 验 capture (Codex order); B: 先写 connector spike (Claude order) | A | \[CX\] (合成接受) |
| **D2** | sub-phase 串行 vs 半并行 | A: 全串; B: F-1a 完后 F-1c (preflight) + F-1d (transport) 并行 | A | \[CL\] |
| **D3** | response body 抽象 | A: `Box<dyn Body>` (+~1% perf); B: enum 双 variant (0 cost, 代码膨胀); C: trait object via type erasure | A 先做 + bench, 真红线切 B | \[CL+CX\] |
| **D4** | http2 fork 长期归属 | A: 保 git-dep pin rev; B: vendor 进 `vendor/http2/`; C: 换 maintained alt (rquest/reqwest-utils) | A 短期 (F-1 内); B/C 留 F-1 完后 single follow-up | \[CL\]; Codex 提风险但不给 B/C |
| **D5** | F-1e (Codex 用 F-1g) 真上游 capture 路径 | A: mitmproxy + CA install (F-2.5 同款, security risk); B: byte-only Python TCP proxy (无 CA) | A (复用 F-2.5 addon) | \[CL\] |
| **D6** | Codex/Kiro h2 字段空白处理 | A: 保 KnownGap 不填; B: F-1e capture 补 wire 数据关 KnownGap; C: 看真捕获结果再定 | C (按数据说话) | \[CL\] |
| **D7** | Connection model | A: 每请求新 H2 握手 (byte 证明易, perf 差); B: pooling (closer to hyper perf, byte 测试间接) | A 先 (F-1 内); B 留 follow-up | \[CX\] |
| **D8** | Release profile set | A: F-1 只 Release 有真 fixture 的 profile, 其他 Mandatory Roadmap; B: 阻塞 F-1 直到所有 4 个 builtin 都有真 capture | A (与 F-2.5 evidence-driven 精神一致) | \[CX\] |
| **D9** | Request trailers | A: F-1 只支持 empty + DATA body, 拒/log unsupported request trailers 标 KnownGap; B: 完整 trailer 支持 | A (gateway 上游 API 不依赖) | \[CX\] |
| **D10** | L2 preflight timing | A: 在 client build time 跑 (fail-fast); B: 在 first request 跑 + cache | A (与 L1 build-time 一致) | \[CX\] |

---

## §3 Gaps each side missed (cross-fill)

### Codex 漏 (Claude 补)

- **G-CX-1 feature-matrix CI 扩展**: Claude R5 — `tools/feature-matrix/verify.sh` 现在
  只跑单 feature, 需加 `--features mimicry-boring,mimicry-http2-fork` 组合矩阵. Codex
  只说"feature gating compile checks" 没给具体动作.

- **G-CX-2 perf regression gate**: Claude R7 — 加 hdrhistogram bench (dev-dep 已有),
  F-1c 后 p99 增加 > 5% 即回滚. Codex 说"per-request handshake 慢, 留 follow-up"
  但没给数值守门.

- **G-CX-3 vendor http2 fork 作为正式 follow-up**: Claude D-F1-C C2 + OOS-follow-up-A —
  把 git-dep 改 path-dep, 像 boring 那样 vendor. Codex 只说"rev 已 pin" + "Owner
  approve before bump", 没提 vendor 化方案.

- **G-CX-4 time buffer 显式标**: Claude 给 +50% upper bound. Codex 没给.

- **G-CX-5 mimicry-http2-fork 与 stock-h2 在 Windows native-tls schannel 路径**: Claude
  R6 — F-2.5 已发现 codex 在 Windows native-tls = schannel; 如果用户开了 mimicry-http2-fork
  但没开 mimicry-boring, fork 配 stock TLS 在 Windows 仍可能 schannel. Codex 没显式
  讨论这个 feature 组合的退化情况.

### Claude 漏 (Codex 补)

- **G-CL-1 现状 Anthropic profile 无真 H2 数据** (\*\*最关键\*\*): Codex §0 truth-first
  note + §4-a evidence contract. 不修这一步, 后续全部"byte match" 都是 mock vs mock.
  → 已采纳为 sub-phase a (合并方案 §4).

- **G-CL-2 boring_tls_connector 已有的复用面**: Codex §4-e — 已分 https/http, 暴露
  `negotiated_h2`. Claude F-1c 估时 2 day 因为没看这部分; 用上后 F-1e 缩到 1.2 day.

- **G-CL-3 fork client::Builder 实际能跑 generic AsyncRead+AsyncWrite**: Codex 实读
  `0x676e67/http2@a33b27e:src/client.rs:1312-1321` 确认 `Builder::handshake` 接 generic.
  Claude R1 把"fork 不支持 custom IO" 列为风险; Codex 已用 source 排除这一风险.

- **G-CL-4 HPACK parser 测试质量风险**: Codex §9 — 现有
  `mimicry_http2_adapter_test.rs:141-193` 自写 HPACK static-name parser, 可能在 order
  错时仍 pass. F-1 应加 negative fixture 或 move 到带 mutation tests 的复用 helper.
  Claude 没注意这个隐藏的测试质量问题.

- **G-CL-5 profile validation 已经在拒空字段**: Codex 引用 `profile.rs:377-403` /
  `http2_adapter.rs:65-81` 证明现有代码已经在拒 "available=true 但字段空". F-1 加
  的 evidence contract 只是把这个事实在文档侧固化 + 加 cross-check 测试. Claude 没
  注意这个已有的保护层.

- **G-CL-6 ALPN h2 检测 fail-closed**: Codex R/ALPN — fork client 必须在 HTTPS 但
  ALPN ≠ h2 时拒绝, 不要降级 h1 偷偷继续. Claude 提 ALPN 失败但没具体到 "fork client
  reject 而不是 fall back".

- **G-CL-7 request body pump 风险**: Codex R/body pump — fork 把 request head /
  response future / send stream 分开 (`client.rs:422-438/527-550`), F-1 需要
  加 2-chunk POST + empty-body 测试. Claude 没拆这层细节.

---

## §4 合并执行方案

### 4.1 sub-phase 序列 (7 步, 沿用 Codex 顺序 + Claude 风险/CI 补丁)

| sub | scope 一句话 | nom | upper | 谁先发现 | 必读现状 |
|---|---|---|---|---|---|
| **F-1.a** | Evidence + fixture contract: 标"未捕真上游就不能 Released" | 0.3 day | 0.5 | CX | `anthropic_claude_code.json:113-127`, `http_profile.rs:58-86` |
| **F-1.b** | Adapter true-IO 抽取: `HttpTwoMimicryAdapter` 接 generic AsyncRead+AsyncWrite | 0.5 | 0.8 | CX (Claude 同形态但合并到 a) | `http2_adapter.rs:139-180`, `0x676e67/http2:src/client.rs:1312-1321` |
| **F-1.c** | L2 preflight module: typed status + runtime byte 对比 | 0.7 | 1 | CL+CX | `l1_preflight.rs` (复用), `http_client.rs:78-103` |
| **F-1.d** | ProxyEngine transport boundary: 抽 hyper-util 后, 行为保守 | 0.8 | 1.2 | CX (Claude 拆 c.1/c.2 同效果) | `proxy_engine/mod.rs:97-103/345-348`, `relay.rs:54-61/120-133` |
| **F-1.e** | HTTP/2 fork outbound client: 真 TCP/TLS, 复用 boring_tls_connector ALPN | 1.2 | 2 | CX | `boring_tls_connector.rs:163-178/249-258`, `0x676e67/http2:src/client.rs:1356-1576` |
| **F-1.f** | Builder + dispatch 接线: L2 preflight 在 build time 跑, Block* 错误映射 | 0.5 | 0.8 | CX (Claude 合到 F-1b) | `http_client.rs:78-103`, `dispatch.rs:151-180` |
| **F-1.g** | Profile backfill + byte tests + release evidence (含 G-CX-1 CI matrix + G-CX-2 perf) | 1.0 | 1.7 | CL+CX | F-2.5 status doc 同款风格 |
| **合计** | | **5.0 day** | **8.0 day** | | |

**Buffer 后总时间 5-8 codex-day**, 落 Owner OD 给的 5-8 day 区间.

### 4.2 关键 mutation discriminator (per sub-phase 节选)

- **F-1.a**: profile `available=true` 但 fixture 字段空 → 新一致性 test 红
- **F-1.b**: 删 `apply_settings` 中 `builder.settings_order(...)` 调用 → SETTINGS order
  assertion red (Codex 已查 fork API `0x676e67/http2:src/client.rs:1210-1215`)
- **F-1.c**: comparison 只 check id 不 check value → "wrong value test" red
- **F-1.d**: transport wrapper 整包 buffer response 不流 → 既有 idle timeout/streaming
  test 红 (`relay.rs:706-883`)
- **F-1.e**: fork client 不 spawn connection future task → 请求 hang → loopback test
  timeout (Codex 强调这个易错点)
- **F-1.f**: L2 preflight 从 builder 移走 → missing-H2 profile 返 Ok client →
  `expect_err("L2 missing H2 fields must fail closed")` red (与 F-2.3+ pattern 同)
- **F-1.g**: 改 profile.SETTINGS value 不改 fixture → byte equality test red

### 4.3 验收 (acceptance criteria for F-1 整体)

合并 Claude + Codex 的 acceptance + Claude G-CX-1 / G-CX-2 补:

1. `cargo test -p core_gateway --no-default-features` 全绿
2. `cargo test -p core_gateway --features mimicry-http2-fork --lib` 含新真 loopback 测试
3. `cargo test -p core_gateway --features mimicry-boring,mimicry-http2-fork --lib`
   含 proxy_engine_http2_fork / mimicry_http2_preflight / mimicry_http2_wire
4. **For 每个 F-1 Released profile**: runtime fork 出 byte exact match 真上游 CLI
   fixture (SETTINGS id+value+order, pseudo-header order). 不许 "not-equal-to-bad" 弱断言.
5. L2 preflight 在 combined-feature builder 接好: missing/wrong-value/wrong-order/
   wrong-pseudo-order 都 structured fail-closed
6. `build_mimicry_action` 映 L2 错误成 `Block*` 不 panic (F-2.3+ pattern)
7. Release evidence doc 标每 profile 的状态: Released / Feature Flag / Safe Equivalent
   / Mandatory Roadmap. 不悄悄丢功能 (Feature Preservation Rule)
8. **新增 G-CX-1**: `tools/feature-matrix/verify.sh` 加 `mimicry-boring,mimicry-http2-fork`
   组合, 全绿
9. **新增 G-CX-2**: hdrhistogram bench (复用 dev-dep): F-1.e + F-1.f 完后, p99
   relay latency 增加 ≤ 5% (vs 基线). 超过即回滚 D3 选项 A
10. CLAUDE.md #8 per-commit codex review: 每 sub-phase ≥1 轮闭环, P1 全清, P2 ≤2 轮
11. CLAUDE.md #14 mutation: 至少 F-1.b + F-1.c + F-1.f 各跑一次 live mutation check
    (像 F-2.3+ 那样实改+重跑)
12. CLAUDE.md #11 attestation: 每 commit body 含 attestation 行, 无 0x676e67/http2 抄码

### 4.4 risk register 合并 (8 项)

| # | risk | type | 缓解 | 来源 |
|---|---|---|---|---|
| R1 | Anthropic profile 无真 H2 数据 → F-1 不能 Released | evidence | F-1.a 强制 evidence contract; F-1.g capture before backfill | CX (Claude 漏) |
| R2 | http2 fork unstable API 漂移 | dep | rev pin (已), 编译+byte 双层测试, rev bump 需 Owner OK; 长期 vendor 化 (D4 B) | CL+CX |
| R3 | response body 类型 → relay 断流 / 终态分类 | tech | F-1.d 行为保守, F-1.e 加 fork-h2 测试; idle/timeout/streaming test 同 commit | CL+CX |
| R4 | L2 preflight pass synthetic 但 fail real upstream | compat | F-1.a 把 synthetic vs real 分类, Released 必须 real fixture | CX (Claude 漏) |
| R5 | per-request handshake 性能差 / 连接数 | perf | F-1 先 per-request (D7 A), pooling 留 follow-up; F-1.g acceptance 9 加 perf gate | CL+CX |
| R6 | ALPN 协商 h2 失败 → fork 错走 h1 | compat/security | F-1.e fork client reject h1 fallback, fail-closed (CX G-CL-6) | CX (Claude 漏 fail-closed) |
| R7 | request body pump (chunked / empty / trailer) 处理错 | tech | F-1.e 加 2-chunk POST + empty body 测试; trailers KnownGap (D9 A) | CX (Claude 漏) |
| R8 | feature-matrix CI 漏 mimicry-boring+mimicry-http2-fork 组合 → release 漏报 | CI | F-1.g 加 verify.sh 组合 (G-CX-1) | CL (Codex 漏) |
| R9 | HPACK parser 测试漏 order 错误 | test-quality | F-1.b 把 HPACK parser 加 negative fixture 或 move 到 helper + mutation test | CX (Claude 漏) |

### 4.5 OOS follow-ups (F-1 不承诺, 但跟踪)

合并 Claude + Codex out-of-scope:

- **OOS-A** vendor `0x676e67/http2` 进 `vendor/http2/` (D4 B 路径, ~0.5 day) — CL
- **OOS-B** H2 GREASE frame profile 字段 + adapter 支持 (~1 day) — CL
- **OOS-C** F-1-platform-h2-divergence (analog F-2.5 §7.1 codex 5th gap, 如果 Windows
  schannel 漏入 h2 路径) (~0.5 day) — CL
- **OOS-D** F-2.5-Gemini-h2 capture (gemini 操作触发 HTTP/2 的; F-2.5 §7.2
  model_api_ht_alpn 未触发) (~0.5 day) — CL
- **OOS-E** F-2.5-Kiro upstream capture (~1 day, 需 Kiro CLI 真账号) — CL+CX
- **OOS-F** H2 connection pooling (D7 B 升级) — CL+CX
- **OOS-G** Template revision across CLIs based on real capture — CX
- **OOS-H** First-request L2 preflight + periodic recert (D10 B 升级) — CX
- **OOS-I** bench harness — hdrhistogram p50/p95/p99 auto-bench + regression gate (~1 day) — CL

合计 follow-up ~5-6 codex-day, 不在 F-1 epic 内.

---

## §5 推荐下一步 (Owner 决策点)

**最小决策面**: 只需 Owner 拍 D1-D10 这 10 个选项. Claude 推荐已列在 §2.5 表;
Owner 可以 "全按推荐" 一次性放行, 或单点 override.

**默认推荐路径** (Claude 倾向, 等 Owner 拍):
1. D1=A (Codex order, evidence first)
2. D2=A (全串行)
3. D3=A (Box<dyn Body> 先, bench 红线再切 B)
4. D4=A (git-dep pin, vendor 留 OOS)
5. D5=A (mitmproxy 复用 F-2.5)
6. D6=C (按数据说话, F-1.g capture 后再定)
7. D7=A (per-request, pooling OOS)
8. D8=A (Released 只覆盖有 fixture 的 profile)
9. D9=A (no trailers, KnownGap)
10. D10=A (build-time preflight, match L1)

**执行入口**: Owner OK 后, 我开 F-1.a (evidence + fixture contract, 0.3-0.5 day).
F-1.a 完成且 Anthropic H2 capture 落地 (可能需要先跑 F-1.g 的 capture tooling
sub-step) 之后, F-1.b 起的真正代码工作启动.

**风险信号**: 如果 F-1.a 发现 Anthropic H2 capture 需要的 mitmproxy 重装 / Codex
真账号 / 长 setup 时间, 估时上半段就会消化掉 0.5-1 day buffer. 在 F-1.a 完成前
Owner 应明确是否接受这种前置代价.

---

## §6 Self-check (synthesis 完成前)

- ✅ 两份 draft 都通读 (Claude self / Codex 38KB 全文)
- ✅ Agreement 列项 (7 大共识)
- ✅ Conflicts 列 5 项 + winner 标注
- ✅ Gaps 双向交叉补 (Codex 漏 5 项, Claude 漏 7 项)
- ✅ 合并执行方案: 7 sub-phase, 5-8 day, 12 acceptance, 9 risks, 9 OOS
- ✅ Owner decision matrix 10 项 (D1-D10), 每项给 Claude 推荐
- ⚠️ 没看 codex 输出后再回头改 claude draft (CLAUDE.md #10 严守 — synthesis 才看)
- ⚠️ Anthropic H2 capture 是真实 blocker, Owner 必须意识到 F-1 不只是写代码, 还要补
  evidence (Codex G-CL-1 警告)
