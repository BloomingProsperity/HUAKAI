# fingerprint-collector — 实现说明

## 结构选择与边界决策

### 包结构

| 包 | 职责 |
|----|------|
| `cmd/` | CLI 入口：flag 解析、主流程编排 |
| `internal/capture/` | pcap 接口封装（平台差异通过构建标签隔离） |
| `internal/tls/` | ClientHello 解析器、GREASE 检测、JA3、JA4 |
| `internal/mitm/` | 证书链完整性检查（严格依赖系统证书池） |
| `internal/output/` | JSON/TXT 输出序列化、SNI/IP/MAC 净化 |
| `internal/preflight/` | 交互式五条款确认清单 |

### 关键边界决策

**1. CGO / 跨平台编译**

`gopacket/pcap` 依赖 CGO + 平台原生 libpcap/npcap 头文件，无法从 Windows 直接交叉编译为 Linux 二进制。
解决方案：
- `pcap_linux.go`：构建标签 `linux && cgo`，使用 libpcap
- `pcap_windows.go`：构建标签 `windows && cgo`，使用 npcap
- `pcap_other.go`：构建标签 `(!linux && !windows) || !cgo`，提供存根实现，返回友好错误
- 结果：`CGO_ENABLED=0 GOOS=linux` 和 `GOOS=windows`（native）均编译成功

**2. HTTP/2 SETTINGS 不可捕获**

HTTP/2 SETTINGS 帧位于 TLS 记录层内部，被加密。被动 pcap 无法读取。
v1 在 `http2-settings.json` 中记录此限制说明，不提供虚假数据。
如需此数据，操作员须使用 SSLKEYLOGFILE + Wireshark 或自控透明代理。

**3. 信任来源：严格依赖系统证书池（Owner 2026-05-06 directive：选 C）**

最初实现含一个 `well_known_roots.go` 内置根 CA 列表作为系统池不可用时的
fallback，但其 hash 值是占位 demo 数据。Owner 评审时指出：假数据本身是
攻击面（被假 hash 误判为信任 = 蒙混 MITM），三选项中选 C — 删 fallback，
严格依赖 `x509.SystemCertPool()`。

当前行为：
- `cert_chain.go` 完全依赖 `x509.SystemCertPool()`
- 系统池加载失败 / 返回 nil → 立即返回 `ErrNoSystemCertPool`，调用方
  必须 fail-closed 退出
- 不再有任何"备用根列表"
- 部署运维要求：Linux 须装 `ca-certificates`；Windows / macOS 走原生
  信任库（schannel / Keychain）；docker 镜像须确保 ca-certificates 存在

**4. JA4 格式**

JA4 输出格式：`<proto><ver><sni><count><extcount>_<alpn>_<cipher_hash12>_<ext_hash12>`
共 3 个下划线、4 段。`_` 前的 ALPN 段来自规范的 "alpn_first_two_chars" 字段。

**5. SNI 净化**

`-include-sni` 未设置时，`clienthello-template.json` 中的 SNI 字段替换为 `<redacted>`。
默认行为遵循 README 中"可提交到私有模板库"的承诺。

**6. MITM 检测**

主动发起 TLS 握手（不依赖 pcap），检查：
- 叶证书 SAN/CN 是否匹配预期主机
- 完整链是否能被 `x509.SystemCertPool()` 验证

任一失败 → 打印醒目警告并退出，除非 `-disable-mitm-detection` 明确指定。
系统池不可加载 → 返回 `ErrNoSystemCertPool`，调用方 fail-closed。

---

## 读取的源文件

- 无读取任何外部参考项目（sub2api / new-api / openSSL 等）的源代码
- JA3 算法参考：https://github.com/salesforce/ja3 公开规范文档（仅算法描述）
- JA4 算法参考：https://github.com/FoxIO-LLC/ja4 公开规范文档（仅算法描述）
- RFC 8446 §4.1.2（ClientHello 结构定义）
- RFC 8701（GREASE 值定义）

## Lane

specifier（读取公开规范摘要 → 行为描述；未读取任何参考项目源代码）

## Agent

claude executor（claude-sonnet-4-6 模型）

## UTC 时间戳

2026-05-06T09:00:00Z（实现完成时间）
