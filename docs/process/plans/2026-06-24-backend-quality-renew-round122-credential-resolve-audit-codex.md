# 2026-06-24 backend quality renew round122 credential resolve audit

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？”；继续 `/home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md` 中代码质量 renew，不触碰另一个目标。 |
| Scope | 仅核实并收敛凭据读取路径 `ResolveActive` 的审计错误纪律。实际源码显示线索不在 `internal/auditledger`，而在 `internal/credentialstore/postgres_store.go`：`ResolveActive` 成功解密后用 `_ = InsertAuditEvent` 写 `credential_resolved`。不修改 schema、不修改凭据选择 SQL、不修改认证核心、不改 money/quota/billing。 |
| Success criteria | 1. `ResolveActive` 的审计策略从隐式丢弃变成命名清楚的 helper；2. 测试覆盖“读取路径 best-effort，不因审计表错误熔断数据面”与“事件字段仍正确传入”；3. 写路径 `insertAuditEventStrict` 语义不变；4. 静态检查通过，可用 Go 检查如环境缺工具链则如实记录。 |
| Time estimate | 约 20-35 分钟。 |
| Blast radius | `credentialstore` 读取路径与对应单测。若误改为 fail-closed，可能导致审计表短暂故障时所有依赖 `PostgresCredentialVault` 的 relay 取凭据失败；本轮避免该行为变化。 |
| Failure modes | 1. 测试 fixture 需要真实可解密 payload，否则无法覆盖成功路径；缓解：在包内测试中直接用现有 `Cipher.Encrypt` 生成合法 envelope。2. 与已有未提交扫描函数重构冲突；缓解：只围绕审计 helper 和测试新增，不回退已有 diff。3. 注释误提参考项目；缓解：注释只写 HUAKAI 自身语义。 |
| Decision points | 本轮不把读取路径升级为 fail-closed，因为它是数据面取凭据路径；如 Owner 要求“所有凭据读取必须强审计”，需单独确认可用性影响后再改。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已确认 `ResolveActive` 真实位置在 `credentialstore`；3. 已确认 `ResolveActive` 被 `provider.PostgresCredentialVault` 数据面调用；4. 已确认 `credentialstore` 有既存未提交扫描重构，补丁不得覆盖；5. 编辑后运行静态检查与可用测试。 |

## 执行顺序

1. 在 `postgres_store.go` 中增加命名 helper，替换裸 `_ = s.InsertAuditEvent`。
2. 扩展 `credentialStoreDBStub` 以便测试捕获 `Exec`。
3. 新增 `ResolveActive` 成功路径测试：审计 `Exec` 返回错误时仍返回凭据，同时断言事件 SQL 与关键字段已尝试写入。
4. 运行 `git diff --check`、文本扫描、可用 Go 命令。
