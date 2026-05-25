# P1 Wave 2 Plan (Pools fields + Voucher GetBatch + Channel Health list)

| Owner directive | "推 P1 wave 2 补完 (Pools + Voucher + Channel Health 三块后端漏的补上)" — Owner 2026-05-17 |
| --- | --- |
| Scope | In: 3 后端 fix — (a) Pools create/update 字段真落库 (top_k_default / capability_default / allow_last_resort); (b) Voucher GetBatch 路由挂 (handler 已写); (c) Channel Health list/detail endpoint. OpenAPI 同步. Out: frontend (Owner 冻结), Rust core_gateway, LICENSE, billing 核心, F-PAY-001, 反封禁 impl |
| Success criteria | (a) Pools POST/PATCH 接受字段全落库; AT-POOL-001-001 (新) 验证落库 + retrieve match; (b) Voucher GetBatch endpoint 真 mount; AT-BILL-002-011 (新) 验证 admin GET batch detail; (c) Channel Health GET list (paginated) + GET {id} detail endpoint; AT-CH-002-014 / 015 (新) 验证 admin 可 see 状态; (d) OpenAPI 跟后端 align; (e) `go test ./... -count=1` 无 FAIL (新增 test PASS + 不引新 FAIL); (f) `go build ./...` PASS |
| Time estimate | 3-6 hr 总 (1 codex lane 顺序 — 3 task 都触 main.go + OpenAPI, 避 race) |
| Blast radius | 中后端 (Pools store field 加 + Voucher endpoint + Channel Health list); 0 frontend; 0 Rust; 0 migration (除非 Pools 需新字段 column, 若已存就不动 schema, 仅修 store layer 真存) |
| Failure modes | Pools schema 已有 column 但 store layer 漏 SET / SELECT → 加 SET + SELECT; OpenAPI 漏 sync 字段; Voucher GetBatch handler 已写但 chi route mount 漏 → 加 1 line route; Channel Health list 需 pagination + tenant scope; main.go race (上 commit a24b81b 改过) — 顺序 codex 不会 race |
| Mitigations | 1 codex lane (顺序做) 避免 race; 每 task 独立测试 + commit message 标 task A/B/C; 不引新 migration (若 Pools schema 没字段才考虑加 0026 migration, 否则跳过); OpenAPI 跟后端同 commit |
| Decision points | Stop if Pools 需新 migration (0026) — surface Owner approve; stop if Channel Health list 需复杂 query (cursor pagination 等), default page+limit OK |
| Pre-execution checklist | (a) ≤3 codex (现 0); (b) plan artifact 写 (本文件); (c) clean-room (codex 不读 ref); (d) 顺序 task A → B → C; (e) verify build/test 后 commit |

## Concrete Execution Order

1. **Task A** (1-2 hr): Pools create/update 字段真落库
   - Read backend/internal/pool/store.go or admin handler 锚 current SET / SELECT
   - 加 `top_k_default`, `capability_default`, `allow_last_resort` 到 store CREATE/UPDATE
   - 验 Pools schema 已有这些 column (若没 → surface Owner 不加 migration)
   - AT-POOL-001-001 (新) 测试: POST 加字段 → GET 验落库
2. **Task B** (0.5-1 hr): Voucher GetBatch 路由挂
   - Read backend/internal/gatewayhttp/voucher_handler.go 找 GetBatch handler
   - 找 admin voucher mount point (main.go or admin route file)
   - 加 `GET /v1/admin/vouchers/batches/{batch_id}` route
   - AT-BILL-002-011 (新) 测试: admin GET batch detail
3. **Task C** (1-2 hr): Channel Health list/detail endpoint
   - 当前只 pause/resume/force-active 控制, 缺 read
   - 加 `GET /v1/admin/channel-health` list (tenant-scoped + pagination page=&limit=)
   - 加 `GET /v1/admin/channel-health/{channel_id}` detail (含 audit_events 最近 N 条)
   - AT-CH-002-014 (list) + AT-CH-002-015 (detail with audit events)
4. OpenAPI 同步 (跟 task A/B/C 同 commit)
5. Verify: `go build ./...` + `go test ./internal/pool ./internal/voucher ./internal/channelhealth ./internal/gatewayhttp -race -count=1 -timeout 120s` PASS
6. Commit + push (1 commit cover 3 task)

## Clean-Room Guard

- Codex 不读上游 ref source
- 仅改后端 + OpenAPI; 不动 frontend / Rust / LICENSE / billing 核心 / spec wave
- migration 仅在 Pools 真缺字段时 (0026) 加, 否则跳过

## 中文摘要

P1 wave 2 plan: 3 块后端漏补全 (Pools 字段 / Voucher GetBatch route / Channel Health list+detail). 1 codex lane 顺序做避 race. 3-6 hr 估. ≤3 codex 合规. doc-only plan artifact, 后续 dispatch codex. Frontend 冻结不动.
