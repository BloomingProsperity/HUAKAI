# 2026-05-27 Trust Chain Simplification — Codex Evaluation

| Field | Value |
|---|---|
| Owner directive | "我觉得检测有没有掺假很简单, 不用搞什么复杂的信任链。直接在用户面板每条 API 返回内容带上最上游的提供商就行了。" |
| Assumption | HUAKAI SaaS edition: 商家中转请求, 用户在面板看每条消费记录, 不是单租户自用网关。 |
| Lane | specifier |
| Observed regions | 18 |
| Inferences | 9 |
| Open questions | 3 |

## §1 现状摘要 (F-TRUST-001 现在做了什么)

F-TRUST-001 当前不是普通"显示上游 provider"功能, 而是 Phase 6 commercial foundation 的 **per-request user-verifiable ledger**。矩阵行要求每个 request 写一条 user-verifiable ledger entry, 包含 hop chain、model verdict、Merkle 前后 root、ed25519 signature, 并提供 entry / detached verify / pubkey registry / chain head + proof 四类用户验证端点; 同行还列出 90 天公钥轮换、append-only enforcement、tenant scope 防枚举和隐私字段误入等风险 [docs/03_FEATURE_PARITY_MATRIX.md:119](../../03_FEATURE_PARITY_MATRIX.md:119)。

spec 的问题陈述更明确: HUAKAI 要解决的是 operator 单方信任模型, 用户无需只相信商家没有换模型、掺水或用便宜模型冒充贵模型; F-TRUST-001 覆盖"链路公开 + 模型校验 + cryptographic anti-tampering" [docs/specs/trust-chain-user-verifiable-ledger.md:16](../../specs/trust-chain-user-verifiable-ledger.md:16), [docs/specs/trust-chain-user-verifiable-ledger.md:26](../../specs/trust-chain-user-verifiable-ledger.md:26)。

现有设计的核心控制面是: ledger row 里把 hop chain 和 model chain 作为用户可见安全元数据, 再把前一 root、当前 root、公钥指纹和签名绑定到 canonical entry hash [docs/specs/trust-chain-user-verifiable-ledger.md:30](../../specs/trust-chain-user-verifiable-ledger.md:30), [docs/specs/trust-chain-user-verifiable-ledger.md:38](../../specs/trust-chain-user-verifiable-ledger.md:38), [docs/specs/trust-chain-user-verifiable-ledger.md:40](../../specs/trust-chain-user-verifiable-ledger.md:40), [docs/specs/trust-chain-user-verifiable-ledger.md:43](../../specs/trust-chain-user-verifiable-ledger.md:43)。model validation 不是只显示 provider, 而是把 requested、route-decided、upstream-reported 三段和 verdict 放在一起, 包括 mismatch / unknown 路径 [docs/specs/trust-chain-user-verifiable-ledger.md:97](../../specs/trust-chain-user-verifiable-ledger.md:97), [docs/specs/trust-chain-user-verifiable-ledger.md:111](../../specs/trust-chain-user-verifiable-ledger.md:111)。

用户验证面目前定义为四类 endpoint: 查 entry、detached verification、公钥注册、chain-head 与 proof [docs/specs/trust-chain-user-verifiable-ledger.md:117](../../specs/trust-chain-user-verifiable-ledger.md:117), [docs/specs/trust-chain-user-verifiable-ledger.md:123](../../specs/trust-chain-user-verifiable-ledger.md:123), [docs/specs/trust-chain-user-verifiable-ledger.md:128](../../specs/trust-chain-user-verifiable-ledger.md:128), [docs/specs/trust-chain-user-verifiable-ledger.md:132](../../specs/trust-chain-user-verifiable-ledger.md:132)。公钥轮换计划是 90 天, 老 entry 永久按当时 key fingerprint 验证, 私钥进 KMS / KeyProvider [docs/specs/trust-chain-user-verifiable-ledger.md:176](../../specs/trust-chain-user-verifiable-ledger.md:176), [docs/specs/trust-chain-user-verifiable-ledger.md:178](../../specs/trust-chain-user-verifiable-ledger.md:178), [docs/specs/trust-chain-user-verifiable-ledger.md:180](../../specs/trust-chain-user-verifiable-ledger.md:180)。

结论: 当前 F-TRUST-001 的安全目标是"用户可验证 + 商家事后不能无痕改", 不是"面板显示一个 provider 字段"。Owner 方案可以作为 UX 层的可读字段, 但单独替代 F-TRUST-001 会把商家不可造假的目标缩水为商家自报。

## §2 Owner 提议解读 (response 带 upstream provider 字段)

我把 Owner A 解读为: 每次 API 响应和消费面板的每条记录都新增最上游 provider 的可读字段, 例如用户看到"本次走了 OpenAI / Anthropic / Vertex / OpenRouter / custom provider"。在 SaaS edition 下, 这个字段由 HUAKAI 商家侧中转层生成或转写, 用户不直接和上游 provider 建立信任关系。

这个方案的强项是极简 UX: 用户无需理解 Merkle proof、公钥指纹、detached verify, 面板可以零门槛展示"走了谁"。它也非常适合排查普通运营问题: provider 缺货、路由 fallback、模型 alias、成本异常时, 用户和客服能快速定位。

但它不是 anti-fake proof。只要商家控制网关、数据库和面板, 商家就可以把 provider 字段写成任意值, 也可以只对用户面板写一个漂亮字段而不保留可验证证据。这个字段能降低"黑盒感", 不能证明"商家没有造假"。按 Feature Preservation Rule, 它只能成为 F-TRUST-001 的 `UX Hint` 或 `Safe Equivalent` 的一部分, 不能把 F-TRUST-001 从 Mandatory Roadmap 静默降级。

## §3 借鉴项目对照 (≥3 项目, 每项目带 source cite file:line)

| Project | Observed exposure shape | HUAKAI-fit reading |
|---|---|---|
| Portkey | Observed: 请求侧要求 provider 或 config, response service 会把 retry/index/trace/cache 等 header 附加到响应; 当上下文里有非自身 provider 时, 也把 provider 作为响应 header 暴露。provider 值来自 provider option, 即路由/配置上下文, 不是上游 cryptographic receipt。证据: `Portkey@351692fd9236af222168134b416924fae0bdba23:src/middlewares/requestValidator/index.ts:111`, `Portkey@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/requestContext.ts:144`, `Portkey@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/responseService.ts:99`, `Portkey@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/responseService.ts:122`. | Good UX precedent for Owner A: response-visible provider is useful and cheap. But it is a gateway assertion. No observed signature, append-only chain, or third-party verification in the read region. |
| Helicone | Observed: AI Gateway request can derive provider from target URL pattern; gateway responses set gateway-mode, provider, and model headers for the selected attempt. The web request table and drawer render provider from request metadata. 证据: `Helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/gatewayRouter.ts:134`, `Helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/gatewayRouter.ts:153`, `Helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:151`, `Helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/initialColumns.tsx:91`, `Helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/RequestDrawer.tsx:242`. | Strong precedent for combining API response header and operator/user panel. Still observability/tracing, not user-verifiable anti-tamper by itself. |
| LLMGateway | Observed: model catalog response includes a providers array with provider ID and provider-specific model name; chat response transformation builds metadata with requested/used provider and model, sets the response model to a provider-qualified value, and returns transformed JSON. 证据: `LLMGateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:28`, `LLMGateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:199`, `LLMGateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:167`, `LLMGateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:512`, `LLMGateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/chat.ts:10138`. | Best precedent for Owner A's "response body visible metadata" direction. It improves user debuggability, but the metadata is still produced by the gateway. |
| LiteLLM | Observed: router response metadata can add a model-group header and quality-router decision headers; provider is used in model info / route metadata, but in the read response-header region I observed route/model-group exposure rather than a direct upstream-provider proof. 证据: `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8643`, `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8668`, `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8951`, `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8970`, `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8994`. | Useful caution: mature gateways often expose routing/debug metadata, but that is not equivalent to "merchant cannot fake". |

one-api / all-api-hub: skipped per task instruction.

## §4 攻击面分析 (商家伪造 / 网络中间人 / 用户验证零成本 / 用户离线验证)

**商家直接改 response body 加字段。** Owner A 把 provider 放在 response body 或面板记录里, 但字段生成者仍是商家控制的网关。商家如果恶意, 可以在返回给用户前写入任意 provider/model; 用户只看到一个漂亮字段, 无法知道字段是否来自真实上游响应、路由决策、缓存重放、人工补写或数据库修正。A 只能防"不小心黑盒", 防不了"商家蓄意造假"。

**商家不发 upstream provider 字段。** 如果缺字段仍被当成正常成功, 用户会把"没有证据"误解为"没问题"。HUAKAI 若采纳 A, 必须把缺失字段显示为 `unknown/unverified`, 并在消费面板、导出、客服详情里保留红/灰状态。缺字段不能 fallback 成空字符串、默认 provider 或按请求 model 推断 provider。

**商家造假 model 名字 + provider 字段。** 用户能看到"声明走了谁", 但不能知道实际上是否走了同一 provider、同一模型、同一收费 tier、同一 token 计量。签名 lite B 能证明"这条声明签过且事后没被改"; Merkle C 能进一步证明"这条声明在连续账本中没有被无痕删除/重排"; 但只有上游自身可验证 receipt 或用户直连 SDK D 才能证明"真实上游也承认这次调用"。在多数商业 LLM API 没有给下游用户签发 provider receipt 的前提下, C 的价值是把商家从"可随意改"约束到"造假必须留下系统性可审计痕迹"。

**网络中间人。** 普通外部 MITM 在 TLS 正常时难以改 SaaS 响应; 但 HUAKAI 的核心威胁不是公网 MITM, 而是商家/运营方本身。若用户公司代理、浏览器插件、恶意客户端或导出链路改了内容, A 也无法区分。B/C 通过用户本地验签或离线 proof 可以发现响应/导出被改, 前提是公钥分发不能同源完全自证。

**用户验证零成本。** A 的零成本体验最好: 面板直接显示 provider。B 的零成本体验也可以接近 A: 默认面板显示 provider + green/yellow/red 签名状态, 只有 dispute 或导出时才让用户点开 verify。C 的 UX 需要隐藏复杂度, 把 Merkle 证明降级成"账本连续性: 已验证 / 待验证 / 断裂"。

**用户离线验证。** A 没有离线验证。B 可以给用户导出 signed receipt, 只靠公钥离线验签。C 可以导出 receipt + inclusion/consistency proof, 离线验证单条存在性和链连续性。D 的离线验证依赖用户自己的 SDK/provider logs, 但这会削弱 SaaS 商家中转模式。

## §5 三大维度评估 (架构 / 算法 / 生态)

### 架构升级

Owner A 是观测字段, 不是信任架构。它应放在 response metadata、billing receipt、admin/user panel、export CSV/JSON 里, 作为所有方案的 UX 基础。它能让 HUAKAI 比黑盒 gateway 更透明, 也比只给 admin 看日志更友好。

签名 lite B 是最小可信架构: 每条 response/receipt 的 provider、model verdict、request id、tenant-scoped ref、cost ref、timestamp 形成 canonical envelope, 网关用 KMS-backed key 签名, 用户用公开 key 验证。B 不证明商家当时没撒谎, 但能证明商家事后不能悄悄改账、导出不能被客服或中间链路改, 也能把 missing/tampered 做成强 UI 状态。

Merkle C 是当前 F-TRUST-001 完整架构: B 解决单条记录完整性, C 解决删除、重排、选择性披露、批量改账。对 SaaS 商业版最关键的是 dispute / audit / 商家背书: 用户不需要每条都懂 Merkle, 但第三方工具、商家审计和 Owner release gate 需要它。

直连 SDK D 是另一种信任架构: 用户直接调上游或带自己的 provider key, 商家无法替换上游。但它和 HUAKAI SaaS edition 的"商家中转 + 统一消费面板 + provider pool"天然冲突, 适合作为高信任 enterprise mode, 不适合作为主线替代。

### 算法升级

A 无签名算法, 只有字段生成和展示规则。它的安全边界等于商家自报。

B 推荐 ed25519: 验证快、签名短、实现成熟, 适合 per-request receipt。验证延迟可以做到用户侧毫秒级; 服务端签名如果走 KMS, 主要延迟来自 KMS round-trip 和批量策略。公钥分发至少需要 well-known endpoint + key fingerprint + 本地缓存; 更强版本可把公钥 digest 放到安装包、SDK 或第三方 mirror。

C 在 B 上增加 hash chaining / Merkle inclusion proof / chain-head checkpoint。算法延迟不应在用户热路径全量验证; 写入时计算当前 entry hash 与 root, 用户按需 verify。关键不是 hash 成本, 而是 writer 串行性、DLQ 恢复、分区策略和 chain-head 发布纪律。

D 依赖 provider SDK/TLS/OAuth/API key 身份, 本身不解决 HUAKAI billing receipt 的不可改问题。若用户直连后仍要 HUAKAI 出账, 仍需 B 或 C 来签消费记录。

### 生态升级

用户面板 UX: A 是必做, 但必须带状态。推荐显示 provider/model 时同时显示 `verified / signed-only / unverified / missing / mismatch`。不要只显示 provider 名字, 否则用户会把"商家声明"误解成"已验证"。

商家 audit: B 可以支持单条 receipt 争议处理, C 支持批量审计和第三方抽样。若 Owner 的卖点仍是"商家不能做假", 商家 audit 不能只靠面板字段。

第三方验证工具: A 无工具价值。B 可以做一个极小 CLI/web verifier。C 可以让第三方下载 chain-head、proof、receipt 做抽样/全量验证。D 的工具生态走 provider SDK, 不是 HUAKAI 账本生态。

## §6 4 选项对比 (Owner A / 签名 lite B / Merkle 完整 C / 直连 SDK D)

| Option | Shape | 工时估计 | 安全性 | UX | SaaS fit | Main gap |
|---|---|---:|---:|---:|---:|---|
| A. Owner response/provider field only | API response + user panel + export 显示 upstream provider/model | 1-2 天 / 8-16h | 2/10 | 9/10 | 9/10 | 商家可伪造、可缺失、可事后改; 只能证明"商家显示了什么"。 |
| B. 签名 lite | A + per-request signed receipt + public key + detached verify; no Merkle | 3-5 天 / 24-40h | 6/10 | 8/10 | 8/10 | 防事后篡改, 但不能证明商家签名时没有撒谎; 删除/选择性披露仍弱。 |
| C. Merkle 完整 | 当前 F-TRUST-001: signed entry + append-only chain + proof + key rotation + verification endpoints | 10-15 天 / 80-120h | 9/10 | 6/10 raw, 8/10 with panel abstraction | 8/10 | 工程复杂度最高; 需要 writer、DLQ、chain partition、pubkey ops 成熟。 |
| D. 直连 SDK | 用户直接调 provider 或自带 provider key, HUAKAI 只做辅助面板/计费 | 5-8 天 / 40-64h for constrained mode | 8/10 provider identity, 4/10 HUAKAI billing trust | 4/10 | 3/10 | 破坏商家中转/provider pool 商业模型; 仍需 B/C 签 HUAKAI 账单。 |

评分解释:

- A 的 UX 最高, 但安全分低, 因为它没有独立验证者。
- B 是"比 sub2api 简洁但更安全"的最小候选: 用户仍只看面板, 但高级用户/客服/第三方可以验签。
- C 才匹配当前 F-TRUST-001 的"商家不能做假"强卖点。
- D 对"用户信任上游身份"强, 对 HUAKAI SaaS 商业闭环弱。

## §7 Codex 推荐 + 理由

Codex 推荐: **不要用 Owner A 单独替换 F-TRUST-001。把 A 合入 F-TRUST-001 作为第一层 UX, 同时把 B 作为可先交付的 TRUST-lite, 保留 C 为 Mandatory Roadmap / 商业卖点完整形态。**

推荐落地顺序:

1. 先做 A 的面板与响应字段, 但字段名称旁必须显示验证状态。缺字段、空字段、provider/model 不一致都显示 unverified/mismatch, 不可默默成功。
2. 同一小切片做 B: 对 provider/model/request/cost/redacted metadata 签名, 提供 public key 和 detached verify。这样 Owner 想要的简洁体验保留, 但不会把"反掺假"降级成纯自报。
3. C 不必阻塞最早 UX, 但不能从 F-TRUST-001 删除。等 B 稳定后补 Merkle chain-head/proof, 用于商家 audit、第三方验证、争议处理和 release gate。
4. D 只作为 enterprise/direct-key mode 或 Mandatory Roadmap 的旁路能力, 不作为 SaaS edition 主线。

理由:

- Reference projects 证明 provider/model 暴露是成熟且有用的 UX 模式, Portkey/Helicone/LLMGateway 都有类似 response-visible 或 panel-visible 元数据。
- Reference projects 没有在已读区域证明"provider 字段 = 防商家造假"。这些实现更接近 observability/troubleshooting。
- HUAKAI 的差异化不是"我们也显示 provider", 而是"用户不必相信商家自报"。A+B 是最小可辩护版本; C 是完整版本。
- `[[feedback_huakai_better_than_sub2api]]` 的合理解释不是做更复杂的仪式, 而是让默认体验比 sub2api 简洁, 同时在争议和审计路径上有可验证证据。A alone 简洁但不安全; C alone 安全但首期重; A+B staged 最平衡。

Open questions:

1. Owner 是否接受 TRUST-lite B 作为先交付状态, 并把 Merkle C 保留为后续 release gate?
2. 公钥分发是否只用 HUAKAI well-known endpoint, 还是需要第三方 mirror / SDK pinning?
3. 对"商家签名时就撒谎"的威胁, HUAKAI 是否要进一步探索上游 request-id cross-check、direct-key mode 或 provider-side receipt?

## Source Coverage Proof

Source files read:

- HUAKAI: `docs/03_FEATURE_PARITY_MATRIX.md` lines 105-135, especially F-TRUST-001 line 119.
- HUAKAI: `docs/specs/trust-chain-user-verifiable-ledger.md` lines 1-270.
- HUAKAI: `git show --stat 158c421 -- docs/specs/trust-chain-user-verifiable-ledger.md`.
- LiteLLM: `litellm/router.py` lines 8636-8682 and 8948-9020.
- LiteLLM: `litellm/types/utils.py` lines 2664-2690.
- LiteLLM: `tests/test_litellm/router_strategy/test_quality_router.py` lines 825-890.
- Portkey: `src/globals.ts` lines 1-55.
- Portkey: `src/handlers/services/responseService.ts` lines 90-132.
- Portkey: `src/handlers/services/requestContext.ts` lines 60-110 and 136-152.
- Portkey: `src/middlewares/requestValidator/index.ts` lines 108-180.
- Helicone: `worker/src/routers/gatewayRouter.ts` lines 128-166.
- Helicone: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts` lines 145-172.
- Helicone: `web/components/templates/requests/initialColumns.tsx` lines 86-102.
- Helicone: `web/components/templates/requests/RequestDrawer.tsx` lines 236-250.
- LLMGateway: `apps/gateway/src/models/models.ts` lines 24-36 and 184-210.
- LLMGateway: `apps/gateway/src/chat/tools/transform-response-to-openai.ts` lines 148-188, 500-518, and 1118-1132.
- LLMGateway: `apps/gateway/src/chat/chat.ts` lines 10060-10142.

Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-27T09:53:21Z

中文摘要: 本文真实观察到的部分是 HUAKAI F-TRUST-001 当前 spec、以及 LiteLLM/Portkey/Helicone/LLMGateway 对 provider/model/routing metadata 的响应或面板暴露方式; 合理推断部分是这些暴露方式在 HUAKAI SaaS edition 下只能构成 UX transparency, 不能单独证明商家没有造假; open question 共 3 个, 主要围绕 TRUST-lite 是否可作为先交付版本、公钥分发强度、以及是否需要上游 request-id/direct-key 进一步约束"签名时就撒谎"的问题。
