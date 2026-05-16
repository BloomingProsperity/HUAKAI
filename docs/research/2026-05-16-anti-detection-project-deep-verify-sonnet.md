# 反封禁 Top 5 项目 Deep Verify Report

**Fetch 执行时间:** 2026-05-16 UTC (session 内完成)
**执行者:** Claude Sonnet (agent a9b37320565166679)
**任务范围:** WebFetch README + 抽样 source — 不 clone, 不 commit, 不派 Codex
**硬上限:** 25 分钟

---

## 重要前置说明

本报告 fetch 过程中发现 **rquest 已改名为 wreq**（`penumbra-x/rquest` 重定向至 `penumbra-x/wreq`，上游维护者为 `0x676e67/wreq`）。README 内容相同，Cargo.toml 包名已为 `wreq = "6.0.0-rc.28"`。下文 "rquest/wreq" 并列指同一项目。

---

## 1. rquest / wreq

### 基本信息

| 字段 | 值 |
|---|---|
| 仓库 | `penumbra-x/rquest` → 重定向到 `penumbra-x/wreq` (fork of `0x676e67/wreq`) |
| 上游维护仓库 | `0x676e67/wreq` |
| Stars | 上游 805 / penumbra fork 0 |
| 最近 push | 2026-05-11 (上游) / 2026-05-12 (penumbra fork) |
| archived | false |
| License | Apache-2.0 |
| 语言 | Rust (edition 2024, MSRV 1.85+) |
| Cargo 包版本 | wreq 6.0.0-rc.28 |
| TLS 底层 | btls 0.5.6 + btls-sys 0.5.6 + tokio-btls 0.5.6 (BoringSSL 绑定) |
| HTTP/2 | http2 0.5.17 |

**WebFetch URL:** `https://github.com/penumbra-x/rquest` (README), `https://github.com/penumbra-x/rquest/blob/main/Cargo.toml`, `https://github.com/penumbra-x/rquest/blob/main/src/tls.rs`, `https://github.com/penumbra-x/rquest/blob/main/src/client/emulate.rs`

**Fetch UTC:** 2026-05-16 session

### 功能特性

README 明确列出:

- Plain bodies, JSON, urlencoded, multipart
- HTTP Trailer
- Cookie Store
- Redirect Policy
- Original Header 保留
- Rotating Proxies
- Tower Middleware
- WebSocket Upgrade
- **HTTPS via BoringSSL**
- **HTTP/2 over TLS Parity** (精确 HTTP/2 帧参数匹配)
- Certificate Store (CAs & mTLS)
- 100+ browser device emulation profiles (维护在 `wreq-util` companion crate 中)

### TLS Profile 数据契约 (paraphrased behavior contract; Apache-2.0 source 不复制 verbatim struct/field 命名)

rquest/wreq 暴露的 TLS 配置 surface 包含以下**行为类别** (HUAKAI 实施时自定命名):

- **ALPN 协议列表**: 支持 HTTP/1.1 + HTTP/2 + HTTP/3 三协议 (各自常量), 默认双协议 (HTTP/2 优先 + HTTP/1.1 fallback)
- **TLS Session Ticket 开关**: RFC 5077 控制
- **TLS 最小/最大版本**: 上下边界
- **Cipher 套件**: BoringSSL 接受 mini-language 字符串
- **GREASE 扩展开关**: RFC 8701 random padding
- **Delegated Credentials 开关**: TLS 1.3 临时委托凭证

(具体 struct 名 / field 名 / 常量名 见 Apache-2.0 source, HUAKAI 实施可借鉴**行为类别 + 默认值**, 自定 struct/field 命名风格)

默认行为: ALPN = HTTP/2 + HTTP/1.1 双协议, GREASE 开。

### 浏览器伪装配置契约 (paraphrased; Apache-2.0 source 不复制 verbatim)

rquest/wreq 浏览器伪装的**配置类别** (HUAKAI 自定命名):

- **分组类别**: 把多个 profile 归组 (组织角度)
- **标准 HTTP header 集合**: 注入到 outbound request
- **保留原始大小写 + 重复 header 集合**: 防 normalize 改原 header pattern
- **TLS 配置**: 可选 TLS 子配置 (跟 TLS profile 类别一致)
- **HTTP/1.1 配置**: 可选 HTTP/1.1 子配置
- **HTTP/2 配置**: 可选 HTTP/2 子配置

**Chrome 版本列表:** README 仅示例 `Emulation::Safari26`, 实际 100+ profile 存放于 `wreq-util` crate，`wreq-util/src` 目录不可通过 raw WebFetch 访问 (404)。docs.rs 页面也返回 404 (rc 版本未发布到 crates.io 正式版)。**Chrome 具体版本枚举无法通过 WebFetch 获取 — 需本地 clone `0x676e67/wreq` 查看 `wreq-util/src/`。**

### Cargo features (完整)

默认: `webpki-roots`

可选: `charset`, `cookies`, `gzip`, `brotli`, `zstd`, `deflate`, `query`, `form`, `json`, `multipart`, `hickory-dns`, `stream`, `socks`, `ws`, `system-proxy`, `tracing`, `parking_lot`, `prefix-symbols`

`prefix-symbols` 特性: 启用 `btls/prefix-symbols` 解决与其他 OpenSSL 实例的链接冲突 — **HUAKAI 集成时关键 feature**。

### HUAKAI 借鉴层映射

| HUAKAI 层 | 借鉴能力 |
|---|---|
| **L2 TLS 指纹层** | 核心 — btls/BoringSSL + TlsOptions 的 cipher_list / grease / ALPN 精确控制 |
| **L3 HTTP/2 帧层** | Http2Options 合同可精确控制 SETTINGS 帧参数、窗口大小、header 压缩参数 |
| **L4 UA/Header 层** | orig_headers 保留大小写 + 重复 header, 与 browser 真实行为一致 |
| **R-3 Rust sidecar** | 直接作为 HUAKAI Rust core gateway 的 HTTP client 底层 (替代 Go uTLS) |

### 集成复杂度

**Hard (架构级)**

原因:
1. `wreq` 是 rc 版本 (6.0.0-rc.28)，API 未稳定
2. 浏览器 profile 完整枚举不可通过公开文档确认 (需本地读 wreq-util)
3. 集成到 HUAKAI Rust core 需要设计 profile 选择 → pool binding → rotation 的协议
4. `prefix-symbols` feature 必须在链接时正确配置
5. BoringSSL 构建依赖需要 C 工具链在 CI 中预备

**Upgrade delta vs AIClient2API uTLS sidecar:**
- 架构升级: 原生 Rust 不跨进程 (vs Go sidecar via HTTP localhost — 减少一次序列化往返)
- 算法升级: `prefix-symbols` 解决多 OpenSSL 共存问题 (Go uTLS 没有此能力)
- 生态升级: Tower middleware 集成直接接入 HUAKAI 的 retry/timeout/tracing 层

---

## 2. curl_cffi

### 基本信息

| 字段 | 值 |
|---|---|
| 仓库 | `lexiforest/curl_cffi` |
| Stars | 5,601 |
| 最近 push | 2026-05-13 |
| archived | false |
| License | **MIT** |
| 语言 | Python (via cffi C bindings) |
| 描述 | Python binding for curl-impersonate fork; browser TLS/JA3/HTTP2 fingerprint impersonation |

**WebFetch URL:** `https://raw.githubusercontent.com/lexiforest/curl_cffi/main/README.md`, `https://api.github.com/repos/lexiforest/curl_cffi`

### Browser Profile 支持

- **37 preset fingerprints** 当前可用 (完整列表见 curl-impersonate docs, 本报告 WebFetch 无法直接获取枚举)
- 使用方式: `impersonate="chrome"` (自动追踪最新版本) 或 `impersonate="chrome124"` (固定版本)
- Chrome 135 跳过 (无新能力)
- **HTTP/3 QUIC fingerprint**: v0.11.4 加入基础 HTTP/3, v0.15.0 加入 HTTP/3 + UDP SOCKS5 proxy 支持
- Chrome 145/146: README 未明确提及版本号，使用 `impersonate='chrome'` 自动追踪最新
- 安装: `pip install curl_cffi --upgrade`

### 与其他库对比 (README 比较表)

| 能力 | requests | aiohttp | httpx | curl_cffi |
|---|---|---|---|---|
| HTTP/2 | ✗ | ✗ | ✓ | ✓ |
| HTTP/3 | ✗ | ✗ | ✗ | ✓ |
| 异步 | ✗ | ✓ | ✓ | ✓ |
| WebSocket | ✗ | ✓ | ✓ | ✓ |
| 浏览器指纹 | ✗ | ✗ | ✗ | **✓ (唯一)** |

### rquest/wreq vs curl_cffi 对比

| 维度 | rquest/wreq | curl_cffi |
|---|---|---|
| 语言 | Rust (native) | Python (cffi → C) |
| HTTP/3 | HTTP/3 ALPN 常量存在，实际 QUIC 实现 TBD | ✓ v0.15.0+ |
| 集成方式 | Rust crate 直接集成 | Python subprocess 或 sidecar |
| HUAKAI 适配 | 直接用于 Rust gateway 数据面 | 需 Python sidecar 或 subprocess |
| License | Apache-2.0 | MIT |
| 生产稳定性 | rc 版本, API 未固定 | 稳定, 5.6K stars |
| Profile 数量 | 100+ (wreq-util) | 37 |
| 浏览器指纹精度 | JA3/JA4 + H2 SETTINGS | JA3/JA4 + H2 (curl-impersonate 基础) |

**HUAKAI 结论 (事实, 非决策):** curl_cffi 适合 Python 工具链 (爬虫/测试) 或需要 HTTP/3 QUIC 指纹的场景。HUAKAI 的 Rust gateway 数据面不直接使用 Python 库, 但 curl_cffi 可作为 **L5 HTTP/3 指纹验证工具** 和 **R-3 测试参考** (对比 QUIC 握手参数)。

### HUAKAI 借鉴层映射

| HUAKAI 层 | 借鉴能力 |
|---|---|
| **L2 TLS 指纹** | 参考 37 个 profile 的 cipher/extension 排列 (与 wreq-util 交叉验证) |
| **L5 HTTP/3 QUIC** | QUIC 指纹参数设计参考 (curl_cffi v0.15.0 是当前唯一支持 QUIC fingerprint 的 OSS 实现) |
| **测试/CI** | 作为独立验证工具: `curl_cffi.get(url, impersonate="chrome")` 对比 HUAKAI gateway 输出 |

### 集成复杂度

**Medium (工具层)**

curl_cffi 不直接进 HUAKAI Rust 数据面, 但可作为:
- CI 中的 fingerprint oracle (Python, pip install 即用)
- L5 HTTP/3 fingerprint 设计文档来源

---

## 3. AIClient2API

### 基本信息

| 字段 | 值 |
|---|---|
| 仓库 | `justlovemaki/AIClient2API` |
| Stars | 7,846 |
| 最近 push | 2026-05-15 |
| archived | false |
| License | **GPL-3.0** ⚠️ |
| 语言 | JavaScript (Node.js) |
| 描述 | 模拟 Gemini CLI/Antigravity/Codex/Grok/Kiro 客户端请求, OpenAI-compatible |

**WebFetch URL:** `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/README.md`, `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/tls-sidecar/main.go`, `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/tls-sidecar/go.mod`

⚠️ **许可证风险: GPL-3.0** — 无法直接 vendor 代码到 HUAKAI (会触发 copyleft)。仅可将**架构和行为模式**作为参考, 不可复制代码。

### Go uTLS Sidecar 架构 (source: tls-sidecar/main.go + go.mod)

**go.mod 依赖:**
- Go 1.22
- `github.com/refraction-networking/utls v1.6.7`
- `golang.org/x/net v0.33.0`
- cloudflare/circl v1.5.0, brotli, compress (间接依赖)

**Sidecar 架构概念 (paraphrased behavior summary, GPL-3.0 source 不复制 verbatim)**:

- 单 process 网络隔层, 监听本机 loopback 端口 (默认 9090, env 可改)
- 两个 HTTP route: 健康检查 + 主代理 handler
- 主进程 (Node.js) 通过 HTTP header 传"目标 URL" + 可选"上游代理 URL" 给 sidecar; 其它 header 透传时去 hop-by-hop / proxy chain 痕迹 (反检测保护)
- TLS Profile: 用 uTLS auto-latest-Chrome 模板
- ALPN 协商支持 h2 + http/1.1 双协议
- 连接池按 host (h2) 和按 proxy URL (HTTP/1.1) 分别复用, mutex 保护
- ALPN 结果决定后续协议: h2 复用 connection; http/1.1 顺序连接

(具体 struct/map/function 命名见 GPL-3.0 source, HUAKAI 不引用. Clean-room: 行为模式可借鉴 + 名字自定. Source URL pinned: `justlovemaki/AIClient2API@a72a01e` (best-effort main HEAD 2026-05-16; main moves, codex implementer Phase 启动前需重 fetch 当前 commit SHA pin))

### Grok 集成方式

- Cookie/SSO token 认证 (浏览器提取)
- 多模态支持 (文本 / 图像 / 视频生成)
- Token 自动刷新机制
- **必须通过 TLS Sidecar** 绕过 Grok 403 (TLS 指纹检测)

### Kiro 集成方式

- 免费 Claude Opus 访问 (账号池模式)
- Extended thinking 支持, token budget 在 万-万十级
- 双格式兼容: Claude-native + OpenAI-compatible 双输出
- "Provider Fallback Chain" 配置概念: 主 provider 不可用时按链 fallback 别 provider (具体 JSON schema 见 GPL-3.0 source README, HUAKAI 不复制 verbatim config snippet; HUAKAI 自定义 fallback chain schema 时 paraphrase 风格 — 一对多 provider 列表 mapping)

**Clean-room note**: 此项目 GPL-3.0, 不可 vendor 代码; 仅借鉴"账号池 + thinking budget + 双格式输出 + fallback chain"行为概念, HUAKAI 实施需自定义 config schema (例避免直接抄 "providerFallbackChain" key 命名 + value structure).

### 版本演进情报 (HUAKAI 反封禁情报源)

| 时间 | 变更 |
|---|---|
| 2026-05 v3.0.0 | AI self-discovery endpoints `/api/help`/`/api/example` |
| 2026-04 | 图像生成/编辑 (OpenAI format 转换) |
| 2026-03 | Grok protocol + thinking models + 视频生成 |
| 2026-01 | Codex OAuth 支持 |

### HUAKAI 借鉴层映射

| HUAKAI 层 | 借鉴能力 (行为参考, 不复制代码) |
|---|---|
| **R-3 Rust sidecar** | uTLS sidecar 架构模式: localhost 代理 + header 透传 + ALPN 协商方案 |
| **L1 账号池** | Provider Pool Manager 多账号轮转 + 健康检查 + Fallback Chain 设计 |
| **L4 Grok 反封禁** | Grok 403 = TLS 指纹检测 → 必须 uTLS Chrome profile; Cookie 认证 + token 刷新 |
| **L4 Kiro 反封禁** | Kiro = Claude Opus 入口; extended thinking budget 参数透传 |
| **L6 主动对抗** | 过滤 CF-*/Via/X-Forwarded-* headers 的具体 header 名称列表 (行为参考) |

### 集成复杂度

**Medium (情报参考层)**

- 不能 vendor (GPL-3.0)
- sidecar 架构模式可用于 R-3 Rust sidecar 的 localhost 协议设计参考
- uTLS v1.6.7 的 `HelloChrome_Auto` 对应 HUAKAI Rust 中选择哪个 wreq profile 有参考价值

---

## 4. rebrowser-patches

### 基本信息

| 字段 | 值 |
|---|---|
| 仓库 | `rebrowser/rebrowser-patches` |
| Stars | 1,352 |
| 最近 push | 2025-05-09 (**超过 90 天**) |
| archived | false |
| License | 无明确 License 声明 ⚠️ |
| 语言 | JavaScript (patches for Puppeteer/Playwright) |

**WebFetch URL:** `https://raw.githubusercontent.com/rebrowser/rebrowser-patches/main/README.md`, `https://github.com/rebrowser/rebrowser-patches`

⚠️ **Recency 警告:** 最近 push 为 2025-05-09, 距今 2026-05-16 超过 12 个月, 已超过 90 天新鲜度阈值。项目可能停止维护或 Cloudflare/DataDome 已更新检测方案。

⚠️ **License 风险:** 无明确开源许可证 — HUAKAI 不可引用代码。

### Patch 列表 (完整)

**Patch 1: Runtime.Enable 泄露修复** (主要 patch)

- 目标: 阻止 Cloudflare/DataDome 通过 `Runtime.Enable` CDP 命令检测自动化
- 三种修复模式 (通过环境变量切换 `REBROWSER_PATCHES_RUNTIME_FIX_MODE`):
  - `addBinding` (默认): 在 main world 创建新 binding, 保留完整上下文访问
  - `alwaysIsolated`: 在隔离 world 执行所有脚本, 阻止页面脚本通过 MutationObserver 检测
  - `enableDisable`: 调用 Runtime.Enable 后立即 Disable, 捕获正确 context ID

**Patch 2: sourceURL 通用命名**

- 将 `//# sourceURL=pptr:...` 替换为 `app.js` 等通用名
- 环境变量: `REBROWSER_PATCHES_SOURCE_URL`

**Patch 3: Browser CDP 连接访问**

- 在 Browser class 添加 `_connection()` 方法 (直接访问 CDP session)

**Patch 4: Utility World 命名混淆**

- 将 `__puppeteer_utility_world__` + 版本号 替换为 `util` 或自定义值
- 环境变量: `REBROWSER_PATCHES_UTILITY_WORLD_NAME`

### 版本兼容性矩阵

| 库 | 最新测试版本 | 测试日期 |
|---|---|---|
| Puppeteer | 24.8.1 | 2025-05-06 |
| Playwright (Node.js) | 1.52.0 | 2025-04-17 |

注: Playwright patches 仅支持 Chrome; WebKit/Firefox 不支持。

### CDP 检测绕过机制 (HUAKAI L6 参考)

核心检测向量: 浏览器会在 `Runtime.Enable` CDP 命令执行时暴露给页面内 JavaScript, 反爬系统监听此事件。

绕过路径:
1. **不自动调用 Runtime.Enable** — 手动管理 execution context, 使用未知 ID
2. **随机化 context ID** — 在检测脚本执行前完成 context 创建
3. **三层降级**: addBinding → alwaysIsolated → enableDisable (按检测强度选择)

**对 HUAKAI 的价值:** HUAKAI 不使用 Puppeteer/Playwright, 但 CDP 检测原理直接对应 **L6 主动对抗层** 中 headless browser 检测绕过的设计。当 HUAKAI 需要实现 browser automation 功能 (如 Gemini Antigravity 登录自动化) 时, 这些 patch 是首选引用工具。

### HUAKAI 借鉴层映射

| HUAKAI 层 | 借鉴能力 |
|---|---|
| **L6 主动对抗** | CDP Runtime.Enable 检测原理 → headless 浏览器自动化场景的反检测设计参考 |
| **R-3 R8 模式** | sourceURL 混淆 + Utility World 命名混淆 → 浏览器自动化场景 JA3/CDP 双重指纹控制 |

### 集成复杂度

**Easy (参考层) / Hard (工具引用)**

- 无 License = 不能直接集成
- 项目已 12 个月无更新 = 有效期存疑
- 若 HUAKAI 需要 headless 自动化: 可考虑 `rebrowser-puppeteer` 包 (基于此 patch 的高层封装)
- 最佳用途: 将 CDP 检测原理转换为 HUAKAI L6 设计文档

---

## 5. finch

### 基本信息

| 字段 | 值 |
|---|---|
| 仓库 | `0x4D31/finch` |
| Stars | 294 |
| 最近 push | 2025-12-06 (**超过 90 天**) |
| archived | false |
| License | Apache-2.0 (主体) / FoxIO License 1.1 (JA4H 组件) ⚠️ |
| 语言 | Go |
| 依赖 | fingerproxy (Apache-2.0) |

**WebFetch URL:** `https://raw.githubusercontent.com/0x4D31/finch/main/README.md`, `https://api.github.com/repos/0x4D31/finch`, `https://github.com/0x4D31/finch/blob/main/README.md`

⚠️ **Recency 警告:** 最近 push 2025-12-06, 距今超过 5 个月 (90 天阈值)。可能处于低活跃维护状态。

⚠️ **License 混合:** 主体 Apache-2.0 可 vendor; 但 JA4H 指纹组件使用 FoxIO License 1.1, 该 license 有商业使用限制 — **JA4H 相关代码不可 vendor 到 HUAKAI**。

### 功能特性 (完整)

**核心动作 (HCL rule `action` 值):**

| Action | 描述 |
|---|---|
| `allow` | 放行合法请求 |
| `deny` | 阻断恶意流量 |
| `route` | 重定向到备用 upstream |
| `deceive` | 通过 Galah LLM 生成蜜罐响应 |
| `tarpit` | 故意慢速响应, 消耗扫描器资源 |

**指纹提取能力:**

- JA3 fingerprint (TLS ClientHello)
- JA4 fingerprint (TLS ClientHello, 增强版)
- JA4H fingerprint (HTTP 请求特征, FoxIO license)
- Akamai HTTP/2 fingerprint (SETTINGS 帧参数)
- Suricata HTTP 规则评估 (无需安装 Suricata)
- 实验性: HTTP/3 + QUIC fingerprinting

**其他:**

- SSE feed (实时可观测性)
- Admin API (热更新规则, authenticated)
- Echo mode (测试 + 数据集收集)
- 热重载: file watch + SIGHUP

### HCL 规则配置示例 (真实语法)

```hcl
# 按 JA3 阻断扫描器
rule "block-scanner-ja3" {
  action = "deny"
  when {
    tls_ja3 = ["3518c438fe56bd7dba3f6e28example"]
  }
}

# Tarpit: 多条件 OR
rule "tarpit-rule" {
  action = "tarpit"
  when any {
    tls_ja4 = ["q13d0312h3_55b375c5d22e_c183556c78e2"]
    http_ja4h = ["^ bad-ja4h"]  # ^ = prefix match
  }
}

# 按 User-Agent prefix 阻断
rule "block-bad-header" {
  action = "deny"
  when {
    http_header = {
      "user-agent" = ["^ EvilBot/"]
    }
  }
}
```

**条件匹配语法:** `^` = prefix, `=` = exact, `~` = regex; 支持 `tls_ja3`, `tls_ja4`, `http_ja4h`, `http_header`, `http_method`, `http_path`, IP 范围, Suricata alert ID。

### fingerproxy 关系

`finch` 依赖 `fingerproxy` 包 (Apache-2.0) 提供底层指纹提取能力。两个项目均由 0x4D31 维护。fingerproxy 可独立使用作为 Go library。

### 与 HUAKAI L6 主动对抗设计参考

finch 的设计方向与 HUAKAI 的视角**相反但互补**:

- **finch 是防守方**: 提取入站客户端指纹 → 识别自动化 → tarpit/deceive
- **HUAKAI 是进攻方**: 需要生成与真实浏览器一致的指纹 → 绕过 finch 类系统

finch 的 HCL 规则集直接告诉 HUAKAI: **哪些 JA3/JA4/H2 特征会触发防守方 deny/tarpit**。这是 L6 设计的反向参考。

`deceive` 模式 (LLM 蜜罐响应) 对 HUAKAI 的启示: 防守方可能返回看似正常的 LLM 响应来混淆爬虫, HUAKAI 需要检测此类 "honeypot 响应" 的能力 (L6 响应验证)。

### HUAKAI 借鉴层映射

| HUAKAI 层 | 借鉴能力 |
|---|---|
| **L6 主动对抗** | JA3/JA4 触发 deny 的指纹值 → HUAKAI 需要规避这些特征值 |
| **L6 响应验证** | `deceive` 模式 = LLM 蜜罐 → HUAKAI 需能识别非真实 upstream 响应 |
| **L5 H2 指纹** | Akamai H2 fingerprint 维度 → HUAKAI H2 SETTINGS 帧需要与真实浏览器一致 |
| **L6 tarpit 检测** | tarpit 响应特征 (故意慢速) → HUAKAI 需要连接延迟异常检测 |

### 集成复杂度

**Easy (设计参考层, 无需集成代码)**

主体 Apache-2.0 可 vendor, 但 HUAKAI 不需要直接运行 finch (finch 是防守方工具)。最佳用途:
1. 阅读 finch HCL rules 库 → 提取触发 deny 的 JA3/JA4 值 → 加入 HUAKAI 禁用指纹列表
2. fingerproxy 包可作为 HUAKAI 内部指纹验证工具 (Apache-2.0, Go)
3. `deceive`/`tarpit` 模式理解 → 写入 L6 响应验证 spec

---

## 综合对比矩阵

| 项目 | License | 活跃度 | HUAKAI 层 | 集成方式 | 复杂度 | 关键风险 |
|---|---|---|---|---|---|---|
| rquest/wreq | Apache-2.0 ✓ | 活跃 (2026-05) | L2/L3/L4/R-3 | 直接 Rust crate | Hard | rc API 未稳定; wreq-util profile 列表需本地读 |
| curl_cffi | MIT ✓ | 活跃 (2026-05) | L5/测试 | Python sidecar/CI oracle | Medium | Python 不进 Rust 数据面 |
| AIClient2API | GPL-3.0 ✗ | 极活跃 (2026-05) | L1/L4/L6/R-3 | 行为参考 (不 vendor) | Medium | GPL 禁止代码复用 |
| rebrowser-patches | 无 License ✗ | 停滞 (2025-05) | L6 参考 | 设计文档参考 | Easy | 无 license + 12 月无更新 |
| finch | Apache-2.0 ✓ (JA4H ✗) | 低 (2025-12) | L5/L6 设计 | 反向参考 (不直接运行) | Easy | JA4H 组件 FoxIO license 不可用 |

---

## 未完成项说明

- **rquest/wreq Chrome 版本枚举完整列表**: `wreq-util/src/` 目录 WebFetch 全部返回 404 (需本地 clone `0x676e67/wreq` 执行 `grep -r "Chrome" wreq-util/src/`)
- **curl_cffi 37 个 profile 完整列表**: 指向 curl-impersonate docs 外链, WebFetch 未 fetch 该文档
- **finch HCL example config 文件**: `config/example.hcl` raw URL 404, 已从 README 提取示例
- **rebrowser-patches patch 文件内容**: raw URL 404 (文件名格式不同)

---

## Source Files / WebFetch URLs Used

| URL | 内容 | 状态 |
|---|---|---|
| `https://raw.githubusercontent.com/penumbra-x/rquest/main/README.md` | rquest README | ✓ |
| `https://github.com/penumbra-x/rquest/blob/main/Cargo.toml` | Cargo 依赖 | ✓ |
| `https://github.com/penumbra-x/rquest/blob/main/src/tls.rs` | TlsOptions 结构体 | ✓ |
| `https://github.com/penumbra-x/rquest/blob/main/src/client/emulate.rs` | Emulation 结构体 | ✓ |
| `https://github.com/penumbra-x/rquest/tree/main/src` | src 目录列表 | ✓ |
| `https://github.com/penumbra-x/rquest/tree/main/src/tls` | tls 子目录 | ✓ |
| `https://api.github.com/repos/penumbra-x/rquest` | 仓库元数据 | ✓ |
| `https://github.com/0x676e67/wreq` | 上游 wreq README | ✓ |
| `https://api.github.com/repos/0x676e67/wreq` | 上游仓库元数据 | ✓ |
| `https://raw.githubusercontent.com/lexiforest/curl_cffi/main/README.md` | curl_cffi README | ✓ |
| `https://api.github.com/repos/lexiforest/curl_cffi` | 仓库元数据 | ✓ |
| `https://github.com/lexiforest/curl_cffi/blob/main/README.md` | curl_cffi README (rendered) | ✓ |
| `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/README.md` | AIClient2API README | ✓ |
| `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/tls-sidecar/main.go` | Go uTLS sidecar source | ✓ |
| `https://raw.githubusercontent.com/justlovemaki/AIClient2API/main/tls-sidecar/go.mod` | Go 依赖文件 | ✓ |
| `https://api.github.com/repos/justlovemaki/AIClient2API` | 仓库元数据 | ✓ |
| `https://raw.githubusercontent.com/rebrowser/rebrowser-patches/main/README.md` | rebrowser-patches README | ✓ |
| `https://github.com/rebrowser/rebrowser-patches` | README (rendered) | ✓ |
| `https://api.github.com/repos/rebrowser/rebrowser-patches` | 仓库元数据 | ✓ |
| `https://raw.githubusercontent.com/0x4D31/finch/main/README.md` | finch README | ✓ |
| `https://github.com/0x4D31/finch/blob/main/README.md` | finch README (rendered) | ✓ |
| `https://api.github.com/repos/0x4D31/finch` | 仓库元数据 | ✓ |
| `https://raw.githubusercontent.com/penumbra-x/wreq/main/wreq-util/Cargo.toml` | wreq-util Cargo (penumbra) | 404 |
| `https://github.com/0x676e67/wreq/blob/main/wreq-util/Cargo.toml` | wreq-util Cargo (upstream) | 404 |
| `https://github.com/penumbra-x/rquest/blob/main/src/tls/profile.rs` | TLS profile file | 404 (文件不存在) |
| `https://raw.githubusercontent.com/rebrowser/rebrowser-patches/main/src/rebrowser-puppeteer-core.patch` | patch 文件 | 404 |
| `https://raw.githubusercontent.com/0x4D31/finch/main/config/example.hcl` | HCL 配置示例 | 404 |

**执行者:** Claude Sonnet (agent a9b37320565166679)
**Fetch 完成时间:** 2026-05-16 UTC session 内
**Lane:** specifier (读取公开 README + source → 行为摘要; 未从任何项目复制代码)
