# 2026-05-22 W3 收尾对照 — 公开错误安全模型 vs 参照项目

> 补救波 W3(W3a 公开错误面 GW-02/04/05/06/09 + W3b 流式/eventbus C-12/C-18/B-11)
> 闭合后,按 per-slice ref-recompare 纪律对照参照项目同模块:查缺补漏 + 三维升级。
> W3 实现:`b9f5beb`/`eda7b3c`/`c73356b`(W3a)、`47d357c`/`75c362e`(W3b)。

## 对照范围

「网关把错误返回给客户端」这条面:客户端拿到的 JSON / header / SSE 错误帧里
有没有内部 `err.Error()`(SQL 表名、上游 body、账号线索)。这正是 W3 修的口子。

参照项目(均 archived=false,pushed_at 在 90 天内,2026-05-22 复核):

| 项目 | commit/snapshot | 错误处理模块 |
|---|---|---|
| LiteLLM | `BerriAI/litellm@79b4578` | `litellm/proxy/common_request_processing.py` |
| Portkey Gateway | `Portkey-AI/gateway@351692f` | `src/handlers/handlerUtils.ts`、`src/errors/GatewayError.ts` |
| CLIProxyAPI | `router-for-me/CLIProxyAPI`(快照 ~`21fad9db`,2026-05-22 仍活跃) | `internal/api/server.go`、`internal/interfaces/error_message.go` |

## 各项目错误返回客户端的做法

**LiteLLM**(`common_request_processing.py:1814-1849`):异常经
`_handle_llm_api_exception` 包成 `ProxyException`,而 message 直接拼 `str(e)`
(`raw_detail = getattr(e, "detail", str(e))`、`error_msg = f"{str(e)}"`、
`f"Invalid request format: {error_msg}"`)。**原始异常字符串直接进客户端 message。**
流式路径同样有 `StreamErrorSerializer(ProxyException -> SSE error frame)`
(`:56-57`)—— 即 SSE 错误帧也带 raw 文案。等于 HUAKAI W3 之前 GW-04 的形态。

**Portkey Gateway**(`handlerUtils.ts:806-819`):做了**真的 public/internal 切分**。
`GatewayError`(`GatewayError.ts`)持 `message`(公开)+ `cause`(内部)。
未处理异常 → 客户端只拿固定文案 `'Something went wrong'`,
`error.message/cause/stack` 进 `console.error`(内部日志);已处理的
`GatewayError` → 客户端拿 curated `error.message`。**Portkey 是正面例子。**

**CLIProxyAPI**(`server.go:1046`、`:1057`):`Message: errGet.Error()`、
`Message: errDecode.Error()` —— 原始 `.Error()` 直接进响应 Message;
`redis_queue_protocol.go:72/282` 把 `"ERR "+err.Error()` 写进 wire 协议错误。
**原始错误泄露给客户端**,同 GW-04 反模式。

## 查缺补漏

| 维度 | 参照做法 | HUAKAI W3 现状 | 判定 |
|---|---|---|---|
| public/internal 错误切分 | Portkey 有(message/cause + 未处理异常给固定文案);LiteLLM / CLIProxyAPI 无 | clienterr 目录(code→固定文案)+ LogInternal;所有 call site 禁 `.Error()` | ✅ 已补,GW-02/04 闭合 |
| HTTP header 错误脱敏 | 三家都未见对诊断 header 的脱敏处理 | X-Huakai-* header 只放稳定 code | ✅ HUAKAI 更全(见升级点) |
| 流式错误帧脱敏 | LiteLLM SSE error frame 带 raw 文案;Portkey/CLIProxyAPI 未见显式脱敏 | C-18:protocol error 帧改 canonical 脱敏帧 | ✅ 已补 |
| 扫描/IO 错误分类 | 未见参照项目把 SSE 扫描错误细分(overflow vs 网络) | C-12:只有真 overflow 映射 overflow,网络错误单独分类 | ✅ HUAKAI 更精细 |
| DLQ 持久化失败可见 | 参照项目无等价 async-handler DLQ | B-11:计数器 + 导出 accessor + state 标记 | ✅ HUAKAI 原创(见升级点) |

**无 S0/S1 缺口。** W3 八项发现全部闭合,无新增阻断项。

## 三维升级点(HUAKAI delta vs 参照)

- **架构升级 — 集中式编码错误目录**:Portkey 的 message 是**分散**在各
  `GatewayError` 抛出点的;HUAKAI 用一个 `internal/clienterr` 目录把
  稳定 `code`→固定文案集中,客户端可机读 `code` 分支(Portkey 未处理异常
  只给一句 `'Something went wrong'`,不可机读)。
- **架构升级 — 结构性强制**:HUAKAI 加了**静态测试**禁止任何 public error
  call site 再出现 `.Error()` —— 不是靠人记得脱敏,而是回归测试机械卡死。
  三家参照都没有这层 enforcement(所以 LiteLLM / CLIProxyAPI 才会漏)。
- **架构升级 — 三面覆盖**:HUAKAI 同时脱敏 JSON body + HTTP header + SSE
  错误帧三条出口;参照项目只(部分)处理 JSON body 这一条。
- **算法升级 — 扫描错误判别分类**:C-12 把 SSE 扫描错误细分为
  真 overflow / context 取消 / 网络读错误三类各自映射,修正重试与
  channel-health 信号;参照项目把扫描层错误粗粒度处理。
- **生态升级 — DLQ 失败可观测**:B-11 让 DLQ 持久化失败可见(计数器 +
  导出 accessor + handler state 标 `dlq_persist_failed`)。参照项目无等价
  async-handler DLQ,更无"DLQ 写失败也要被运维看到"这层。

## 路线图候补(非阻断)

- **RR-W3-001**:`clienterr.LogInternal` 走 `slog.Default()`(stderr),与本进程
  zap 主管道不统一。可选:slog→zap 桥接。优先级低。
- **RR-W3-002**:eventbus DLQ 失败目前只做到"可见"(日志+计数器+state),
  未接真正的告警系统。待 metrics/alerting 基建就绪后接线。

---
Lane:Claude 独立对照(读 MIT 参照源码 LiteLLM / Portkey / CLIProxyAPI;
clean-room paraphrase,无逐行翻译,无标识符 verbatim 复制)。
Source files read:litellm/proxy/common_request_processing.py、
portkey handlerUtils.ts + GatewayError.ts、CLIProxyAPI server.go +
error_message.go + redis_queue_protocol.go。
UTC:2026-05-22
