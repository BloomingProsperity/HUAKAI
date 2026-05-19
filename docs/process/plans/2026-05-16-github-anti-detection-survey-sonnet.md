# 2026-05-16 Github 反检测/对抗 成熟项目调研 (Sonnet lane)

| 字段 | 值 |
|---|---|
| Lane | Sonnet subagent (general-purpose, 资料汇总 / 大批量 WebFetch — memory `feedback_sonnet_only_low_stakes` 允许范围) |
| Owner directive | 2026-05-16 "再派 sonnet 去抓取 github 上成熟的对抗项目" |
| Memory ref | [[feedback_anti_detection_specs_claude_writes]] [[feedback_sonnet_only_low_stakes]] [[feedback_dont_ask_check_refs]] |
| Scope | Github 反检测 / 反封禁 / 对抗 项目矩阵 + 反封禁 7 层 (L0-L6) mapping + Top 5 推荐 |
| 关联 | 给 [2026-05-16-all-vendor-subscription-anti-detection-roadmap-claude.md](2026-05-16-all-vendor-subscription-anti-detection-roadmap-claude.md) 补强证据 |
| Sonnet agent | agentId `a952e74f35e924da1` (可 SendMessage 继续) |
| 抓取范围 | 25+ projects across 7 categories |
| 抓取手段 | 25 次 WebSearch + 19 次 WebFetch |
| UTC | 2026-05-16T~08:30 UTC |

## ⚠️ 数据准确度 disclaimer (Owner review 后补)

本调研由 sonnet lane 限时 25 分钟内 25 次 WebSearch + 19 次 WebFetch 完成. **部分项目的 star 数 / 最近更新日期 仅自 WebSearch 摘要推断**, 未单独 WebFetch 各项目 README + GitHub releases 页 verify. 已知不精确点:
- CycleTLS: 仅"活跃" 标识无具体日期
- Botright: 仅 ⭐ 982 无日期
- HumanCursor: 仅"活跃"无 star 无日期
- AuthenticCursor: 标"小项目"无 star/日期
- CLIProxyAPI: 抓到 v7.0.9 实际 GitHub releases 页 latest 是 v7.0.8 (2026-05-16 04:26 UTC), 可能是 in-flight release 抓到草稿

**codex implementer 在引用本 survey 进入实施 Phase 前, 必须重 WebFetch 各项目 README + releases 页 verify 当前最新 star/日期/反检测技术细节** (项目活跃度 + 技术 4 周内可能变化). 本 survey 作 Phase 启动 initial reference, 非生产决策最终源.

## 1. TLS 指纹库 (L1 层) — 6 项目

- **rquest** ([github.com/penumbra-x/rquest](https://github.com/penumbra-x/rquest)) — 2026-05 活跃 (v0.30.x), ⭐ ~12 (新 fork; 原 0x676e67/rquest-deprecated 已归档)
  - 卖点: "Black Magic" Rust HTTP/WebSocket 客户端, 精细控制 TLS + JA3/JA4 + HTTP/2 签名
  - 反检测技术: BoringSSL 驱动, 100+ 浏览器设备仿真 profile (via wreq-util), 精细扩展顺序控制 (非简单字符串匹配); Chrome 123–137 均可覆盖
  - HUAKAI 借鉴: **L1 核心** = HUAKAI Rust sidecar 的主体库; JA4+ 自适应 + H2 帧精确复刻
  - 风险: Apache 2.0 可 vendor; 新 fork 社区小, 需跟踪 0x676e67 上游; Chrome 版本追踪持续维护

- **wreq** ([github.com/0x676e67/wreq](https://github.com/0x676e67/wreq)) — 2026-02-11 (v6.0.0-rc.28), ⭐ 805
  - 卖点: ergonomic Rust HTTP 客户端, 同 rquest 同一作者
  - 反检测: 100+ 浏览器设备仿真 profile; JA3/JA4 + Akamai HTTP2 指纹
  - HUAKAI: **L1 + L2** 备选; wreq-util profile 库直接可用
  - 风险: Apache 2.0; 2026-02 后无 release, 可能被 rquest 吸收

- **curl_cffi** ([github.com/lexiforest/curl_cffi](https://github.com/lexiforest/curl_cffi)) — 2026-04-23 (v0.15.1b1), ⭐ 5.6k
  - 卖点: Python binding for curl-impersonate, 37 浏览器 preset, **支持 HTTP/3 fingerprint (Chrome 145/146, Firefox 147)**
  - 反检测: BoringSSL (Chrome) / NSS (Firefox); JA3 + JA4; HTTP/2 + HTTP/3 QUIC 指纹; chrome124/chrome135 preset
  - HUAKAI: **L1 测试 + HTTP/3 参考** = Python POC 首选; profile 列表可反向指导 Rust sidecar 应覆盖哪些版本
  - 风险: MIT; Python 性能不适合 high-throughput

- **curl-impersonate** ([github.com/lwthiker/curl-impersonate](https://github.com/lwthiker/curl-impersonate)) — 2024-03-02 (v0.6.1, **维护近停**), ⭐ 6k
  - 卖点: curl 特殊编译, 精确复现 Chrome/Edge/Safari/Firefox TLS + HTTP 握手
  - 反检测: BoringSSL 编译; Chrome 99–116, Firefox 91–117, Edge/Safari
  - HUAKAI: **L1 基础参考** 理解 BoringSSL patch
  - 风险: MIT; 维护近停, 推荐用 lexiforest/curl-impersonate fork 代替

- **uTLS / refraction-networking/utls** ([github.com/refraction-networking/utls](https://github.com/refraction-networking/utls)) — 2026-01-13 (v1.8.2), ⭐ 2.4k
  - 卖点: Go 标准 TLS fork, ClientHello 低级访问, 浏览器 parroting
  - 反检测: Chrome 62/70/72/83, Firefox 56/65, iOS 11.1/12.1; HelloCustom + HelloRandomized; CycleTLS/fhttp 底层依赖
  - HUAKAI: **L1** Go control-plane 层 (若有 Go 网络代码必须基于此); 但 profile 偏旧 (最高 Chrome 83)
  - 风险: MIT + BSD

- **CycleTLS** ([github.com/Danny-Dasilva/CycleTLS](https://github.com/Danny-Dasilva/CycleTLS)) — 活跃, ⭐ 1.4k
  - 卖点: Go + JS 双语言 TLS/JA3 欺骗, **唯一同时支持 JA4 + JA4R + HTTP/3 + SSE + WebSocket 的 Go 库**
  - 反检测: JA3/JA4/JA4R; H2; HTTP/3 QUIC; SOCKS4/5; Chrome + Firefox profile
  - HUAKAI: **L1 + L2** Go 层 JA4R + HTTP/3 主参考; SSE 支持对流式上游重要
  - 风险: MIT

## 2. 设备指纹库 / 浏览器伪装 (L3 层) — 5 项目

- **camoufox** ([github.com/daijro/camoufox](https://github.com/daijro/camoufox)) — 2026-05-11 (v150.0.2-beta.25), ⭐ **8.4k**
  - 卖点: Firefox fork for AI agents, **C++ 层指纹注入** (不可 JS 检测), 专为 scale 设计
  - 反检测: C++ 实现层修改 navigator/屏幕分辨率/WebGL/WebRTC/AudioContext/字体 metrics; BrowserForge 真实流量分布; 基于 Firefox 150 最新
  - HUAKAI: **L3 顶推** = headless 浏览器执行层 (账号验证流) 首选; C++ 注入是标杆
  - 风险: MPL-2.0; Google 已明确禁此用途, Antigravity 代理用户账号被封; 维护者个人情况有间隔

- **undetected-chromedriver** ([github.com/ultrafunkamsterdam/undetected-chromedriver](https://github.com/ultrafunkamsterdam/undetected-chromedriver)) — v3.5.0, ⭐ **12.6k**
  - 卖点: chromedriver 二进制 patch, 通过 Distil/Imperva/DataDome/Cloudflare
  - 反检测: chromedriver 二进制 patch; 防自动注入 (v3.4.0+); CDP 集成; headless undetected
  - HUAKAI: **L3** 成熟度最高的 Chrome 自动化伪装参考 (但 nodriver 继任)
  - 风险: GPL-3.0 (不可 vendor); 维护下降

- **nodriver** ([github.com/ultrafunkamsterdam/nodriver](https://github.com/ultrafunkamsterdam/nodriver)) — 活跃, ⭐ 4.2k
  - 卖点: undetected-chromedriver 继任, 消除 WebDriver/Selenium 依赖, 直接 CDP; 内置 cf_verify() Cloudflare bypass
  - 反检测: 直 CDP; 每次 fresh profile; shadow-root always open; iframe 集成; 绕 hCaptcha + Cloudflare
  - HUAKAI: **L3 + L6** = 账号登录/维护自动化的最隐蔽路径; cf_verify() 是 L6 主动对抗直接参考
  - 风险: GPL-3.0 (仅参考)

- **fingerprint-suite (Apify)** ([github.com/apify/fingerprint-suite](https://github.com/apify/fingerprint-suite)) — 2026-05-04 (v2.1.83), ⭐ 2.2k
  - 卖点: 生产级 Playwright/Puppeteer 指纹生成+注入, 贝叶斯网络训练于真实流量
  - 反检测: HTTP header 生成 + 浏览器 JS API 指纹 + Playwright/Puppeteer 注入; 统计真实分布
  - HUAKAI: **L3** 指纹生成统计方法可借鉴; fingerprint-generator 逻辑可指导 HUAKAI 设备指纹库
  - 风险: Apache 2.0 (部分可 vendor); 2026 JS 注入识别率上升

- **DrissionPage** ([github.com/g1879/DrissionPage](https://github.com/g1879/DrissionPage)) — 2025-03-21 (v4.1.0.17), ⭐ **12k**
  - 卖点: Python 浏览器自动化, 非 WebDriver 协议架构
  - 反检测: 非 WebDriver (绕过 navigator.webdriver); 自研引擎
  - HUAKAI: **L3** 非 WebDriver 架构思路; 中文社区最大
  - 风险: MIT; 2025-03 后无更新

## 3. CDP 反检测 / Stealth 插件 (L3 + L6 层) — 3 项目

- **rebrowser-patches** ([github.com/rebrowser/rebrowser-patches](https://github.com/rebrowser/rebrowser-patches)) — 2025-05-09 (v1.0.19), ⭐ 1.4k
  - 卖点: Puppeteer + Playwright 双 patch, 修复 **Runtime.Enable CDP 泄漏** (主要 anti-bot 检测手段)
  - 反检测: 禁用自动 Runtime.Enable; 混淆 sourceURL (pptr:... → app.js); Utility World 名随机化; Puppeteer 24.8.1 + Playwright 1.52.0
  - HUAKAI: **L3 + L6 必选** = CDP 泄漏修复是 headless 账号管理必选; Runtime.Enable 检测是所有主流 anti-bot 核心手段
  - 风险: MIT; 2025-05 后无更新

- **puppeteer-extra + puppeteer-extra-plugin-stealth** ([github.com/berstend/puppeteer-extra](https://github.com/berstend/puppeteer-extra)) — ⭐ 7.3k (monorepo)
  - 卖点: 插件化 Puppeteer; stealth 覆盖 navigator.webdriver + UA + canvas
  - 反检测: navigator.webdriver=undefined; HeadlessChrome UA 替换; 多模块 (webgl, canvas, chrome.runtime); 通过 fpscanner/Intoli/areyouheadless
  - HUAKAI: **L3** 成熟度最高的 Puppeteer stealth; plugin 模式参考
  - 风险: MIT; 2026 headless 识别率上升 ~25%

- **Selenium-Driverless** ([github.com/kaliiiiiiiiii/Selenium-Driverless](https://github.com/kaliiiiiiiiii/Selenium-Driverless)) — ⭐ 852, 活跃
  - 卖点: 无 chromedriver 的 Selenium, 通过 Cloudflare/Bet365/Turnstile
  - 反检测: 直 CDP; isolated world JS; pointer 加速度+平滑模拟; 网络拦截; CDP-patches
  - HUAKAI: **L3 + L4** pointer 物理模型可借鉴 L4 行为节奏; isolated world 防 JS 探针
  - 风险: **非商业许可 (educational only)**; 不可 vendor

## 4. 行为模拟 / 节奏仿真 (L4 层) — 3 项目

- **Botright** ([github.com/Vinyzu/Botright](https://github.com/Vinyzu/Botright)) — ⭐ 982
  - 卖点: 开源 Playwright 框架, 内置 fingerprint 轮换 + 免费 AI 验证码破解 (hCaptcha 90%, reCAPTCHA 50-80%)
  - 反检测: 真实 Chromium 本地实例; 自爬 Chrome 指纹数据库; AI 视觉 captcha 破解 (cv2+CLIP+数学)
  - HUAKAI: **L4 + L6** captcha 破解模式参考 L6 主动对抗; fingerprint 自爬思路
  - 风险: GPL-3.0 (不可 vendor); captcha 不稳定

- **HumanCursor** ([github.com/riflosnake/HumanCursor](https://github.com/riflosnake/HumanCursor)) — 活跃
  - 卖点: 模拟真实人类鼠标曲线 (变速+加速度+曲率), Selenium 支持
  - 反检测: 贝塞尔曲线 + 随机速度/加速度; 非瞬间 teleport
  - HUAKAI: **L4** 账号初始化/登录人类行为; 轨迹算法可移植
  - 风险: MIT; 功能单一

- **AuthenticCursor** ([github.com/matt-nann/AuthenticCursor](https://github.com/matt-nann/AuthenticCursor)) — 小项目
  - 卖点: 生成式 AI 训练于真实人类鼠标数据, bypass web bot detection
  - HUAKAI: **L4** AI 生成鼠标轨迹是 L4 前沿; 参考数据集 + 模型
  - 风险: MIT; 项目较小, 未大规模 production 验证

## 5. AI 网关 / 反代级 Anti-Detection (L1-L6 整合参考) — 5 项目

- **CLIProxyAPI** ([github.com/router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)) — **2026-05-16 04:26 UTC (v7.0.8 latest, 非 v7.0.9 — sonnet 抓 7.0.9 是 in-flight, GitHub releases 页 latest 是 v7.0.8)**, ⭐ **32.8k** + Fork 5.5k
  - 卖点: **最大的订阅转 API 项目** (Go), Gemini/ChatGPT/Claude/Codex/Antigravity; release notes 提到 "Claude Chrome TLS fingerprint + 3 anti-detection fixes"
  - 反检测: Chrome TLS 指纹仿真; 多账号轮换; OAuth token 管理
  - HUAKAI: **L1 整合参考 (Tier 1 竞品)** = 32.8k ⭐ 说明市场需求; anti-detection fix commit log 是 HUAKAI 情报源
  - 风险: MIT snapshot (见 kaitranntt/CLIProxyAPIPlus fork); Google 已封部分账号

- **AIClient2API** ([github.com/justlovemaki/AIClient2API](https://github.com/justlovemaki/AIClient2API)) — 2026-05-14 (v3.0.7), ⭐ 7.8k
  - 卖点: Node.js, Gemini/Claude/Grok/Codex/Kiro; **内置 Go uTLS sidecar** (Chrome 最新指纹 + H2 自动协商), 专为 Grok 等严格 TLS 检测
  - 反检测: Go uTLS sidecar (Chrome 最新 TLS + HTTP/2 自动协商); 多协议转换; Kiro Claude 免费通道
  - HUAKAI: **L1 关键参考** = 唯一已落地 uTLS sidecar 方案, 与 HUAKAI R-3 Rust sidecar 思路一致, 可参考 sidecar 架构
  - 风险: MIT; Node.js + Go sidecar 混合复杂; 封号风险持续

- **anti-api** ([github.com/ink1ing/anti-api](https://github.com/ink1ing/anti-api)) — 2026-03-25 (v2.9.0), ⭐ 380
  - 卖点: Antigravity/Codex/GitHub Copilot → Anthropic & OpenAI API; Kiro provider + 多账号
  - 反检测: **无 TLS 指纹欺骗文档**; 标准本地代理
  - HUAKAI: **L6 参考** Kiro + 多 provider 接入逻辑; 账号管理 + quota 可视化
  - 风险: MIT; 反代稳定性依赖上游不变; **无 anti-detection 层是明显弱点**

- **antigravity-proxy (frieser)** ([github.com/frieser/antigravity-proxy](https://github.com/frieser/antigravity-proxy)) — 2026-02-27, ⭐ 53
  - 卖点: Go 实现, Antigravity → OpenAI 兼容; 账号健康评分 + quota 自动冷却 + 自动 project 发现
  - 反检测: Cloud SDK 仿真; 账号轮换 + 健康评分
  - HUAKAI: **L5 + L6** 账号健康评分算法是 L5 IP 池 + L6 策略切换的直接参考
  - 风险: MIT; 53 ⭐ 社区小; Google 已禁

- **puppeteer-real-browser** ([github.com/ZFC-Digital/puppeteer-real-browser](https://github.com/ZFC-Digital/puppeteer-real-browser)) — 2026 社区维护 (原作者 2026-02 停)
  - 卖点: 绕过 Puppeteer bot-detecting captchas (Cloudflare)
  - 反检测: 真实浏览器实例 (非 headless); Puppeteer 管理但外观如真用户
  - HUAKAI: **L3 极端保障** = 账号初始化场景 non-headless 策略
  - 风险: 原作者停止维护; 依赖社区; 资源消耗大

## 6. JA4+ 工具 / 指纹感知反代 (情报层) — 4 项目

- **fingerproxy** ([github.com/wi1dcard/fingerproxy](https://github.com/wi1dcard/fingerproxy)) — 2025-05-25 (v1.2.3), ⭐ 316
  - 卖点: HTTPS 反代, 提取 JA3/JA4/Akamai HTTP2 指纹转 header; **生产环境 Subscan.io 12 实例 4000 万请求/天**
  - 反检测: JA3 + JA4 + Akamai HTTP2 提取; X-JA3-Fingerprint / X-JA4-Fingerprint header
  - HUAKAI: **L0 + L6 情报** = 监控上游对 HUAKAI 自身流量的 fingerprint 检测; HUAKAI fingerprint-sensing 中间件
  - 风险: MIT; 功能单一 (提取不伪装)

- **finch (0x4D31)** ([github.com/0x4D31/finch](https://github.com/0x4D31/finch)) — 早期 v0.1.0 (非生产就绪)
  - 卖点: 指纹感知 TLS 反代, block/reroute/tarpit/**deceive** 流量; JA3/JA4/JA4+QUIC/JA4H/HTTP2; HCL 热重载规则
  - 反检测: 基于指纹的规则引擎; QUIC JA4 支持
  - HUAKAI: **L6 主动对抗直接参考** = "检测上游测我们时切策略"; **deceive 模式 (对探针伪装) 是 L6 核心能力**
  - 风险: 个人项目 v0.1.0 非生产就绪; 仅参考架构; 基于 fingerproxy

- **impersonator (zhkl0228)** ([github.com/zhkl0228/impersonator](https://github.com/zhkl0228/impersonator)) — 更新 2024-2025
  - 卖点: Java 版 TLS/JA3/JA4 + HTTP/2 指纹欺骗 (OkHttp 扩展)
  - 反检测: bctls (TLS/JA3/JA4); okhttp (TLS+H2); ImpersonatorFactory.ios() profile API
  - HUAKAI: **L1 参考** Java 生态实现; IOS/Chrome profile API 设计模式
  - 风险: 小众, Java 与 HUAKAI Rust 主线无直接复用

- **niespodd/browser-fingerprinting** ([github.com/niespodd/browser-fingerprinting](https://github.com/niespodd/browser-fingerprinting)) — 知识库
  - 卖点: 系统梳理 bot 检测系统工作原理 + 可用对抗手段; 含实测 test suite
  - 反检测: 分析 FingerprintJS / DataDome / Cloudflare / Imperva 检测逻辑; 列 countermeasures
  - HUAKAI: **L0 情报** = 规划 HUAKAI 防护栈分类框架; 测试套件可复用 HUAKAI 自测
  - 风险: 知识库性质, 无代码实现; 更新频率不明

## 7. 补充 — node-wreq

- **node-wreq** ([github.com/StopMakingThatBigFace/node-wreq](https://github.com/StopMakingThatBigFace/node-wreq)) — 极新, ⭐ 小
  - 卖点: TypeScript HTTP 客户端, JA3/JA4 浏览器伪装, 底层 wreq Rust core (Node.js FFI)
  - 反检测: TLS + HTTP2 + JA3/JA4; Rust 性能 + TypeScript 接口
  - HUAKAI: **L1** 验证 Rust wreq core + 语言 FFI 架构方向 (HUAKAI Rust sidecar 同路)
  - 风险: 极新, 生产稳定性未知

---

## HUAKAI 应优先借鉴的 5 个项目 (Top 5)

| # | 项目 | 优先级 | 对应 HUAKAI 层 | 核心可借鉴能力 |
|---|---|---|---|---|
| 1 | **rquest / wreq** (Rust) | ⭐⭐⭐ 必选 | L1 核心 | HUAKAI Rust sidecar 直接实现基础; wreq-util 100+ profile + JA4 + H2 精确控制; Apache 2.0 可 vendor |
| 2 | **curl_cffi** (Python) | ⭐⭐ 测试参考 | L1 + HTTP/3 | 5.6k ⭐, 2026-04 活跃; HTTP/3 QUIC 指纹 (领先能力); 测试基准 |
| 3 | **AIClient2API** (Node.js+Go sidecar) | ⭐⭐⭐ 必读竞品 | L1 整合 | 7.8k ⭐, **唯一已落地 uTLS Go sidecar** 方案, 与 HUAKAI R-3 思路一致 |
| 4 | **camoufox** (Firefox fork) | ⭐⭐ 账号管理 | L3 | 8.4k ⭐, C++ 层指纹注入 (不可 JS 检测), Firefox 150 最新; headless 账号管理首选 |
| 5 | **rebrowser-patches + finch** | ⭐⭐⭐ L3+L6 必选组合 | L3 + L6 | rebrowser-patches 修复 Cloudflare/DataDome 核心检测点 (Runtime.Enable CDP 泄漏); finch deceive/reroute 模式直接对应 HUAKAI L6 "检测上游测我们时切策略" |

## 调研手段统计

- WebSearch 共 25 次 query (含 antigravity / utls / camoufox / nodriver / rebrowser-patches / fingerproxy / finch / DrissionPage / Botright / HumanCursor 等)
- WebFetch 共 19 次 (重点项目 README 全 fetch)
- 总耗时 ~25 分钟硬上限内完成

## Sonnet lane 限制说明

按 memory `feedback_sonnet_only_low_stakes`, sonnet 仅做"资料汇总 + 大文件 grep" 类低 stakes 工作. 本调研是 WebSearch + WebFetch 批量抓取 + 整理, 符合 sonnet 适用范围. **不**包括: 反检测代码实施 / 关键架构决策 / 法律风险判断 — 这些需 Claude/Codex/Owner 做.

## Source / Lane / UTC

- WebSearch (25 queries) + WebFetch (19 URLs), 全 fetch 2026-05-16
- Lane: sonnet subagent (general-purpose)
- Agent: Claude Sonnet 4.6
- AgentId: `a952e74f35e924da1` (可 SendMessage 继续抓取更多项目)
- UTC: 2026-05-16T~08:30 UTC
