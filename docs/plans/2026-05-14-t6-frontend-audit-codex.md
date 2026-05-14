# 2026-05-14 T6 Frontend Audit Page
| Owner directive | "HUAKAI 信任链 T6 — 前端\"我的审计 / 我的消费链路\"页（Round 11 frontend slice）... 中文。不要问 Owner。" |
| Scope | 新增 `/audit` App Router 页面、audit 专用展示组件、audit API/mock 前端库；在侧边栏增加“审计”导航；生成 `/tmp/codex-t6-frontend-audit-final.txt`。不修改 `globals.css`、`dashboard/page.tsx`、现有 layout 组件主体行为、后端核心、schema、license、secrets。 |
| Success criteria | `GET /audit` 与 `GET /audit?request_id=mock1` 在 dev server 返回 HTTP 200；页面能展示 LedgerEntry、6 跳 HopChain、ModelChain 三方比对、Merkle proof、签名 verify 状态；每个新文件不超过 250 LoC；`npm run type-check` 与 `npm run build` 通过；最终报告包含 route、文件、检查证据和 Tailwind 命中列表。 |
| Time estimate | 约 60-90 分钟墙钟；单 Codex 执行。 |
| Blast radius | 前端 `/audit` 新路由与侧边栏高亮；若类型不匹配可能影响 Next build；若侧边栏 active 判断错误可能改变现有导航状态。 |
| Failure modes | 后端未启动导致 `/v1/audit/*` fetch 失败：使用 mock fallback 并在 UI 标注 partial；JSON shape 后续变更：类型集中放在 `frontend/lib/audit-api.ts` 降低扩散；Tailwind 未命中：用现有 token class 并通过 HTML/class 检查确认；每文件 LoC 超标：拆分组件职责并检查 `wc -l`。 |
| Decision points | 不涉及高风险文件；不新增 runtime dependency；不做浏览器内真实 ed25519 加密校验，因为 WebCrypto Ed25519 支持不稳定，本 slice 以链路 proof 字段完整性、root 对账和状态判定作为浏览器展示层 verify 状态，并保留 CLI 独立验签路径。 |
| Pre-execution checklist | 1. 写 `/tmp/codex-t6-frontend-audit.txt` stub。2. 读取现有 frontend layout/nav/API 模式。3. 读取 HUAKAI 内部 audit handler/types 对齐 JSON。4. 新增 audit lib/mock。5. 新增四个展示组件。6. 新增 `/audit` layout/page。7. 更新 sidebar nav active。8. 追加 `/tmp` 进度。9. 运行 type-check/build/dev server smoke。10. 写最终 `/tmp` 报告。 |
| Concrete execution order | API/mock types first → leaf UI components → page composition → sidebar nav → LoC/type/build checks → dev server HTTP smoke and Tailwind hit proof → final report. |
