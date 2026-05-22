# 2026-05-22 W2 收尾对照 — L2 精确响应缓存键 vs 参照项目

> 补救波 W2(GW-01:跨协议缓存污染)闭合后,按 per-slice ref-recompare 纪律
> 对照参照项目同模块:查缺补漏 + 架构/算法/生态三维升级点。
> W2 实现提交 `8224d6c`。

## 对照范围

L2「精确响应缓存」的**物理键构造** —— 即一次请求如何映射到缓存条目,
哪些维度参与键、哪些被忽略。GW-01 的根因是协议族未参与键。

参照项目(均 archived=false,pushed_at 在 90 天内,2026-05-22 复核):

| 项目 | commit | 缓存键模块 |
|---|---|---|
| LiteLLM | `BerriAI/litellm@79b4578` | `litellm/caching/caching.py:276-356` |
| Portkey Gateway | `Portkey-AI/gateway@351692f` | `src/handlers/services/cacheService.ts:24-40` |
| llmgateway | `theopenco/llmgateway@1146e11` | `packages/cache/src/cache.ts:7-66`、`apps/gateway/src/chat/chat.ts:3599-3612` |

## 各项目缓存键维度

**LiteLLM**(`caching.py:276-321`):键由全部 LLM API 参数拼接后哈希;
model 维度经一层解析(`caching.py:339-356`),优先取 caching-group / model-group
再回落原始 model 名,以支持「跨模型组共享缓存」。**无显式端点族维度** ——
LiteLLM 内部把所有请求归一到 OpenAI 形态,不同端点(completion / embedding /
transcription)靠参数集天然不同而隐式区分,而非靠独立的端点轴。键带 namespace。

**Portkey Gateway**(`cacheService.ts:24-40`):有端点感知 —— 一个判定函数把
file / batch / finetune / imageEdit 类端点排除出可缓存集合;实际键计算委托给
宿主注入的回调,网关本体不持有键算法。

**llmgateway**(`cache.ts:7-12`、`chat.ts:3599-3612`):缓存载荷显式列举
provider + model + messages + 生成参数;流式走**独立键命名空间**(键加
`stream:` 前缀,`cache.ts:64-66`),分块存储后重放。

## 查缺补漏

| 维度 | 参照做法 | HUAKAI 现状 | 判定 |
|---|---|---|---|
| 协议/端点族入键 | Portkey 端点感知;llmgateway 带 provider 但单协议形态 | W2 已补 `EndpointFamily` 入 preimage | ✅ 已补,GW-01 闭合 |
| 流式 / 非流式分离 | llmgateway 用 `stream:` 前缀独立键 + 分块重放 | HUAKAI L2 **仅服务非流式**(`chat_completions_stream.go:34` 见 Stream 即早退),`stream` 字段从 canonical body 剥离 | ✅ 设计上无冲突——流式根本不进 L2,不存在串响应风险 |
| 端点可缓存性闸 | Portkey 显式排除 file/batch/finetune | HUAKAI L2 仅挂在 chat 链路,隐式闸住 | ⚠️ 当前安全;见 RR-W2-001 |
| 租户隔离入键 | llmgateway 键为纯 `sha256(payload)`,**无 project/tenant 维度** | HUAKAI `TenantID` 入 preimage | ✅ HUAKAI 更强(见升级点) |

**无 S0/S1 缺口。** GW-01 已闭合,无新增阻断项。

## 三维升级点(HUAKAI delta vs 参照)

- **架构升级 — 版本化键 schema**:HUAKAI 键前缀含 schema 版本(本波
  `l2:v1`→`l2:v2`),键结构一变旧条目自然失效、新旧不碰撞。LiteLLM 与
  llmgateway 的键均无版本位 —— 键结构升级会**静默命中陈旧条目**。
- **架构升级 — 租户绑定入键**:HUAKAI 把 `TenantID` 写进 preimage。
  llmgateway 的键是纯请求载荷哈希,两个 project 若请求体相同则**共享同一
  缓存条目**(跨租户响应复用)。HUAKAI 在键层面物理隔离。
- **架构升级 — 显式端点族轴**:本波新增的维度。llmgateway 带 provider 但
  其网关只接 OpenAI 形态;HUAKAI 同时承载 Anthropic / OpenAI / Gemini 三种
  协议族,端点族是真实必需轴,不是冗余。

三项均落「架构升级」维度:键的数据结构(版本位 / 租户位 / 端点族位)。

## 路线图候补(非阻断)

- **RR-W2-001**:端点可缓存性显式闸。当前 L2 仅挂 chat 链路、隐式安全;
  HUAKAI 后续接入 file / batch / 长任务类端点时,须显式排除其进 L2
  (对标 Portkey 的端点感知判定),否则可能错缓存一次性 / 有副作用响应。
- **RR-W2-002**:流式响应缓存(可选增强)。llmgateway 支持流式精确重放;
  HUAKAI 当前流式直接绕过 L2。精确流式重放命中率本就低,优先级低,
  仅在出现明确收益数据后再评估。

---
Lane:Claude 独立对照(读 MIT 参照源码 LiteLLM / Portkey,llmgateway;
clean-room paraphrase,无逐行翻译,无标识符 verbatim 复制)。
Source files read:litellm/caching/caching.py、portkey gateway cacheService.ts、
llmgateway cache.ts + chat.ts。
UTC:2026-05-22
