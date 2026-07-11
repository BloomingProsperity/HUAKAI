# B 类根治:durable settlement intent(持久结算意图)— 设计 + Owner schema 决策 — 2026-07-11

对抗审 B 类 3 个 pre-existing 竞态(sweeper 竞态 S1-3 / claim 复活串账 S1-4 / 崩溃后陈旧重放 S1-5)的根治方案。**核心=首字节交付前落一条持久"结算意图"行,把"意图→交付→结算"绑定到 (request_id, attempt) 上。此项含 DB schema 迁移 = Owner 硬门,故 surface 未擅自迁移。**

## 三镜对照(§15/§16)
| 关注点 | sub2api | new-api | CLIProxyAPI | HUAKAI 取法 |
|---|---|---|---|---|
| 意图先行 | ✅ 异步图像作业表(created→settling→settled,先落行再冻结再回填终态,`migrations/159` + `batch_image.go:19-28`) | ⚠️ 仅订阅通道有持久幂等记录(`subscription.go:1213`);钱包侧内存会话,崩溃即过扣 | ❌ 无计费 | **学 sub2api 骨架**,裁剪成 pending→settling→settled/failed 少数态 |
| 幂等防串账 | ✅ `UNIQUE(request_id,api_key_id)` + 指纹一致校验,不符显式报冲突(`071_add_usage_billing_dedup.sql`) | ⚠️ 仅订阅 `request_id uniqueIndex` | ❌ | **学 sub2api**:唯一约束 + 指纹校验根治 S1-4/S1-5 |
| 终局出口 | ✅ 重试上限自动转终态,注释强调必须枚举全部失败码否则无限重试 | ⚠️ 仅清理任务 | ❌ | **学 sub2api** 自动耗尽 + **补 delta**:运维人工裁决出口 |
| **同步文本 intent-first** | ❌ 文本仍事后写 | ❌ | ❌ | **HUAKAI delta:三镜都没做,我们在 relay 主链路做=超越** |

## 表设计(新表 `settlement_intents`)
迁移前字段草案(最终以评审定稿为准):
- `id bigserial PK`
- `tenant_id bigint NOT NULL`
- `request_id text NOT NULL` / `logical_request_id text` / `attempt_seq int NOT NULL`
- `claim_id bigint NOT NULL` / `api_key_id bigint`
- `request_fingerprint text NOT NULL`(归一 payload hash,防串账)
- `status text NOT NULL DEFAULT 'pending'` CHECK ∈ {pending, delivering, settling, settled, aborted, failed}
- `predicted_cost numeric(20,8)` / `actual_cost numeric(20,8)` / `hold_id bigint`
- `first_byte_at timestamptz`(顺带闭合记忆里 settler 漏写 first_byte_at)
- `retry_count int DEFAULT 0` / `version int DEFAULT 0`(乐观锁)
- `created_at / updated_at / settled_at timestamptz`
- **`UNIQUE(request_id, attempt_seq)`** ← 根治串账/陈旧重放的核心约束

## 3 个 B 类缺陷怎么被根治
1. **S1-3 sweeper 竞态**:意图行在**首字节前**就落库(status=pending/delivering)。sweeper abort 前查意图行:存在未结算意图 → 不零成本 abort。窗口从"交付后才 enqueue 恢复行"提前到"首字节前已有意图行",彻底闭窗。
2. **S1-4 claim 复活串账**:结算证据绑 `(request_id, attempt_seq)` 唯一约束 + 指纹校验。旧意图行的 attempt_seq 与新 attempt 不同 → 物理隔离,旧恢复无法串到新 attempt 的三证。
3. **S1-5 崩溃后陈旧重放**:幂等重放证据绑 attempt_seq;claim 复活(新 attempt_seq)时旧意图行标 superseded,重放取当前 attempt 的行,不再返回旧响应。

## 集成点(不改金额口径/准入语义)
- Reserve 时(首字节前)插意图行 pending;forwarder 首字节时 UPDATE delivering + first_byte_at;settle 时 UPDATE settling→settled/failed。
- sweeper(`billing/settler.go` abortOnce)加意图行复查(与现有 post_delivery 复查合并)。
- 恢复 worker proof 改绑 attempt_seq(`settlementrecovery/postgres_proof.go`)。
- 补运维裁决出口(admin force-settle/关闭卡住意图行,复用 auth 黑洞车道/Hermes 运营台鉴权)。

## 迁移与 blast radius
- **schema 迁移**:新增 `settlement_intents` 表 + 索引(纯新增,不改现有表结构=低风险迁移;`sql/migrations/` 加一对 up/down;sqlc 生成 + 手改校验)。
- **blast radius**:relay 主链路每请求多一次 intent INSERT(首字节前)+ 2 次 UPDATE(交付/结算)。性能:单行索引写,毫秒级;可评估合并进现有 Tx1/Tx2 减少往返。
- **回滚**:down 迁移 drop 表;代码可 feature-flag 灰度(intent 写入失败降级到现有 post_delivery 恢复路径,不阻断请求)。

## Owner 决策点(schema 硬门)
1. **批准新增 `settlement_intents` 表 + 迁移**(纯新增表,低风险)。
2. intent 写入是否与现有 Reserve 的 Tx1 原子合并(减往返)vs 独立轻事务(隔离)。
3. 运维人工裁决出口(force-settle)属动钱 admin 操作,是否本切片一起做 vs 后续。

## 工时
约 2-4 天:迁移+sqlc(0.5d)/意图行生命周期接线(1d)/sweeper+proof 绑 attempt(1d)/运维出口(0.5-1d)/真 PG 故障注入并发测试(1d)。全程安全网(对抗审+零S0/S1+变异证+真 PG E2E)。
