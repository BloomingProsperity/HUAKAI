# 2026-05-28 S1-025 Protocol-loss evidence持久化修复（Claude 计划）
| Owner directive | Implement audit fix S1-025: persist protocol-loss evidence through settlement/abort/cache-hit billing path |
| Scope | 修改 backend/internal/gateway/forwarder_types.go、backend/internal/gatewayhttp/chat_completions_billing.go、backend/internal/billing/billing.go、backend/internal/billing/settler.go，补充最小鉴别式测试 |
| Success criteria | 非空 protocol_loss 从适配器能力图传播到账单 InsertUsageRecordParams.ProtocolLoss；Abort 路径同样持久化；回归测试可捕获回退到 [] 的错误（mutation） |
| Time estimate | 45 分钟 |
| Blast radius | usage record 持久化审计字段，错误影响 SRE 与审计溯源 |
| Failure modes | 漏记字段导致数据库永远空数组；Abort 需新签名或复用 Draft 搬运，若签名改动不一致会触发编译/调用遗漏；测试不具备可判错能力 |
| Decision points | 是否在 billing.SettleRequest 增加独立 ProtocolLoss 载荷，还是依赖 Draft（取决于调用链是否全程含 Draft） |
| Pre-execution checklist | 1) 确认 freeze 包不新增文件 2) 确认数据库列和 sqlc 参数已就绪 3) 约束无新增依赖 4) 指定一处可重放的鉴别式测试 |
| Concrete execution order | 1) 建立 Draft/Request carrier 字段；2) 在 settlement 发起点序列化并赋值；3) settler 三处硬编码替换；4) 增加测试并跑 build/test/vet；5) 输出执行与偏差说明 |
