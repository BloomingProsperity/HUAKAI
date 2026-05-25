# 6 Placeholder Session Adapter — Synthesis (Claude × Codex)

- UTC: 2026-05-24T07:45Z
- 输入:
  - Claude: [2026-05-24-placeholder-session-adapters-claude.md](2026-05-24-placeholder-session-adapters-claude.md) (27K,6 个 D 决策)
  - Codex: [2026-05-24-placeholder-session-adapters-codex.md](2026-05-24-placeholder-session-adapters-codex.md) (66K,810 行,8 个 D 决策)
- Ref anchor: [../2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md)

## §A 共识区(直接落地)

| 主题 | Claude | Codex | 一致 |
|---|---|---|---|
| Copilot OAuth | D-1 paraphrase litellm device-code 模板 | D-3 实 service-token refresh adapter,引用 litellm authenticator.py:83-193 | **复用 litellm 模式**,paraphrase |
| Cursor/Kiro/Windsurf 无 endpoint 证据 | D-3 研究切片(P-D/E/F),Owner 抓包后才动 | D-2 Safe Equivalent — endpoint 必须 owner-verified 才进 hardcode default | **不 hardcode 假 endpoint**,owner 抓包/手填后才生效 |
| Cursor/Kiro/Windsurf refresh 策略 | (Claude 未具体列;只说研究切片) | D-6 manual-first refresh outcome | **manual-first 兜底**,不假装自动 refresh |
| Vendor Refresher 注入 | D-4 单 map | (Codex 未单列,假设 map) | **单 map** 注入到 Scheduler |
| 新 runtime dependency | (Claude 倾向不加) | D-7 不加任何新依赖 | **共识:不引新依赖** |
| Schema migration | (Claude D-3 提了 0008/0009/0010 三个 migration) | D-8 不动 schema,用 JSON metadata | **冲突** → 见 §B-2 |
| Frozen package | gatewayhttp/gateway/proto 不动 | 同 | 共识 |

## §B 冲突区(必 Owner 拍板)

### B-1 Registry default 启用策略 ⭐核心冲突⭐

- **Claude D-2 推荐**:**完工逐个上**(D 选项)— copilot/gemini_advanced/antigravity 完工后翻 true,cursor/kiro/windsurf 维持 false 等到 P3/P4 抓包完工。
- **Codex D-1 推荐**:**全注册 default-on + 内部 fail-closed** — 所有 6 vendor 默认 on,但 adapter 内若 endpoint/auth provenance 缺失就拒发请求(fail-closed)。
- **赌注**:
  - Claude 立场:env gate 控住,生产用户看不到没完工的 vendor → 不会误用
  - Codex 立场:统一注册 → 用户能看到 vendor 在产品里,只是请求当场被 adapter 拒
- **风险对比**:
  - Claude:产品 UX 上 6 个 vendor 半亮半暗;用户疑惑(为啥有的能开有的不能)
  - Codex:用户看到全部 vendor,选了 cursor 但请求 fail → 体验差
- **借鉴对照**:
  - `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:111-145` — 全 provider 注册 + 显式拒绝无效 provider → Codex 同路线
  - `Wei-Shaw/sub2api@63b0631a5827`(本日 anchor)— vendor 按 enabled 字段过滤,disabled 不出现在 UI → Claude 同路线
- **Owner 拍板维度**:HUAKAI 产品定位 — 想像 Portkey 全 vendor 注册显示 fail,还是 sub2api 完工才出?

### B-2 Schema migration 加 health_state / endpoint catalog?

- **Claude D-4(refresh closure)+ placeholder D-3**:加 migration 0008 (health_state) / 0009 (endpoint catalog) / 0010 (oauth devicecode)
- **Codex D-8(本 plan)+ D-SCHEMA-001(refresh plan)**:不动 schema,用 JSON metadata;任何 schema 改动需 Owner 高风险确认
- **冲突点**:health 状态 / endpoint URL / device-code session metadata 这 3 类信息存哪
- **借鉴对照**:
  - `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:641` — credential payload 加密存,无新 schema → Codex 路线
  - `envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:47` — provider 配置作为 first-class control-plane 对象,有 schema → Claude 路线
- **Owner 拍板维度**:HUAKAI 是否要 ops dashboard 直接查 vendor health(需要 schema 列)还是只通过 API 查(JSON 够)。

## §C 单边维度(可独立拍)

### C-1 Copilot service-token refresh 完整度 (Codex D-3)

- A: 真 refresh adapter,exchange GitHub auth → Copilot service token + 存 expiry + API base (Recommended)
- B: 静态 session token mode,过期就让 user 重 OAuth
- Codex 推 A,因为 B 会过期不可用
- **Claude 同 A**(D-1 paraphrase litellm authenticator.py)
- **共识**:A,无需 surface(已在 §A)

### C-2 Gemini Advanced refresh (Codex D-4)

- A: 复用 Gemini OAuth refresh + 动态 browser endpoint (Recommended)
- B: hardcode browser endpoint + cookie only
- Codex 推 A;Claude 在 G-2 endpoint catalog 切片里也是 A
- **共识**:A

### C-3 Antigravity endpoint 来源 (Codex D-5)

- A: Google Code Assist 控制面元数据 + project discovery (Recommended)
- B: 保 placeholder chat-completions 域名
- Codex 强推 A,B 不是生产
- **Claude 切片 P-C 也是 A**(读 CLIProxyAPI antigravity 包 OAuth scope + Google CodeAssist 后端)
- **共识**:A

## §D 推荐执行序

1. **P-A Copilot device-code 闭环** — 共识最齐全,可立即开工
2. **P-B Gemini Advanced 网页协议 + SAPISIDHASH + bl=** — ref 中等,可开工
3. **P-C Antigravity Google CodeAssist** — ref 较弱但思路一致,可开工
4. **P-D Cursor 抓包前不开工** — Owner 提供 endpoint capture 后启动
5. **P-E/F Windsurf/Kiro** — 研究切片,无 ref,Owner 抓包后再说
6. **P-Z registry default 翻** — 卡在 §B-1 Owner 决策

## §E 借鉴项目对照(CLAUDE.md #15 给 Owner)

| 维度 | Portkey-AI/gateway (MIT @d2ea41f4) | Wei-Shaw/sub2api (LGPL @63b0631a) | litellm (Apache-2.0 @41486676) | CLIProxyAPI (MIT @50d19e20) |
|---|---|---|---|---|
| Registry default | 全 provider 注册 + 显式验证拒绝 | enabled flag 过滤 (隐藏 disabled) | proxy 注册全 vendor | CLI 工具,无 registry 概念 |
| Vendor endpoint catalog | provider/index.ts hardcode | 配置表 + UI 改 | model_prices_and_context_window.json | 无(单 vendor) |
| Copilot refresh | 无(不支持 copilot) | 无 | github_copilot/authenticator.py:83-193 (完整) | 无 |
| Antigravity | 无 | 无 | 无 | internal/auth/antigravity/auth.go:226-377 (完整) |
| Cursor / Kiro / Windsurf | 无 | 无 | 无 | 无 |
| 适合 HUAKAI | registry default 模式参考 | enabled flag 模式参考 | copilot 实现 ref | antigravity / gemini 实现 ref |

## §F Owner 决策清单(Surface)

| ID | 决策 | 选项 | 推荐 | 必要性 |
|---|---|---|---|---|
| PH-D1 (B-1) | Registry default 启用策略 | (A) 全 default-on + fail-closed / (B) 完工逐个翻 | **Owner 选 — 影响产品 UX 定位** | **必决**,卡 P-Z |
| PH-D2 (B-2) | Schema migration 加 health/endpoint 列 | (A) JSON metadata / (B) 显式 schema | **Owner 选 — 影响 ops dashboard 能力** | **必决**,卡 G-2/G-4 |
| PH-D3 (Claude D-6) | 错误响应 N 连续封策略 | 3 连封 + 30min cooldown / 1 次 1h / 指数退避 / 配置可调 | (D) 配置可调默认 3 连封 + 30min | 后排 |
| PH-D4 (Codex Antigravity, 共识) | Google CodeAssist endpoint 复用 | (A) 控制面元数据 + project discovery | 已共识 A | 已决 |
| PH-D5 (Codex Copilot, 共识) | Copilot 真 refresh adapter | (A) 真 refresh | 已共识 A | 已决 |
| PH-D6 (Codex Gemini, 共识) | Gemini Advanced refresh | (A) 动态 endpoint + Gemini OAuth | 已共识 A | 已决 |

## §G Lane + UTC

- Synthesis: Claude (claude-opus-4-7)
- UTC: 2026-05-24T07:45Z
- Inputs: Claude §6 + Codex §6 + 共 12 D 决策对照
