-- auth 降级车道的持久化查询。表 provider_account_auth_cooldowns 是车道唯一真相;
-- 进程内镜像只承担选号门的本地即时性与写失败兜底。所有状态转换都在单条语句内原子完成,
-- 多副本并发写同一账号时由行锁串行化,窗口内的重复失败不产生写入。

-- name: RecordAuthFailure :one
-- 记录一次鉴权失败。语义与进程内车道逐条对齐:
--   * 窗口去抖:now 未越过 auth_until 时不升级、不改截止,也不写行(DO UPDATE 的 WHERE 为假,
--     调用方收到 no rows 即表示"窗口内,状态未变");
--   * 版本前进:调用方凭据版本高于行内版本 → strike 重置为 1 并按本次失败重新判定;硬禁不因此解除;
--   * 迟到的旧版本:调用方版本低于行内版本(双方都已知)→ 这次失败属于已轮换掉的凭据,不升级、不写行;
--   * 封顶指数退避:auth_until = now + min(cap, base * 2^(strike-1));
--   * 硬禁:仅铁证类且 strike 达上限;歧义类永不硬禁;已硬禁的行不再改写(no rows);
--   * 版本守卫:无行时若账号已有更高版本凭据,本次失败属于旧凭据,不建行(no rows)。
-- 首次失败插入时租户来自账号真相行;账号不存在则插入失败,调用方按持久化失败处理。
INSERT INTO provider_account_auth_cooldowns AS t (
    provider_account_id,
    tenant_id,
    strike,
    auth_until,
    hard_disabled,
    credential_version,
    last_failure_class,
    last_escalated_at,
    hard_disabled_at,
    created_at,
    updated_at
)
SELECT
    sqlc.arg(provider_account_id)::bigint,
    pa.tenant_id,
    1,
    sqlc.arg(now)::timestamptz + make_interval(secs => LEAST(sqlc.arg(cap_seconds)::float8, sqlc.arg(base_seconds)::float8)),
    (sqlc.arg(iron_clad)::boolean AND 1 >= sqlc.arg(hard_disable_strikes)::int),
    GREATEST(sqlc.arg(credential_version)::int, 0),
    sqlc.arg(failure_class)::text,
    sqlc.arg(now)::timestamptz,
    CASE WHEN (sqlc.arg(iron_clad)::boolean AND 1 >= sqlc.arg(hard_disable_strikes)::int)
         THEN sqlc.arg(now)::timestamptz END,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz
FROM provider_accounts pa
WHERE pa.id = sqlc.arg(provider_account_id)::bigint
  -- 无行时同样做版本守卫:显式轮换已删行后,在途旧版本的失败不得给新凭据建退避窗。
  AND (
      sqlc.arg(credential_version)::int <= 0
      OR NOT EXISTS (
          SELECT 1 FROM account_credentials ac
          WHERE ac.provider_account_id = pa.id
            AND ac.deleted_at IS NULL
            AND ac.credential_version > sqlc.arg(credential_version)::int
      )
  )
ON CONFLICT (provider_account_id) DO UPDATE SET
    strike = CASE
        WHEN (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
              AND sqlc.arg(credential_version)::int > t.credential_version)
            THEN 1
        ELSE t.strike + 1
    END,
    auth_until = sqlc.arg(now)::timestamptz + make_interval(secs => LEAST(
        sqlc.arg(cap_seconds)::float8,
        sqlc.arg(base_seconds)::float8 * power(2::float8, LEAST(
            CASE
                WHEN (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
                      AND sqlc.arg(credential_version)::int > t.credential_version)
                    THEN 0
                ELSE t.strike
            END,
            40)::float8)
    )),
    -- 硬禁只增不减:版本前进只重置 strike/软退避,不解除硬禁(硬禁只由运营 resume / 显式轮换删除行解除;
    -- 刷新成功抬升的版本不得借一次失败把硬禁降回软退避)。
    hard_disabled = CASE
        WHEN (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
              AND sqlc.arg(credential_version)::int > t.credential_version)
            THEN (t.hard_disabled OR (sqlc.arg(iron_clad)::boolean AND 1 >= sqlc.arg(hard_disable_strikes)::int))
        ELSE (t.hard_disabled OR (sqlc.arg(iron_clad)::boolean AND t.strike + 1 >= sqlc.arg(hard_disable_strikes)::int))
    END,
    hard_disabled_at = CASE
        WHEN (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
              AND sqlc.arg(credential_version)::int > t.credential_version)
            THEN CASE WHEN (t.hard_disabled OR (sqlc.arg(iron_clad)::boolean AND 1 >= sqlc.arg(hard_disable_strikes)::int))
                      THEN COALESCE(t.hard_disabled_at, sqlc.arg(now)::timestamptz) END
        WHEN (t.hard_disabled OR (sqlc.arg(iron_clad)::boolean AND t.strike + 1 >= sqlc.arg(hard_disable_strikes)::int))
            THEN COALESCE(t.hard_disabled_at, sqlc.arg(now)::timestamptz)
        ELSE NULL
    END,
    -- 版本只前进不后退:迟到的旧版本事件不得把行退回旧版本,否则下一次新版本失败会被误判为轮换。
    credential_version = GREATEST(t.credential_version, sqlc.arg(credential_version)::int, 0),
    last_failure_class = sqlc.arg(failure_class)::text,
    last_escalated_at = sqlc.arg(now)::timestamptz,
    updated_at = sqlc.arg(now)::timestamptz
WHERE NOT (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
           AND sqlc.arg(credential_version)::int < t.credential_version)
  -- 行内版本未知(如 resume 墓碑)时,同样按账号凭据表判定本次失败是否属于旧凭据。
  AND (
      t.credential_version > 0
      OR sqlc.arg(credential_version)::int <= 0
      OR NOT EXISTS (
          SELECT 1 FROM account_credentials ac
          WHERE ac.provider_account_id = t.provider_account_id
            AND ac.deleted_at IS NULL
            AND ac.credential_version > sqlc.arg(credential_version)::int
      )
  )
  -- 已硬禁的行不再因请求失败改写:硬禁只增不减,strike/截止对选号已无意义,401 风暴下不产生写入。
  AND NOT t.hard_disabled
  AND (
      t.auth_until IS NULL
      OR sqlc.arg(now)::timestamptz > t.auth_until
      OR (sqlc.arg(credential_version)::int > 0 AND t.credential_version > 0
          AND sqlc.arg(credential_version)::int > t.credential_version)
  )
RETURNING
    t.strike,
    t.auth_until,
    t.hard_disabled,
    t.credential_version,
    t.hard_disabled_at,
    COALESCE(t.hard_disabled_at = sqlc.arg(now)::timestamptz, false)::boolean AS newly_hard_disabled;

-- name: LockCurrentCredentialVersion :one
-- 刷新永久失效硬禁前,在同一事务内锁住账号当前最高版本的凭据行并读取其版本:
-- 显式轮换(Rotate)会 UPDATE 同一行,二者由行锁串行——轮换先提交则这里读到新版本、调用方放弃硬禁;
-- 这里先拿到锁则轮换等待,提交后由轮换事务内的删除清掉刚写下的硬禁。无凭据行时返回 no rows(视为版本 0)。
-- 前提:账号的活跃凭据 1:1(最高版本行即被轮换的行);多凭据行账号只与最高版本行串行。
SELECT ac.credential_version
FROM account_credentials ac
WHERE ac.provider_account_id = sqlc.arg(provider_account_id)::bigint
  AND ac.deleted_at IS NULL
ORDER BY ac.credential_version DESC
LIMIT 1
FOR UPDATE;

-- name: MarkAuthCooldownHardDisabled :one
-- 即时硬禁(凭据刷新拿到永久失效,或副本补写本地硬禁;failure_class 记录来源类别),strike 与软退避截止保持不变。版本守卫由调用方在同一事务内
-- 基于 LockCurrentCredentialVersion 的结果判定(账号已轮换出更高版本 → 不调用本语句)。
-- evidence_at 是证据产生的时刻:直写为 now;副本补写本地硬禁时为该硬禁当初进入的时刻。运营 resume 会
-- 在行上留下 resumed_at 墓碑,早于墓碑的证据(补写旧本地状态)不得写回(no rows),晚于墓碑的新证据照常
-- 生效——resume 之后新到达的永久失效应重新硬禁。已硬禁的行只刷新时间戳,newly 为 false。
INSERT INTO provider_account_auth_cooldowns AS t (
    provider_account_id,
    tenant_id,
    strike,
    auth_until,
    hard_disabled,
    credential_version,
    last_failure_class,
    hard_disabled_at,
    created_at,
    updated_at
)
SELECT
    sqlc.arg(provider_account_id)::bigint,
    pa.tenant_id,
    0,
    NULL,
    true,
    GREATEST(sqlc.arg(observed_credential_version)::int, 0),
    sqlc.arg(failure_class)::text,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz,
    sqlc.arg(now)::timestamptz
FROM provider_accounts pa
WHERE pa.id = sqlc.arg(provider_account_id)::bigint
ON CONFLICT (provider_account_id) DO UPDATE SET
    hard_disabled = true,
    hard_disabled_at = COALESCE(t.hard_disabled_at, sqlc.arg(now)::timestamptz),
    credential_version = GREATEST(t.credential_version, sqlc.arg(observed_credential_version)::int, 0),
    last_failure_class = sqlc.arg(failure_class)::text,
    updated_at = sqlc.arg(now)::timestamptz
WHERE t.resumed_at IS NULL OR t.resumed_at < sqlc.arg(evidence_at)::timestamptz
RETURNING
    t.strike,
    t.auth_until,
    t.credential_version,
    t.hard_disabled_at,
    COALESCE(t.hard_disabled_at = sqlc.arg(now)::timestamptz, false)::boolean AS newly_hard_disabled;

-- name: ClearAuthCooldownSoft :execrows
-- 成功请求:只清软退避,不解除硬禁——硬禁只能由运营 resume 或显式轮换解除,否则在途请求的迟到成功会
-- 复活刚被确认永久失效的账号。没有 resume 墓碑的软退避行直接删除(表只保留仍有状态或带墓碑的行)。
DELETE FROM provider_account_auth_cooldowns
WHERE provider_account_id = sqlc.arg(provider_account_id)::bigint
  AND NOT hard_disabled
  AND resumed_at IS NULL
  AND (strike > 0 OR auth_until IS NOT NULL);

-- name: ClearAuthCooldownSoftKeepTombstone :execrows
-- 成功请求(带 resume 墓碑的行):清空软退避但保留墓碑,墓碑继续拒绝早于它的本地硬禁补写。
UPDATE provider_account_auth_cooldowns
SET strike = 0,
    auth_until = NULL,
    updated_at = sqlc.arg(now)::timestamptz
WHERE provider_account_id = sqlc.arg(provider_account_id)::bigint
  AND NOT hard_disabled
  AND resumed_at IS NOT NULL
  AND (strike > 0 OR auth_until IS NOT NULL);

-- name: ClearAuthCooldown :one
-- 运营 resume / ForceActive:彻底解除车道状态(strike、截止与硬禁),并在行上留下 resumed_at 墓碑:
-- 其他副本因写库失败仍持有的旧本地硬禁,补写时会被墓碑拒绝,不会撤销这次人工恢复;墓碑之后新到达的
-- 失败证据照常生效。行不存在时也建墓碑(账号真相行不存在则 no rows)。had_state 报告此前是否确有
-- 生效中的状态(硬禁 / strike / 截止),供日志区分"真的恢复了什么"与"重复 resume"。
WITH before AS (
    SELECT (hard_disabled OR strike > 0 OR auth_until IS NOT NULL) AS had_state
    FROM provider_account_auth_cooldowns
    WHERE provider_account_id = sqlc.arg(provider_account_id)::bigint
)
INSERT INTO provider_account_auth_cooldowns AS t (
    provider_account_id, tenant_id, strike, auth_until, hard_disabled, credential_version,
    last_failure_class, resumed_at, created_at, updated_at
)
SELECT sqlc.arg(provider_account_id)::bigint, pa.tenant_id, 0, NULL, false, 0, 'ambiguous',
       now(), sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
FROM provider_accounts pa
WHERE pa.id = sqlc.arg(provider_account_id)::bigint
ON CONFLICT (provider_account_id) DO UPDATE SET
    strike = 0,
    auth_until = NULL,
    hard_disabled = false,
    hard_disabled_at = NULL,
    -- 墓碑时刻取数据库时钟,不依赖执行 resume 的副本本机时钟。
    resumed_at = now(),
    updated_at = sqlc.arg(now)::timestamptz
RETURNING COALESCE((SELECT b.had_state FROM before b), false)::boolean AS had_state;

-- name: GetAuthCooldown :one
SELECT provider_account_id, tenant_id, strike, auth_until, hard_disabled, credential_version,
       last_failure_class, last_escalated_at, hard_disabled_at, resumed_at
FROM provider_account_auth_cooldowns
WHERE provider_account_id = sqlc.arg(provider_account_id)::bigint;

-- name: ListAuthCooldowns :many
-- 进程镜像重载:取全部仍有状态的行(硬禁、strike>0 或带截止;含已过期但保留 strike 的行,与进程内
-- "条目保留到成功/轮换/resume"语义一致),使成功请求能识别需要清除的行、不为每次成功请求发一条写语句。
-- 带 resume 墓碑的无状态行也返回:重载据此丢弃早于墓碑的本地兜底条目(他副本 resume 后本副本不再多挡),
-- 但它们不进镜像。行数以"当前处于车道中 + 曾被 resume"的账号数为上界。
SELECT provider_account_id, tenant_id, strike, auth_until, hard_disabled, credential_version,
       last_failure_class, last_escalated_at, hard_disabled_at, resumed_at
FROM provider_account_auth_cooldowns
WHERE hard_disabled OR strike > 0 OR auth_until IS NOT NULL OR resumed_at IS NOT NULL
ORDER BY provider_account_id;
