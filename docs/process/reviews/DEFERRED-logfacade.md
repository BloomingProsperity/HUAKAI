# DEFERRED — logfacade(slog 门面统一片 D)

日期 2026-07-03,分支 `feat/slog-facade`。对抗审查(52 agent)后的刻意延后项。

## 1. "credential" 宽词误杀凭证子系统 error 文本(S3,fail-closed 刻意取舍,不改)

privacy 禁写标记表含 "credential" 宽词:credentialworker/credentialstore 等子系统的
error 文本(如 "credential decrypt failed")作为 slog attr 值过门面会被整值替换
`[REDACTED]`。这是 privacy 既有 fail-closed 政策的一致延伸(SanitizeError 对同类错误
也只回 "credential_error" 类别),方向正确但观测性有小损。

**后续片 H**:部分遮蔽(partial masking)——命中宽词时不整值替换,而是保留错误类别
骨架、只抹动态段(需在 privacy 内建带结构感知的遮蔽原语,属脱敏逻辑演进,单独成片)。

## 2. log 通道统一(A 项退回后的欠账)

setupSlogFacade 刻意退回了 slog.SetDefault 的隐式 log 包桥接(理由见 design §5)。
标准库 log 通道(http.Server ErrorLog 缺省、约 6 个 log.Printf 调用点)仍是
文本直写 stderr、不受 /loglevel 管辖、不过脱敏。**后续片**:先把这些调用点逐个迁到
slog(消息常量化、动态数据进 attr),迁完才能安全桥接 log→门面。

## 3. zap 与 slog 的 JSON 键名不对称(设计 §5 已记录)

zap 用 ts(epoch)/level/msg,slog 门面用 time(RFC3339)/level/msg;zap 侧也没有
service/env/version 常驻字段。统一需动 zap encoder 或 slog ReplaceAttr,留后续片 E。
