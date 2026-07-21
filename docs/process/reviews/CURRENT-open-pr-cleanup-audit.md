# 旧 PR 汇总与清理审计

> 状态：当前有效
> 核实日期：2026-07-21
> 主线基线：`origin/main@145329e72bfcfd038ebe8c236bbcc303790756ee`
> 唯一工作树：`/home/ubuntu/HUAKAI-wt-validated-fixes`
> 唯一分支：`fix/reverse-account-model-pull-closure-codex`

## 一、最终结论

当前 24 个旧开放 PR 不再分别合并。有效且主线尚未吸收的行为已经按当前架构独立实现到唯一工作树；过期、重复或高风险内容不进入新 PR。

| 分类 | PR | 结论 |
| --- | --- | --- |
| 主线已包含或新版完整替代 | `#254`、`#259` 至 `#275`（不含 `#255`） | 禁止再次合并旧提交；新 PR 建立后关闭。 |
| 有效残余已独立吸收 | `#255`、`#276`、`#277`、`#281`、`#282` | 当前工作树已有实现和判别测试；随本轮唯一新 PR 提交。 |
| 明确拒绝吸收 | `#278` | 迁移编号冲突且价格未核实，直接合并可能错账；关闭旧 PR，后续按独立核价目标处理。 |

GitHub PR 不能删除，只能关闭。旧 PR 的提交不作为合并来源，只保留为历史问题证据。

## 二、已吸收的有效残余

### `#255` 模块激活真实性

当前实现增加统一激活快照，区分：能力声明、对象构造、依赖注入、实际激活、多副本安全、运行后端、模式、流量比例、实时验证和逐协议入口覆盖。

- 数据合同：`backend/internal/moduleregistry/descriptor.go`
- 运维投影：`backend/internal/modulehttp/view.go`
- 生产接线：`backend/cmd/gateway/module_registry_wiring.go`
- OpenAPI：`docs/openapi/openapi.yaml`
- 判别测试：`backend/internal/modulehttp/handler_test.go`、`backend/cmd/gateway/module_routes_test.go`

矩阵只报告真码事实：队列等待和非流式响应缓存目前仅覆盖 Chat；结算失败恢复覆盖 Chat、Completions、Embeddings、Rerank、Images、Audio 和 Gemini，且只有 DLQ 处理器真实注册后才显示激活。未知探针不冒充已验证状态。

### `#276` 官方 Gemini 流式 `alt=sse`

官方 Gemini passthrough 在流式请求中追加并去重 `alt=sse`，API Key query 鉴权会保留该参数；非流式请求不追加。测试同时锁住 URL、鉴权参数和流式行为。

- 实现：`backend/internal/provider/gemini/passthrough.go`
- 测试：`backend/internal/provider/gemini/passthrough_test.go`

### `#277` Code Assist 客户端身份

删除暴露本产品名的伪造 User-Agent。按 2026-07-17 核实的官方客户端行为，使用版本、实际模型、运行平台、架构和终端形态组成身份；模型调用和项目初始化复用同一生成器，仍允许已确认的 profile 显式覆盖。

- 实现：`backend/internal/provider/gemini/code_assist.go`
- 复用入口：`backend/internal/provider/antigravity/project.go`
- 测试：`backend/internal/provider/gemini/code_assist_test.go`、`backend/internal/provider/antigravity/project_test.go`

### `#281` 三个真实缺口

1. Embeddings 与 Rerank 在响应已经交付、最终结算失败时写入持久恢复 DLQ；恢复载荷和生产注入与其他同步协议统一。
2. 统一 fallback 把上游毫秒级退避向上取整为秒，并投影为 `Retry-After`，不再固定为零。
3. 待支付订单上限和当日金额上限进入建单事务。PostgreSQL 以租户和用户维度的事务锁串行化，再在插入后事务内复核；超限事务回滚。真实 PostgreSQL 并发测试已证明六个并发请求只能提交一个。

相关实现位于：

- `backend/internal/embeddingshttp/`
- `backend/internal/rerankhttp/`
- `backend/internal/settlementrecovery/`
- `backend/internal/bindingfallback/executor/failure.go`
- `backend/internal/payment/order.go`
- `backend/internal/payment/store_postgres.go`
- `backend/internal/payment/store_postgres_tx.go`

真实 PostgreSQL 测试还发现并修复了锁参数先按文本解析导致 pgx 无法编码的问题；现先按 `bigint` 接收，再生成稳定锁键。

### `#282` Telegram 绑定时效

Telegram Widget 未显式配置时默认只接受 24 小时内的签名材料；`<=0` 不再等于永久有效。测试使用签名正确但已超过 25 小时的材料，确认请求被拒且不会进入绑定服务。

- 实现：`backend/internal/controlhttp/oauth_bindings_handler.go`
- 测试：`backend/internal/controlhttp/oauth_bindings_handler_test.go`

## 三、未吸收内容

### `#278` 定价迁移

该 PR 永久禁止整包合并：

1. 旧迁移 `0197/0198` 已被当前订阅套餐识别和服务器监测占用。
2. SQL 合并方向与“保留人工价格”的声明冲突。
3. 模型价格、计价单位、缓存、长上下文及图片/语音/视频差异未经逐项核价。

本轮不会重编号后偷渡旧 SQL，也不会用未经验证的价格填补功能表。后续定价目标必须具备官方来源、生效时间、运营加价分层、变更预览、人工保护价和账单重算测试。

## 四、验证状态

已通过：

- 模块激活、OpenAPI、Hermes 投影聚焦测试。
- Gemini passthrough、Code Assist、Antigravity 项目初始化聚焦测试。
- fallback、结算恢复、Embeddings、Rerank、Telegram、Payment 聚焦测试。
- PostgreSQL 迁移至 `0209` 后的支付并发上限和并发履约竞态测试，测试数据库即建即删。

全仓竞态、80 个 PostgreSQL 隔离包、迁移往返至 `0209`、Rust mimicry 工作区和质量门均已通过。仍需完成独立只读复审；通过后推送唯一新 PR，再关闭上述 24 个旧 PR。是否合并新 PR 由 Owner 决定。

## 五、清理规则

1. 新 PR 建立前不关闭仍用于核对的旧 PR。
2. 新 PR 建立且确认覆盖后，24 个旧 PR 全部关闭；`#278` 的关闭理由明确为“拒绝不安全实现”，不写成已吸收。
3. 不删除主线能力，不 cherry-pick 旧迁移，不恢复旧架构。
4. 本文件是旧 PR 汇总审计的唯一当前文档，后续只更新本文件。
