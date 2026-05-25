# 2026-05-07 Bedrock EventStream 接入 — Claude 视角

| 字段 | 内容 |
|---|---|
| Lane | reviewer / synthesizer |
| 独立性声明 | **不是真正独立**：本文档写于 Codex plan 完成之后，已读 codex 文件。Claude 没有独立写出"零先验"的 plan，因此这是 review + 决策点表述，不是 mirror plan。后续 atomic 实施前的 cross-discuss 仍按 CLAUDE.md #10 走双 lane。 |
| 主参考 | [docs/process/plans/2026-05-07-bedrock-eventstream-codex.md](2026-05-07-bedrock-eventstream-codex.md) |

## 同意 Codex 的核心判断

1. **不要把 binary EventStream decoder 塞进 `proto/`** — `proto` 是 HCSF 语义协议层，不是 wire framing 层。两层拆分（`provider/bedrock/eventstream` + `proto/bedrock_eventstream`）干净。
2. **StreamForwarder 需要 scanner 抽象** — 当前 `bufio.Scanner + ScanSSEEvents` 硬编码 SSE 行为，binary 不能通过。registry-based scanner pattern 与现有 `ProtocolAdapterRegistry` 同形态，一致性好。
3. **clean-room 边界** — 不读 aws-sdk-go decoder 源；只用官方 EventStream wire-format 文档 + Bedrock streaming JSON 形态。test fixture 用自写 encoder，且加固定 hex fixture 防止 encoder/decoder 相互掩护。
4. **A1 抽象 SSE scanner 时不动行为** — 重构 → 测试不变 → 风险隔离。
5. **不复用 AnthropicAdapter 直接注册** — Bedrock 有 envelope / exception event / wire error，复用语义但不复用 adapter 实例。

## 我会调整的点（Claude review）

### R1：A8 顺序值得讨论
Codex 把 A8（请求体 OpenAI → Bedrock-native 转换）放最后，理由是"先做 reading 路径"。但客户端发请求时 body shape 必须先转，否则 Bedrock 直接 400 拒绝。读路径 A2-A7 可以用 staging/admin 提前注入 Bedrock-native body 测，但**真实 chat-completions 流量端到端不可用**，直到 A8 完成。

**建议**：要么把 A8 提前到 A6 之后（与 A7 并列），要么 A1 plan 阶段就明确"A1-A7 是 reading-only smoke，A8 才解锁端到端"。Owner 需要知道 ship 节奏。

### R2：A2 + A3 是否合并为一个 atomic
A2（decoder）和 A3（scanner 把 decoder 接到 forwarder）紧耦合。分开两个 atomic 意味着 A2 commit 后是死代码（没有调用方）—— codex review 会理直气壮说"unused"。

**建议**：A2 + A3 合并为一个 atomic（decoder + scanner 同时落地），A4（proto adapter）单独一个。这样每个 atomic 都自洽。

### R3：scanner registry 的并发语义
Codex 提到 `default scanner registry` 但没说线程安全要求。`ProtocolAdapterRegistry` 有"启动期 Register 完成后只读"的硬约定（`StaticProtocolAdapterRegistry` 注释明确）。

**建议**：scanner registry 同形态—启动期 MustRegister，运行期只读 For()。不加 RWMutex（保持纯函数 hot path）。

### R4：Bedrock exception frame 当作 body error 还是 transport error
Bedrock EventStream 有 `:event-type=exception` 帧，payload 是 JSON `{"errorCode":"...","message":"..."}`。两种处理：
- (a) 当 transport error → forwarder 直接关流 + ResponseEventTooLarge 类似的归一化错误
- (b) 当 protocol 内 error → translate 为 HCSF error event，客户端看到 SSE error event

**建议**：(b) 更合 R6 错误归一化精神。Codex plan 写"exception frame 转 typed scan error"暗示 (a)，需要 Owner 确认。

### R5：A8 实施位置
Codex 建议 A8 走 F-PROTO-002 canonical request path（不塞 provider passthrough）。我同意—canonical 路径正是 protocol-translation 的家。但这也意味着 A8 涉及 `proto/` 包重大改动 + 影响 OpenAI/Anthropic/Gemini 已有 canonical request path。**A8 自身就是 spec-level atomic**，可能需要单独 spec doc 立项。

## Owner 决策点（必须回答才动 A1）

1. **R1**：A8 提前 vs 维持 codex 顺序？
2. **R2**：A2 + A3 合并 vs 维持 codex 拆分？
3. **R4**：exception frame 当 transport error 还是 protocol error？
4. **scope**：A1-A7 是否一个 milestone？A8 是否同 slice / 同 PR？
5. **Anthropic-on-Bedrock 优先级**：首发只跑 Anthropic 模型，Llama / Cohere on Bedrock 后续？
6. **测试 fixture**：是否禁止任何形式（含测试用）的 aws-sdk-go binary import 检查？

## 风险（与 Codex 互补补充）

- 现有 `StreamForwarder` 测试覆盖率高，重构 scanner 抽象期间任何 forwarder 行为变更都会 cascade。**A1 必须 100% 保持现有 test 不变**（Codex 已写）。
- AWS EventStream CRC32 用 Polynomial 0xEDB88320（即 IEEE）。Go `hash/crc32` 默认是 Castagnoli。**必须显式 `crc32.MakeTable(crc32.IEEE)`**。这是常见踩坑。
- Anthropic-on-Bedrock chunk envelope 含 `:content-type=application/vnd.amazon.eventstream` + 内层 `bytes` 字段是 base64 of Anthropic event JSON。但有些 Anthropic event（如 `error`）形态与 vanilla Anthropic SSE 不完全一致 — proto adapter 得对照 Bedrock 真实示例验。

## 推荐执行顺序（修正版，待 Owner 拍板）

| atomic | 范围 | 工时估 |
|---|---|---|
| **A1** | StreamScanner 抽象 + SSE wrap，行为不变 | 3-4h |
| **A2+A3 合并** | EventStream wire decoder + Bedrock scanner（用 decoder） | 6-8h |
| **A4** | proto.BedrockEventStreamAdapter（语义层） | 4-5h |
| **A5+A6 合并** | registry wire-up + Stream bool intent | 2-3h |
| **A7** | 无 AWS e2e smoke（httptest fixture） | 3-4h |
| **A8** | 请求体 OpenAI → Bedrock-Anthropic native 转换（独立 spec atomic） | 8-12h |

总计 26-36h（清理 + 测试 + codex review 双 lane 多轮 included）。

## 不在范围

- ConverseStream（Bedrock 较新接口，比 InvokeModelWithResponseStream 表达力强但兼容性窄）
- Bedrock Knowledge Bases / Agents（产品级集成，非 streaming 本身）
- Bedrock provisioned throughput / cross-region inference（运维层，与 streaming 解耦）

## 等 Owner 回答后下一步

如果 Owner 选 R1 提前 A8：A1-A2+A3-A4-A8-A5+A6-A7 顺序
如果 Owner 选 codex 顺序：A1-A2+A3-A4-A5+A6-A7-A8 顺序（current default）

A1 启动前需要 Owner 至少回 R1 + R2 + scope。
