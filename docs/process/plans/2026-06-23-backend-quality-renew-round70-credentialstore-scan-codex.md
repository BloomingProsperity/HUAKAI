# 2026-06-23 backend quality renew round70 credentialstore scan

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 仅审查 `backend/internal/credentialstore` 的代码质量、包纪律、重复扫描、测试判别力与运维恢复风险；不展开跨租户/密钥泄露等 security 专项。 |
| Success criteria | 读源码核实 `postgres_store.go` 体量与职责混杂；定位 `scanRecord` / 变体或等价重复扫描逻辑；核对 refresh/resolve/rotate 读取路径与测试是否能抓字段错位；输出带 `file:line` 的中文 findings。 |
| Time estimate | 约 30-45 分钟墙钟；1 个 Codex 审查轮次。 |
| Blast radius | 本轮以审查为主，计划文件为唯一预期写入；若发现低风险注释问题可小补丁，但不改 DB schema、凭据加密、认证核心或生产密钥。 |
| Failure modes | 误把纯安全问题展开为本专项；只凭文档不读源码；把 generated/sqlc 与手写 store 的字段顺序差异误判。缓解：逐段读 `.go` 真码与测试，安全发现只指针化。 |
| Decision points | 若需要修改 DB schema、凭据加密格式、认证核心或删除文件，必须先请 Owner 确认；本轮不会执行这些操作。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已确认 round70 计划文件不存在；3. 已量化 `credentialstore` 文件体量；4. 读取 store 实现与测试；5. 运行可用检查并如实记录工具链缺失。 |

