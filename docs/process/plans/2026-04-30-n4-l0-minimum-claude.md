# 2026-04-30 N+4 — L0 Minimum：api_keys + users schema + 退役 SmokeAuthResolver

| Field | Value |
| --- | --- |
| Owner directive | "B 你所有的决策都要和codex讨论" — pick option B (parallel codex plan first), and 所有决策强化平行讨论规则 |
| Trigger | Blueprint v0.2 §交付状态 §v0.2 待办 第 1 项："N+4 L0 minimum: 0009 schema (api_keys + users) + bcrypt + 退役 SmokeAuthResolver" — 是从 30% → 40% 的最直接路径 |
| Lane | implementation plan (Claude side of the parallel-draft pair) |
| Independence note | Codex 平行写他自己的 `2026-04-30-n4-l0-minimum-codex.md`；本文件不应被 Codex 阅读 |

---

## Scope (in)

### S1 — Schema migration 0009

新文件 `backend/sql/migrations/0009_api_keys_users.up.sql` + `.down.sql`：

- 新表 `users`:
  - `id bigserial PK`
  - `tenant_id bigint NOT NULL REFERENCES tenants(id)`
  - `email text NOT NULL`
  - `display_name text NOT NULL DEFAULT ''`
  - `status text NOT NULL CHECK (status IN ('active', 'disabled', 'deleted')) DEFAULT 'active'`
  - `created_at timestamptz NOT NULL DEFAULT now()`
  - `updated_at timestamptz NOT NULL DEFAULT now()`
  - `UNIQUE (tenant_id, email)`

- 新表 `api_keys`:
  - `id bigserial PK`
  - `tenant_id bigint NOT NULL REFERENCES tenants(id)`
  - `user_id bigint NOT NULL REFERENCES users(id)`
  - `name text NOT NULL` (operator-facing label, e.g. "personal-laptop")
  - `bearer_hash bytea NOT NULL` (bcrypt hash of bearer token; never store plaintext)
  - `bearer_prefix text NOT NULL` (first 8 chars + "..." for audit display only; never enough to authenticate)
  - `status text NOT NULL CHECK (status IN ('active', 'revoked', 'expired')) DEFAULT 'active'`
  - `revoked_at timestamptz`
  - `revoked_reason text`
  - `expires_at timestamptz` (nullable = no expiry)
  - `last_used_at timestamptz`
  - `created_at timestamptz NOT NULL DEFAULT now()`
  - `INDEX (tenant_id, status)`
  - `INDEX (bearer_prefix)` for audit lookup

- 既有 `billing_ledger_claims.api_key_id` / `usage_records.api_key_id` 列已存在但无 FK；本 migration **不加 FK**（因为既有测试种了 synthetic apiKeyID = tenantID*100+1，会报 FK 违反）。FK enforcement 留给 Slice 3 一并做。

### S2 — sqlc 查询

新文件 `backend/sql/queries/auth_inbound.sql`：
- `LookupAPIKeyByPrefix(ctx, prefix) → []APIKeyForBcrypt`（返回多行候选——bcrypt 比对在 Go 侧做；prefix 索引足够过滤大表）
- `MarkAPIKeyUsed(ctx, id) → execrows`（更新 `last_used_at = NOW()`；async fire-and-forget）
- `GetUserByID(ctx, tenantID, userID) → UserRow`（reader-side helper for admin UI later）
- `RevokeAPIKey(ctx, tenantID, id, reason) → execrows`

### S3 — `internal/auth/api_key_resolver.go`

新文件，取代 SmokeAuthResolver 在 production handler 中的位置：

```go
type APIKeyResolver struct { q *db.Queries }

func NewAPIKeyResolver(q *db.Queries) *APIKeyResolver

// Resolve 实现 ResolveInboundAuth 契约（CMB §6 contracts）。
// 解析 Bearer → 8字符 prefix → 候选行 → bcrypt 比较 → 返回 RequestContext。
// 失败：401 Unauthorized（不区分原因，避免泄露）；status='revoked'/'expired' 也返 401。
func (r *APIKeyResolver) Resolve(ctx, *http.Request) (RequestContext, error)
```

bcrypt cost: 默认 10（Go bcrypt.DefaultCost）；如未来发现速度问题，operator 可调。

CMB-5 防御：bearer plaintext 在 Resolve 调用栈中 lifetime 最短化；error 路径不能 log token 内容；只 log prefix。

### S4 — Handler 切换

`backend/internal/gatewayhttp/chat_completions_handler.go`：

- 把 `auth.SmokeAuthResolver` 引用改为 `auth.APIKeyResolver`（接口兼容，因为 Resolve 返回类型一样）
- SmokeAuthResolver 保留在 `internal/auth/smoke_resolver.go`，注释加 `Deprecated: replaced by APIKeyResolver in N+4 (2026-04-30); kept for offline tests until smoke_test.go migrates.`

### S5 — Smoke test 改造

`backend/cmd/gateway/smoke_test.go`：

- seed 步骤新增 INSERT users + api_keys 行（plaintext bearer 通过 bcrypt 哈希存）
- env 改为只设 `HUAKAI_DATABASE_URL`，**不再设** `HUAKAI_SMOKE_BEARER_TOKEN` 等
- 测试 POST 用真生成的 bearer

### S6 — main.go DI 更新

- 删 `auth.SmokeAuthResolver{...}` 构造
- 加 `auth.NewAPIKeyResolver(q)` 构造
- `deps.authSmoke` 字段重命名 `auth` 反映其非 smoke

### S7 — 配套测试

- `internal/auth/api_key_resolver_test.go`（//go:build integration_pg）：
  - happy path（bearer 匹配 → 返回 RequestContext）
  - 错误 bearer → 401
  - revoked api_key → 401
  - expired api_key → 401
  - cross-tenant probe（tenant A 的 bearer 不能登 tenant B 的）

---

## Out of scope (暂不在 N+4)

- API key issuance endpoint（admin 创建 key 的 admin API）→ N+4.1 或 L0-6 admin UI
- Tenant 注册 / 自助 signup → 留作 SaaS Edition Phase E
- bcrypt cost 自适应 → operator 默认即可
- Forgot-password / email verification → 远期
- Rate limiting per api_key → F-RATE-001 整体实现时一并
- API key rotation flow → 后续

---

## Success criteria

- [ ] 0009 migration 应用成功 + `\dt users api_keys` 出现
- [ ] sqlc 生成新 query 无错误
- [ ] APIKeyResolver 5 个集成测试 PASS
- [ ] Smoke test 改造后仍 5/5 PG state 断言绿（regression bar）
- [ ] go test 全包绿
- [ ] codex per-commit review 全部 HIGH 已处理

---

## Risk + mitigation

| Risk | Mitigation |
|---|---|
| bcrypt 慢导致 hot path latency 上升 | benchmark before/after handler；如 p99 增 >20ms 加内存 LRU 缓存 (api_key_id, last_verified_at) 5min TTL |
| api_key_id FK 缺失允许 dangling row | 不加 FK 是为了与既有 synthetic apiKeyID 测试兼容；Slice 3 schema 时一并补 + 数据修复 |
| Smoke test 改造破坏 Phase C.4 commit ce133da 的 smoke 断言 | 改造只动 seed + auth 注入，handler 路径不动；改造后立即跑 smoke 验证 |
| bcrypt cost=10 在低端开发机太慢 | DefaultCost 是 standard；如开发用大量并发会慢，可 env override |
| 0009 migration 在生产已有数据时不能 rollback | down.sql 先 DROP TABLE api_keys, users（CASCADE）；既有 billing_ledger_claims.api_key_id 不动（无 FK）|

---

## Decision points (向 Owner surface)

- **D1**: api_keys.bearer_hash 用 bcrypt（cost=10）vs argon2id？我推 bcrypt — Go 标准库直接有 + 多年验证 + cost 可调。argon2id 更现代但 Go 实现是 third-party。
- **D2**: bearer 生成格式？我推 `hk_<random32hex>`（"hk_" 前缀让 grep 凭证扫描可识别 + 32 hex = 128 bit entropy）。这影响公开 SDK 的可识别性。
- **D3**: api_key_id FK 现在加还是 Slice 3 加？我推 Slice 3（Slice 3 本身要加多列 + 重整数据，一起做迁移成本低）。
- **D4**: 0009 vs 0007/0008 编号？Slice 2 本来要 0007 (Model Registry)，Slice 3 本来 0008 (3-ID columns)，所以 L0 minimum 用 0009 数字逻辑上能接得上。但实际我们 L0 优先级高于 Slice 2/3——是否把 L0 改为 0007，把 Slice 2/3 顺移？我推保持 0009（顺序无关；编号只是文件名）。

---

## Concrete execution order

1. ✅ 写本 plan ← DONE
2. 等 Codex 平行 plan 落地 + Owner 选合并版
3. 0009 migration up + down + 在 docker dev PG apply
4. sqlc 生成
5. api_key_resolver.go + 5 集成测试 PASS
6. main.go DI 更新 → handler 切到新 resolver
7. smoke_test 改造 → smoke 5/5 绿
8. go test -p=1 ./... 全绿
9. codex per-commit review 处理 HIGH
10. commit + report Owner

---

## Per-commit cross-review

按 CLAUDE.md #8 + #10：每个有意义的 chunk（migration / resolver / handler / smoke）落地前 codex review；HIGH 处理完才 commit。

---

## Rollback plan

- migration down.sql DROP TABLE api_keys, users CASCADE
- handler 改回 SmokeAuthResolver（保留代码即可）
- smoke_test 改回 env-based seed
- main.go DI 改回 SmokeAuthResolver
- regression bar：smoke 仍要 5/5 绿
