# 2026-06-23 relay body 有限读取去重

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文件要求处理 6+ relay handler 重复 `MaxBytesReader+io.ReadAll` 样板 |
| Scope | 在 `backend/internal/relaybody` 增加有限读取 helper，并替换 completions / embeddings / rerank / images / gemini / audio 的 JSON 或原始 body 读取样板；不改变各端点当前限额数值、不改变 JSON/multipart 解析、不接入新的 env 配置 |
| Success criteria | 公共 helper 覆盖正常读取与超限错误；调用点不再各自手写 `r.Body = http.MaxBytesReader(...); io.ReadAll(r.Body)`；端点现有错误响应语义保持原样；`git diff --check` 通过；若 Go 工具链可用则运行定向测试 |
| Time estimate | 约 25-35 分钟墙钟时间；单个 Codex 小补丁 |
| Blast radius | 6 个 relay 家族的请求体读取入口；若 helper 行为不等价，可能改变 oversized/读失败时的错误分类 |
| Failure modes | helper 不保留 `r.Body` 包装副作用；调用点误删仍需 `io` 的上游读取；audio multipart 的 413 特殊处理被误改 |
| Mitigation | helper 内部继续写回 `r.Body`；只替换读取样板，不替换 audio multipart 特殊错误分支；每个文件用 `rg` 核对剩余 `io.ReadAll(r.Body)` |
| Decision points | 本轮只去重读取动作；兄弟端点是否统一读取 `HUAKAI_MAX_REQUEST_BODY_MB` 属行为/容量策略变更，后续单独计划 |
| Pre-execution checklist | 1. 已读取目标 objective；2. 已核对目标 handler 的读取语义；3. 已确认另一个目标 plan 不读不改；4. 编辑前记录本计划；5. 编辑后跑可用检查 |

