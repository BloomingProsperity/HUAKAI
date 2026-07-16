# 2026-07-16 Rust sidecar 三家 profile 与 Go 映射（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “R1:Rust sidecar profile 补 codex/gemini/kiro 三家 + Go 映射 · 中文注释/报告 · 禁commit” |
| Scope | 范围内：从本仓三份已抓取 JSON 真值逐字段搬运到 Rust 内置 profile；补 Go mode→profile ID 映射；增加 Rust/Go 判别测试；生成中文报告；运行指定 Rust/Go 门禁。范围外：重新抓包、修改 profile 解析器或字段、修改 `connect.rs`、给 antigravity/cursor/copilot/windsurf 臆造映射、增加依赖、commit/push。 |
| Success criteria | Rust 能加载 `openai-codex-cli-v1`、`gemini-cli-v1`、`kiro-cli-v1`；三份 profile 的指定数组、JA3、JA4、ALPN、host 与模板/Owner 真值一致；Go 三个 mode 映射成功且未支持 mode fail-closed；判别测试覆盖精确真值；指定门禁全绿或如实记录环境阻塞；报告完整且全中文。 |
| Time estimate | 墙钟约 45–90 分钟；单 agent 实施、核验与门禁约 60–120 分钟。Cargo 首次编译可能延长。 |
| Blast radius | Rust sidecar 内置 TLS ClientHello 配置与 Go sidecar 选路；错误数字、名称映射或 ALPN 行为会造成握手指纹失真、profile 加载失败或错误启用 sidecar。不会触及数据库、认证、计费、配额、部署或依赖。 |
| Failure modes | ① 数字列表/顺序搬错：用 JSON 结构化读取与精确断言双检；② OpenSSL cipher/group/sigalg 名映射错误：依照既有 anthropic 范式和库可接受名称核对，cargo test 验证；③ Gemini `force_h1` 改写 ALPN：不改连接逻辑，在报告明确冲突并交 Claude 决策；④ Kiro 随机扩展顺序无法复刻：固定模板样本并用中文注释声明固有差异；⑤ TOML 分段归属错误：每个 profile 明确结束空 `h2_settings` 后再开下一段并运行加载测试；⑥ 门禁受环境/超时影响：保留原始结果并在报告如实记录。 |
| Decision points | 实施前需 Claude/Codex 双计划对照、形成合成计划并由 Owner 批准。实施中若模板字段与 Owner 明示真值冲突、名称无法可靠映射、或必须修改 `connect.rs`/解析器/依赖才能通过，则停止对应高风险扩展并请 Owner/Claude 决定。Gemini ALPN 冲突已在现有代码中确认，本轮只报告不修。 |

## 前置假设与约束

- 三份 `tools/fingerprint-collector/templates/*.json` 是本仓自抓真值；本任务不读取或搬运任何第三方参考项目源码。
- 严格保留数字数组及顺序；不依靠记忆补值，不对缺失字段做推测。
- 代码注释、计划与最终报告使用中文；技术标识符保留英文。
- 不执行 commit 或 push，不修改 `LICENSE`，不增加运行时依赖。
- 不读取同描述符 Claude 计划，以保持本计划独立性。

## Pre-execution checklist

1. 等待 Claude/Codex 两份独立计划完成对照、合成与 Owner 批准。
2. 再次确认工作树状态，区分并保护 Owner/其他 agent 的并行改动。
3. 完整读取 `profile.rs`、三份 JSON、`registry.go`、profile ID 常量定义、现有测试与 `connect.rs` 的 ALPN/`force_h1` 路径。
4. 以脚本化只读提取或人工逐项对照形成三家字段核对表，确认 cipher 数量、signature algorithm 数量、Gemini 指定变体以及 Kiro 主样本。
5. 确认 OpenSSL cipher/group/sigalg 名称均能由现有 BoringSSL 路径接受；不修改解析器来掩盖错误。
6. 用 `apply_patch` 小步修改 Rust profile 与测试，随后修改 Go 常量、映射与测试。
7. 先运行定向测试，再运行完整指定门禁；任何失败先判断是真值错误、能力限制还是环境问题。
8. 编写 `rust_profiles_report.md`，逐 profile 记录字段、特殊约束、每个测试的变异转红点及门禁原始结论。
9. 检查 `git diff --check`、`git diff` 与 `git status`，确认无范围外改动、无 commit/push。

## 具体执行顺序

1. 从 JSON 精确提取 Codex、Gemini `tls_variants[0]`、Kiro 指定样本字段，建立临时核验清单，不写入产品文件。
2. 在 `BUILTIN_PROFILES_TOML` 追加三个完整 `[[profile]]`；Kiro profile 上方加入任务指定含义的中文限制注释。
3. 为三家分别增加 Rust 测试：精确比较完整 `expected_ja3`、三个 JA4 段、完整 `cipher_suites` 与完整 `alpn`，必要时同时断言 profile 可按 ID 查得。
4. 在既有 profile ID 常量处新增三个常量；扩展 `SidecarProfileForMode` 三个 case，保留其余 mode 默认失败。
5. 在现有 registry 测试文件增加表驱动测试：三个支持 mode 必须返回精确 ID/`true`；antigravity、cursor、copilot、windsurf 必须返回空值/`false`。
6. 运行格式化与定向测试；核对测试是否真正判别：任一 cipher 位、JA4 段或 Go case 的变异都会失败。
7. 按 Owner 给定环境运行 `cargo test -p tls-sidecar`、`go build ./...`、`go vet ./internal/transport/...`、`go test ./internal/transport/...`。
8. 输出中文报告，明确 Gemini 在当前 `force_h1=true` 下 ClientHello ALPN 会被收窄，不能保持 `ht` 广告；明确 Kiro 随机扩展顺序不可复刻；逐测试写变异转红说明。
9. 最终只汇报文件、原因、功能缩水、clean-room/安全风险、待确认项与下一步；停等 Claude 亲验，不提交。

