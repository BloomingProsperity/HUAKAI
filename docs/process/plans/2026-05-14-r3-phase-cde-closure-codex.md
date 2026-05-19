# 2026-05-14 R-3 transport mimicry Phase C/D/E 闭环计划（Codex 独立版）

| 字段 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI R-3 transport mimicry — Phase C/D/E 闭环计划起草 (codex 独立版本)。” |
| 独立性 | 本文件按 Codex 判断独立起草；未读取当前同名 Claude 草案。 |
| 范围 | 只规划 Phase C 真指纹接入、Phase D 端到端验真、Phase E OCAW gate 与 release notes；不写代码。 |
| 不在范围 | 不新增 schema migration、不改 auth/billing/quota 核心、不接触真实 secret、不把 5 个 fallback mode 伪装成已验真。 |
| 成功标准 | Phase C/D/E 都有可执行范围、验收标准、风险、Codex lane 数、时间估、决策点；文件少于 400 行。 |
| Blast radius | 后续如果按计划执行，会触碰出站 TLS/HTTP identity、provider mode 策略、admin 启用门、release 文案。 |
| Clean-room | 本计划只读 HUAKAI 内部代码、模板和项目文档；未读非 MIT reference source。 |

## 0. 当前观察

- `backend/internal/transport/factory.go` 已按 8 个 mimicry mode 选择 `mimicry.NewRoundTripper(tmpl)`，registry 缺失或 stub 时当前会回落到 `PhaseADefaultTemplate()`。
- `backend/internal/transport/mimicry/utls_dialer.go` 已能把 `ClientHelloTemplate` 转成基础 `utls.ClientHelloSpec`，但仍是 Phase A 级别：扩展映射不完整，GREASE/随机扩展顺序/HTTP layer 还没有闭环。
- `template.go` 已含 `TLSBackend/GREASE/ExtensionOrder/HTTPLayer/AuthLayer` 字段，`registry.go` 能从 `tools/fingerprint-collector/templates/*.json` 装载。
- 按本次任务口径，Phase C 的“真模板”只认 3 个：`mimicry_chatgpt <- codex-cli.json`、`mimicry_kiro <- kiro-cli.json`、`mimicry_gemini_advanced <- gemini-advanced.json`。
- 仓库仍有 `anthropic-claude-code.json` 旧 Phase A 模板，但它缺少 `http_layer/tls_backend/grease`，不满足本轮“完整真指纹”口径；本计划把 ClaudeCode 归入 fallback，除非 Owner 明确要求升级旧模板。

## 1. Phase C — 真指纹应用到 mimicry dialer

### 范围

1. 把 `ClientHelloTemplate` 中的 TLS 字段真实落到 `utls.ClientHelloSpec`：
   - cipher suites：严格使用 `template.cipher_suites` 顺序；和 `ja3` 中 cipher 列表做一致性校验。
   - extensions：按 `template.extensions` 构造 uTLS extension 列表；padding extension 参与 wire，但 JA3 比对时按 JA3 规则排除。
   - supported groups：使用 `curves` / `supported_groups` 顺序；`key_share_groups` 单独控制 key share，不默认截取第一项。
   - signature algorithms：使用 `sig_algos` / `signature_algorithms` 顺序；缺失时 real template fail-loud。
   - supported versions / PSK modes / ALPN / EC point formats：全部从模板复制，不再用 Phase A 默认值覆盖。
2. 补齐 extension 映射策略：
   - 已知扩展用 uTLS 专用类型：SNI、status_request、supported_groups、ec_point_formats、sig_algos、ALPN、SCT、padding、EMS、session_ticket、supported_versions、PSK modes、key_share、ECH GREASE、renegotiation。
   - 模板里出现但当前未专门映射的扩展，先按“payload 是否可安全为空”分类；不能确认 payload 的 extension 不用 `GenericExtension` 假装成功，而是 fail-loud 并记录需要补映射。
3. GREASE 处理：
   - `grease=false`：不额外插入 `utls.UtlsGREASEExtension` 或 `GREASE_PLACEHOLDER`。
   - `grease=true`：仅当模板能表达 GREASE 位置或样本时启用 uTLS GREASE placeholder；单个 boolean 不足以还原 wire 形态，Phase C 需要补 `grease_positions` 或 `samples` 字段，或降级为“代表样本固定 spec”。
   - ECH GREASE extension `65037` 仍按模板显式扩展处理，不等同于普通 reserved GREASE 扩展。
4. 扩展顺序策略：
   - ChatGPT/Codex：`extension_order=stable`，运行时和单测都固定模板顺序。
   - Gemini Advanced：`extension_order=stable`，固定模板顺序；HTTP 层当前观察为 HTTP/1.1，即使 ALPN 含 `h2` 也不强制升级。
   - Kiro：`extension_order=randomized`，不能用单个 JA3 hash 作为唯一验收。Phase C 需要 deterministic test mode 复现代表样本顺序，runtime mode 再按采样策略随机化；Phase D 验收用 sample set 或 JA4 stable prefix，而不是要求每次都等于代表 JA3。
5. HTTP layer 应用：
   - 在 uTLS dialer 上方加 `httpLayerRoundTripper`，从模板设置 `User-Agent`、endpoint 兼容检查、必要固定 header、auth 机制兼容检查。
   - `http_layer.protocol=http1.1`：`ForceAttemptHTTP2=false`，TLS `NextProtos=["http/1.1"]`，必要时 `TLSNextProto` 置空以防自动 h2。
   - `http_layer.protocol=h2`：`ForceAttemptHTTP2=true`，TLS `NextProtos` 包含 `h2`；必须单测确认 uTLS conn 能让 stdlib net/http 进入 h2。
   - `h2_or_http1.1_reqwest_default`：先按模板 ALPN 和真实 pcap 决定默认；若无法稳定复现 h2 SETTINGS/header order，允许 Phase C 用 http1.1 safe path 进入 D 验真。
   - header order：Go `http.Header` map 不保证 wire 顺序。HTTP/1.1 真要验 header 顺序，需要自定义 ordered HTTP/1.1 writer；HTTP/2 真要验 HPACK 顺序/SETTINGS，可能需要 `x/net/http2` 或 fork，属于 Owner 决策点。
6. 3 真 mode + 5 fallback：
   - 真 mode：ChatGPT、GeminiAdvanced、Kiro 必须使用 registry 中对应 real template，不得回落 Phase A 默认模板。
   - fallback mode：ClaudeCode、Antigravity、Cursor、Copilot、Windsurf 保留 mode 枚举和配置入口，但生产启用必须 OCAW 拦截；运行时不能把 Anthropic Phase A default 当成这些 mode 的真实身份。
   - fallback 的最低安全行为：dev/test 可返回 placeholder-tagged transport；production 默认 fail-loud 或 standard safe fallback，需要 Owner 在 DP-02 选择。

### 涉及文件与 LoC 估算

| 文件 | 预计变化 | LoC |
| --- | --- | --- |
| `backend/internal/transport/mimicry/template.go` | 增强校验、JA3/template 一致性、GREASE/sample 字段解析 | +80~140 |
| `backend/internal/transport/mimicry/registry.go` | real/stub/fallback 状态、mode 映射与错误信息收敛 | +30~60 |
| `backend/internal/transport/mimicry/utls_dialer.go` | spec builder、扩展映射、protocol 选择、每连接 spec clone | +180~280 |
| `backend/internal/transport/mimicry/http_layer_roundtripper.go` | 新增 HTTP layer wrapper；如做 ordered HTTP/1.1 writer 则更多 | +160~260 |
| `backend/internal/transport/factory.go` | 区分 real template / placeholder / production gate 结果 | +50~90 |
| `backend/internal/transport/*_test.go` | 8 mode spec/header/protocol/fallback 单测 | +350~520 |
| `tools/fingerprint-collector/templates/SCHEMA.md` | 说明 GREASE/sample、randomized acceptance、stub 口径 | +30~70 |
| `tools/fingerprint-collector/templates/*.json` | 只补元数据和样本字段，不编造未抓到的值 | +0~80 |

### 单测策略

- `TestClientHelloTemplate_RealTemplateMatchesJA3Fields`：3 个真模板的 cipher/extensions/curves 与 JA3 input string 一致。
- `TestUTLSSpec_ForEachMode`：8 个 mode 都能给出明确状态：3 real 构造 spec，5 fallback 返回 placeholder/fail-loud 状态。
- `TestUTLSSpec_ExtensionOrderStable`：ChatGPT/Gemini exact order；padding 按 wire 保留、JA3 比对排除。
- `TestUTLSSpec_ExtensionOrderRandomizedKiro`：固定 seed 下等于代表样本；runtime 多次构造出现允许范围内的顺序变化。
- `TestUTLSSpec_GREASEPolicy`：`grease=false` 不插入额外 GREASE；`grease=true` 没有位置样本时不假装完整。
- `TestHTTPTransport_ProtocolSelection`：http1.1 禁 h2；h2 模式确认 ALPN 和 net/http 协商结果。
- `TestHTTPLayer_AppliesUserAgentAndHeaders`：UA、auth 机制、header 集合和 ordered writer 的输出顺序与模板一致。
- `TestFactory_NoPhaseADefaultForUnrelatedFallbackModes`：5 fallback 不得静默使用 Claude/Anthropic Phase A template。

### 成功标准

- 3 个真 mode 的 `ClientHelloSpec` 可从模板稳定构造；字段差异有明确 error，不 silent fallback。
- 5 个 fallback mode 在 dev/test/prod 的行为可预测、可审计，不伪装成已验真。
- HTTP layer 至少做到 UA、协议版本、header 集合一致；HTTP/1.1 header wire 顺序若纳入 Phase C，则必须有 raw request 单测。
- 所有新增测试可在 CI 无真实账号、无真实 upstream 的环境跑。

### 风险

- Kiro 随机扩展顺序与单个 JA3 hash 天然冲突，必须改验收口径为 sample set / stable prefix。
- `grease=true` 只有 boolean 时信息不足，可能导致“看起来启用 GREASE、实际不等价”的假阳性。
- Go stdlib 不保证 header wire 顺序；HTTP/2 SETTINGS/HPACK 顺序更难，可能需要高风险 fork 或新依赖。
- 当前 factory 的 Phase A fallback 会给 5 个 fallback mode 带来错误身份，需要改成显式 placeholder/fail-loud。

### Codex lane 数

3 条：Phase C 实施前测试合同审查、uncommitted code review、实现后 transport/clean-room 风险复审。

### 时间估

墙钟 2~3 天；工程时间 18~28 小时。若 Owner 要求 HTTP/2 SETTINGS/header order 精确复刻，另加 2~4 天并升级为高风险实现。

## 2. Phase D — 端到端验真

### 范围

1. CI 可跑部分：
   - local raw TLS listener 捕获 ClientHello，计算 JA3/JA4，与模板比对。
   - local HTTPS mock upstream 完成握手并回收请求，验证 UA、header 集合、HTTP 协议版本。
   - HTTP/1.1 ordered writer 若进入 Phase C，则在 mock upstream 读取 raw plaintext request，验证 header order。
   - 5 fallback mode 只验证 gate/fallback 行为，不要求 JA3/JA4 match。
2. Owner 本机部分：
   - Owner 使用自有账号和真实客户端模板，在本机运行 HUAKAI 出站请求。
   - 通过 tcpdump/pcap 或 collector 捕获 HUAKAI 出站 ClientHello，生成 redacted comparison artifact。
   - header 顺序如需真实 wire 证明，优先用本机受控 upstream 或 mitmproxy + 自签 CA；不要求把真实 token 或账号信息写入仓库。
3. mock upstream vs 真上游：
   - CI 默认 mock upstream，因为 CI 不能依赖真实账号、真实网络、真实 upstream ToS 状态。
   - 真上游只做 Owner 本机验真，不作为普通 CI gate；输出仅保存脱敏摘要。

### 自动化比对

- 输入：模板 JSON、捕获到的 ClientHello/JA3/JA4、HTTP request capture。
- 输出：`MATCH` / `MISMATCH` / `NOT_APPLICABLE_FALLBACK`，并列出 cipher、extension、curve、ALPN、UA、header order 的首个差异。
- ChatGPT/Gemini stable 模板：JA3 input string、JA4、UA、header order exact match。
- Kiro randomized 模板：JA3 hash 必须属于样本集；若样本集暂缺，则 Phase D 必须先补抓不少于 5 个样本后再 release。
- HTTP/2：若只验证 h2 negotiated，不验证 SETTINGS/HPACK 顺序，release notes 必须明确限制。

### 验收标准

- 3 真 mode：JA3/JA4 match；UA match；HTTP protocol match；header order match 或有明确“HTTP/2 order 未纳入本次 release gate”的限制说明。
- 5 fallback mode：不能通过 OCAW production enable；不能输出“已匹配真客户端”的状态。
- 捕获 artifact 不含 secret、真实账号 ID、cookie、OAuth token、IP/MAC 等敏感值。

### 风险

- 真实 upstream 抓包只能看到 TLS 层，HTTP header 被 TLS 加密；header order 需要受控 upstream 或 MITM。
- 真实账号/真实 upstream 验证属于 Owner 本机操作，不应进入 CI 或普通开发机。
- JA4 工具版本差异可能导致 hash 不一致，计划要固定 collector 版本或记录工具版本。

### Codex lane 数

2 条：验收测试矩阵审查、Owner 本机 artifact 的只读验真报告。

### 时间估

CI mock 验真 1~1.5 天；Owner 本机每个真 mode 30~60 分钟，三 mode 约 0.5 天；失败排查另计。

## 3. Phase E — OCAW gate + release notes

### OCAW 是什么

- 仓库现有定义是 One-Click Activation with Warning：生产环境灰区功能默认关闭，operator 在 admin UI 点一键启用，阅读警告、勾选责任确认，然后写 audit log / webhook。
- 本次 prompt 写了 One-Click-Authentication-Wait；若这是 Owner 新命名，应在 release notes/UI/API 中统一，但语义仍应是“生产启用前的显式等待/警告/确认/audit gate”。
- dev/test/CI 不被 OCAW 阻塞；OCAW 只卡 production enable。

### R-3 接入方式

1. Feature flag：
   - 全局：`HUAKAI_R3_TRANSPORT_MIMICRY_ENABLED=false` 默认关闭。
   - per-provider/per-mode：只有 mode 与 provider policy 匹配、模板 real、Phase D artifact 通过时，admin UI 才允许启用。
   - emergency kill switch：运行时关闭后新请求立即回 standard 或 fail-loud，具体由 DP-02 决定。
2. OCAW gate 条件：
   - operator 已确认 warning 文案。
   - 记录 `legal_review_id` 或等价合规确认编号。
   - 绑定 template hash、mode、provider、template collected_at、JA3/JA4、Phase D artifact ID。
   - fallback mode 默认不可 production enable，只显示“等待真实指纹采集”。
3. Audit：
   - 启用时写 admin audit；每次请求应用 R-3 时写轻量 transport policy audit。
   - audit 不记录 token/cookie/账号明文；只记录 template hash、mode、provider、decision reason。

### Release notes 内容

- R-3 是 default-off advanced transport identity feature，不是默认网关路径。
- 本 release 只声明 3 个真 mode 的验真状态：ChatGPT/Codex CLI、Kiro、Gemini Advanced。
- ClaudeCode/Antigravity/Cursor/Copilot/Windsurf 是 fallback/placeholder，不得被描述为已验真。
- 列出已知限制：Kiro randomized JA3 验收口径、HTTP/2 header order/SETTINGS 是否覆盖、真实 upstream header 捕获限制。
- 列出 operator 责任：只能使用自己有权使用的账号与模板；启用前阅读上游 ToS；HUAKAI 不保证任何 upstream 允许该部署形态。
- 列出回滚方式：关闭 feature flag、禁用 per-mode profile、删除或停用 template。

### Staged rollout

1. Stage 0：代码合并但全局 flag off；只跑单测和 mock E2E。
2. Stage 1：本机 Owner 验真 3 真 mode；生成 redacted artifacts。
3. Stage 2：admin UI 显示 OCAW，但 production 默认 off；只允许 3 真 mode。
4. Stage 3：单租户/单 provider canary，观测 handshake mismatch、upstream 4xx/429、账号健康变化。
5. Stage 4：扩大到更多 operator；5 fallback mode 仍保持 disabled，直到真模板闭环。

### 成功标准

- OCAW 前置条件、feature flag、release notes 三者口径一致。
- 生产环境无法误启用 placeholder mode。
- release notes 没有把未验真的 fallback mode 包装成已完成。
- 有明确回滚路径和 operator 可见 audit。

### 风险

- OCAW 是合规叙事和 operator UX，不是法律豁免；release notes 不能暗示“启用后一定安全”。
- 如果仓库继续提交真实 vendor templates，README 里“ships with no fingerprint templates”的说法需要同步修订或解释为“production 不默认启用内置模板”。
- Admin/API gate 可能涉及 schema 或 auth core；本计划不直接改，高风险部分需另走 Owner 确认。

### Codex lane 数

2 条：release note / risk wording review、release gate final review。

### 时间估

文档和 release notes 0.5~1 天；admin/API gate 若只接现有 mock UI 1~2 天；若新增生产 audit schema，需单独计划并 Owner 确认。

## 4. 决策点（12 个）

1. DP-01：`anthropic-claude-code.json` 旧 Phase A 模板是升级为 ClaudeCode 真模板，还是按本轮口径降级为 fallback？
2. DP-02：fallback mode production 行为选 fail-loud、standard safe fallback，还是允许 placeholder transport？
3. DP-03：Kiro randomized 扩展顺序的验收，是 sample-set match 还是要求固定代表 JA3？
4. DP-04：GREASE schema 是否补 `grease_positions` / `ja3_hash_samples` / raw extension samples？
5. DP-05：HTTP/2 SETTINGS/HPACK/header order 是否属于 Phase C 必做；若必做，是否允许新依赖或 fork？
6. DP-06：HTTP/1.1 header wire order 是否允许新增 custom ordered RoundTripper？
7. DP-07：Owner 本机真上游验真使用哪些账号/provider，是否允许真实请求打到 upstream？
8. DP-08：header order 证明使用受控 mock upstream、mitmproxy，还是只验 adapter-level order？
9. DP-09：Phase D redacted artifacts 存放在仓库、私有目录，还是只存摘要？
10. DP-10：OCAW 正式展开名沿用 Activation with Warning，还是改为 Authentication-Wait？
11. DP-11：release 是否允许内置 3 个真模板，还是要求 operator 自采模板后才能 production enable？
12. DP-12：Phase C/D/E closure 是只以 3 真 mode 通过为准，还是必须等 5 fallback mode 也抓到真指纹？

## 5. Pre-execution checklist

1. Owner/PM 综合 Claude 与 Codex 独立计划，生成无后缀 synthesized plan。
2. 对 DP-01~DP-12 做选择或明确 defer。
3. 更新 SCHEMA 中 gemini stub 过期描述，避免测试与文档冲突。
4. 定义 real/stub/fallback 状态枚举和 release 用语。
5. 先写 Phase C 单测合同，再实现 TLS spec builder 和 HTTP layer。
6. Phase C 实现后 staged change，跑 `go test ./internal/transport/...` 和相关 gateway dispatcher tests。
7. 按项目规则跑 `codex exec review --uncommitted --full-auto`。
8. Phase D Owner 本机验真完成后，再进入 OCAW release note。

## Owner 中文摘要

本 Codex 独立计划建议把 R-3 闭环分成三步：Phase C 先把 3 个真模板真实接入 uTLS/HTTP layer，并把 5 个 fallback mode 做成显式 placeholder/fail-loud 而不是复用 Phase A 默认；Phase D 用 CI mock capture + Owner 本机抓包双轨验 JA3/JA4、UA、协议和 header 顺序；Phase E 用 OCAW、feature flag、release notes 和 staged rollout 防止生产误启用灰区能力。没有功能缩水：5 个暂无真指纹的 mode 没删除，只是不准伪装成已验真；clean-room 风险低，因为本计划未读非 MIT 源码；安全/合规风险集中在 GREASE 信息不足、Kiro 随机 JA3、HTTP/2 header order 难复刻、OCAW 不是法律豁免。需要 Owner/PM 后续确认 12 个决策点，尤其是 fallback 行为、ClaudeCode 旧模板口径、HTTP/2 精确复刻范围和 release 是否允许内置真模板。
