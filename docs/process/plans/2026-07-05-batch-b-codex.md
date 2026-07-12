# 2026-07-05 batch-b-codex

| Owner directive | "裁定落地批B(执行器翻默认开 + MO-3 补齐 + 前端字段同步;裁定全文先读 docs/process/plans/2026-07-03-audit-remediation-claude.md 末节)" |
| Scope | 仅处理 NT-2 告警评估执行器默认开启、MO-1 内容审核执行器默认开启、MO-3 配置故障 stale-if-error 与可观测性、MO-2 前端 moderation 字段同步。禁止触碰 Owner 列出的并行车道文件与目录,禁止 commit/push。 |
| Success criteria | unset/非法默认开启且显式 false/0 可关闭;内容审核未配置租户仍因租户配置默认关闭而放行;配置后端故障冷缓存可观测、热缓存过期后保留最后已知启用态并 fail-closed;前端不再声明或展示 violation_fee_usd;指定 Go 与前端门禁通过;每项行为改动有判别测试与变异证红记录。 |
| Time estimate | 约 1-2 小时墙钟,主要耗时在测试定位、变异回滚与前端 typecheck/vitest。 |
| Blast radius | 配置默认值、内容审核路由接线、moderation 配置缓存、前端 moderation 配置表单/列表。默认开启执行器可能增加空转 worker 或启用后真实执行,但租户级配置默认关闭用于控制行为面。 |
| Failure modes | 默认值误翻导致显式关闭失效;配置缓存 stale 误用导致未知租户 fail-closed;错误日志泄漏原始后端错误;前端字段删除不完整导致类型或组件编译失败;测试只覆盖 happy path。缓解:显式 false/0 测试、冷缓存与过期缓存双路径测试、日志只记录 error_class、全局 rg 字段残留、变异验证。 |
| Decision points | 无需新增 Owner 决策。本批不做 PG、schema、auth、billing、quota、notify、obs、gatewayhttp 改动;`.go` 注释不写借鉴项目名,只描述 HUAKAI 自身机制以满足 clean-room 规则。 |
| Pre-execution checklist | 1. 已读裁定计划末节。2. 复核目标文件与测试现状。3. 确认禁改文件/目录不在本批修改范围。4. 先补判别测试再改实现。5. 每个变异用 cp 备份还原。 |
| Concrete execution order | 1. 补 config 默认开启测试并改 `HUAKAI_ALERTING_EVAL_ENABLED`。2. 补 moderation runtime 默认开启测试并改路由注释/逻辑。3. 扩展 ttlLRU 过期读取能力,补 screener 冷缓存错误与 stale-if-error 测试,再改 loadConfig/可观测性。4. 删除前端 moderation 罚款字段类型、表单、展示与测试。5. 跑 Go/前端门禁。6. 逐项做变异证红并还原。 |
