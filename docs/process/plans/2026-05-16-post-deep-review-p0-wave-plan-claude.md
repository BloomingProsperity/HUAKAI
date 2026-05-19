# Post Deep-Review P0 Wave Plan (Claude PM Lane)

| Owner directive | "前端也不动 冻结" + deep review 5 P0/P1 — Owner 2026-05-16 |
| --- | --- |
| Scope | In: 后端 (Go + SQL + main.go + handler) + OpenAPI 同步. Out: frontend/ (全冻代码+依赖, Next.js 14.2.5 critical 接受), Rust core_gateway, LICENSE, billing 核心 |
| Success criteria | (a) `go test ./...` 无 FAIL; (b) pool dispatcher Stop() 不会被 shadow backlog 拖死 (有 drain timeout); (c) Provider Accounts 后端 path 改成 `/v1/admin/provider-accounts` 跟前端期望对齐 + create 不强要 tenant_id (从 admin ctx 取); (d) 缺 list/get/update/clear-rate-limit endpoint 补; (e) OpenAPI 跟后端 + 前端 align; (f) mimicry test PASS (anthropic-claude-code.json 移上层 OR registry 扫 _pending-backfill); (g) Email SMTP backend 接 sub2api 设计 + HUAKAI dev-mode + release gate |
| Time estimate | 3-5 hr 总 (3 codex lane parallel) |
| Blast radius | 中后端 (rename API path + 加 endpoint + Email infra); 0 frontend (冻); 0 production code 之外 doc |
| Failure modes | Path rename 前端冲突 (但前端冻就好); OpenAPI 漏 sync; tenant_id 从 ctx 取漏导致 cross-tenant; SMTP backend 启动 gate 误判致 dev 起不来; dispatcher Stop drain timeout 默认值太短致 production 丢 shadow job |
| Mitigations | Path rename 添加旧 path alias (临时双 path) 防紧急 rollback; OpenAPI 跟改在同 commit; tenant_id 从 session ctx 取 + AT 验证 cross-tenant 拒; SMTP gate 仅 production mode + 显式 env `HUAKAI_RELEASE_MODE=production`; drain timeout default 30s + env override |
| Decision points | Owner 已 approve sub2api email 设计; 其余 P0 修不需要 Owner 再 approve |
| Pre-execution checklist | (a) 当前 worktree status (3 codex result + 2 Claude draft + plan); (b) ≤3 codex (现 0 跑); (c) plan artifact 写 (本文件); (d) clean-room (codex 不读 ref); (e) test/build verify 后 commit |

## Concrete Execution Order

1. **Lane 1 codex (P0)**: pool dispatcher Stop() drain timeout fix
2. **Lane 2 codex (P0+P0)**: mimicry template fix + Provider Accounts 后端 rename + 字段对齐 + 补 endpoint + OpenAPI sync
3. **Lane 3 codex (P1)**: Email Sender SMTP backend (sub2api 设计 + HUAKAI dev-mode + release gate)
4. **Claude 平行**: synthesis F-PRIV + F-AUDIT spec → docs/specs/; verify AT matrix sync; commit spec wave (doc-only)
5. **等 3 fix lane done** → verify build/test
6. **Commit + push wave 2** (P0/P1 fix)
7. **Then wave 3 (P1 后续)**: Pools 字段真落库 / Voucher GetBatch 路由挂 / Channel Health list endpoint

## Clean-Room Guard

- Email lane: 可参考 sub2api `~/refs/sub2api/backend/internal/service/email_service.go` 行为 (specifier lane), 不抄 identifier verbatim, 不抄 sub2api SMTPConfig struct (paraphrase)
- 其他 lane: 不读上游 ref
- 全 lane: 改 production code 内 unsafe (复用 HUAKAI 已有模式)

## 中文摘要

Owner deep review 后, P0 跳一档 + frontend 冻结. 后端 + OpenAPI 主修. Plan = 3 codex lane (pool dispatcher / mimicry+provider-accounts / Email SMTP) + Claude PM synthesis spec wave doc-only. 3-5 hr 估. ≤3 codex 合规. Frontend 全 read-only 含 Next.js critical 不升 (Owner 显式 approve)。
