# 2026-05-08 U6-D 双 lane synthesis (sonnet + codex)

## 双 lane 输入

- sonnet: `docs/process/plans/2026-05-08-upgrade6-u6d-design-sonnet.md` (608 词)
- codex: `docs/process/plans/2026-05-08-upgrade6-u6d-design-codex.md` (340 行)

## 一致点

1. ✅ identity **不**覆盖 `registry.Resolved.ProtocolFamily`（仅影响 ClientAdapter 选择）
2. ✅ `proto.ClientAdapter` 与 `proto.UpstreamAdapter` 已分离，是正确的扩展点
3. ✅ Cursor + Claude model 不应 fail，应 lossy translate（OpenAI client × Anthropic upstream）
4. ✅ FieldMatrix key **不**加 identity 维度
5. ✅ feature flag canary 推出 (执行边界 C 配 OCAW)
6. ✅ acceptance 测试要覆盖 cursor↔anthropic / claude_code↔openai / unknown→path-based 三主轴

## 关键差异 + synthesis 决策

| 维度 | sonnet | codex | 综合采纳 | 理由 |
|---|---|---|---|---|
| 优先级 | identity confidence ≥ 0.7 优先 | **path/route 优先**, identity 仅填空白 | **codex** | header spoof 风险高于 confidence 信号 |
| 失败语义 | lossy 默认, 不 502 | UNSUPPORTED → 4xx 流前 fail loud | **codex** | 流前 fail loud 更可调试; LOSSY 仍可继续 |
| Cody source 可读 | 可（Apache-2.0）| 不读, 用 OCAW black-box | **codex** | clean-room 严守; 即使 license 允许，behavior evidence 更干净 |
| ProtocolFamilyTraits | 没提 | 必需（19 family → coarse Protocol）| **codex** | 当前 capability_matrix / field_matrix 用 coarse Protocol，需显式映射避免 substring 匹配 |
| confidence threshold | fixed 0.7 configurable | policy + path-consistency 检查 | **codex** | 单一 threshold 不足；要 multi-signal |
| 假 finish_reason 标 lossy | 作为可选 | 拒绝 (false client-visible 语义) | **codex** | 错误把 audit 信号塞 wire format 字段 |
| 是否 read 客户端 source | sonnet 提 Cody | codex 全拒 | **codex** | 一致 |
| 原子拆分粒度 | 6 atomic | **8 atomic** (含 ProtocolFamilyTraits + Capability Preflight 独立) | **codex** | 颗粒度更细更安全 |

## 综合后的 U6-D 实施序列

| atomic | 依赖 | 内容 | LoC 估 |
|---|---|---|---|
| **U6-D-0** | — | synthesis gate (本文件) + Owner decision points | 0 |
| **U6-D-1** | U6-D-0 | clean-room client shape evidence (research artifact, OCAW 计划) | 0 (docs) |
| **U6-D-2** | U6-D-1 | `ClientShapeDecision` 决策契约 + selector 实现（含 path-first precedence） | <120 |
| **U6-D-3** | U6-D-2 | `ClientAdapterRegistry` keyed by `proto.ClientProtocol` (独立 registry, 不 mutate ProtocolAdapterRegistry) | <150 |
| **U6-D-4** | U6-D-3 | `ProtocolFamilyTraits` 19 family → coarse Protocol 显式映射 | <80 |
| **U6-D-5** | U6-D-4 | Capability Preflight: 调 CapabilityMatrix 验证 (Client × Upstream, feature)，UNSUPPORTED→4xx, LOSSY+ProtocolLossEntry | <100 |
| **U6-D-6** | U6-D-5 | forwarder/handler 集成（feature flag default off） | <120 |
| **U6-D-7** | U6-D-6 + U7-E | `ClientWirePolicy` strict/tolerant/audit_only 钩子 (FieldMatrix schema 不变) | <80 |
| **U6-D-8** | U6-D-6 | acceptance tests + 低基数 metrics | tests |

每 atomic 独立 commit + 独立 dual-debug-renew (codex + sonnet)。

## Owner decision points （surface）

合并 sonnet + codex 共 10 项，去重精简 5 项问 Owner:

1. **D1 (codex)**: 是否允许 operator-mode 让高 confidence identity 覆盖 path？
   - **推荐**: 默认 NO；只在显式标记的 identity-aware 路由 (admin 配)允许
2. **D2 (sonnet+codex)**: feature flag (`HUAKAI_IDENTITY_AWARE_CLIENT_SHAPE`) 默认 off，OCAW 评估后切？
   - **推荐**: YES，default off
3. **D3 (codex)**: LOSSY translation 是否需 tenant/operator 显式 opt-in？
   - **推荐**: NO，audit visibility 足够（与 capability_matrix 默认行为一致）
4. **D4 (codex)**: U6-D 是否 block on OCAW？
   - **推荐**: 不 block 实施；OCAW 是 production enable 前置（feature flag 之后）
5. **D5 (codex)**: client-wire pruning 记录在 ProtocolLossEntry / usage draft audit / both？
   - **推荐**: both — `ProtocolLossEntry` 给 operator, audit metadata 给 billing/计费

## 与 U1-A 的关系

U1-A pre-review (codex) 推荐 **B 选项**: U1-A 仅做 BindingCache interface + noop stub，schema 留 0013 至 U1-B/U1-C 启用前。**U6-D 与 U1-A 互不依赖**，可并行推进。

## 接下来

我会按此 synthesis 进 U6-D-1 (clean-room evidence artifact)。U6-D-2~D-8 依次推。Owner 若选 A path（D1=YES）我再调整 selector 实现。

Lane: claude (synthesis)
Time: 2026-05-08T<UTC>
