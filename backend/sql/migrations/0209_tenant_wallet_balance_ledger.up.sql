BEGIN;

-- 下级租户经营额度属于租户本身，不绑定某一个管理员账号。
CREATE TABLE tenant_wallets (
    tenant_id  BIGINT PRIMARY KEY REFERENCES tenants(id),
    balance    NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    version    BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- 手工额度下发、收回和租户内分发的永久交易事实。
CREATE TABLE balance_ledger_transactions (
    id                    BIGSERIAL PRIMARY KEY,
    tenant_id             BIGINT NOT NULL REFERENCES tenants(id),
    platform_tenant_id    BIGINT NOT NULL REFERENCES tenants(id),
    operation             TEXT NOT NULL CHECK (operation IN (
        'platform_tenant_credit', 'platform_tenant_debit',
        'platform_user_credit', 'platform_user_debit',
        'tenant_user_credit', 'tenant_user_debit'
    )),
    target_user_id        BIGINT,
    amount                NUMERIC(20,8) NOT NULL CHECK (amount > 0),
    currency_code         CHAR(3) NOT NULL DEFAULT 'USD' CHECK (currency_code = 'USD'),
    actor_role            TEXT NOT NULL CHECK (actor_role IN ('platform_admin', 'tenant_operator')),
    actor_ref             TEXT NOT NULL CHECK (btrim(actor_ref) <> ''),
    actor_scope_tenant_id BIGINT REFERENCES tenants(id),
    idempotency_key       TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 128),
    request_fingerprint   TEXT NOT NULL CHECK (char_length(request_fingerprint) = 64),
    reason                TEXT NOT NULL CHECK (btrim(reason) <> ''),
    request_id            TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, target_user_id) REFERENCES users(tenant_id, id),
    CHECK (
        (actor_role = 'platform_admin' AND actor_scope_tenant_id IS NULL)
        OR
        (actor_role = 'tenant_operator' AND actor_scope_tenant_id = tenant_id)
    ),
    CHECK (
        (operation IN ('platform_tenant_credit', 'platform_tenant_debit') AND target_user_id IS NULL)
        OR
        (operation IN ('platform_user_credit', 'platform_user_debit',
                       'tenant_user_credit', 'tenant_user_debit') AND target_user_id IS NOT NULL)
    ),
    CHECK (
        (operation IN ('platform_tenant_credit', 'platform_tenant_debit') AND tenant_id <> platform_tenant_id)
        OR
        (operation IN ('platform_user_credit', 'platform_user_debit') AND tenant_id = platform_tenant_id)
        OR
        (operation IN ('tenant_user_credit', 'tenant_user_debit') AND tenant_id <> platform_tenant_id)
    ),
    CHECK (
        (actor_role = 'platform_admin' AND operation LIKE 'platform_%')
        OR
        (actor_role = 'tenant_operator' AND operation LIKE 'tenant_%')
    )
);

CREATE UNIQUE INDEX uq_balance_ledger_transaction_idempotency
    ON balance_ledger_transactions (tenant_id, actor_ref, idempotency_key);
CREATE UNIQUE INDEX uq_balance_ledger_transaction_tenant_id
    ON balance_ledger_transactions (tenant_id, id);
CREATE INDEX idx_balance_ledger_transaction_tenant_time
    ON balance_ledger_transactions (tenant_id, created_at DESC, id DESC);
CREATE INDEX idx_balance_ledger_transaction_user_time
    ON balance_ledger_transactions (tenant_id, target_user_id, created_at DESC, id DESC)
    WHERE target_user_id IS NOT NULL;

-- 每笔交易恰有来源和目标两个分录；平台账户是外部发行/回收端，不维护可变余额。
CREATE TABLE balance_ledger_entries (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          BIGINT NOT NULL,
    transaction_id     BIGINT NOT NULL,
    account_kind       TEXT NOT NULL CHECK (account_kind IN ('platform', 'tenant', 'user')),
    account_tenant_id  BIGINT NOT NULL REFERENCES tenants(id),
    account_user_id    BIGINT,
    delta              NUMERIC(20,8) NOT NULL CHECK (delta <> 0),
    balance_before     NUMERIC(20,8),
    balance_after      NUMERIC(20,8),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, transaction_id)
        REFERENCES balance_ledger_transactions(tenant_id, id),
    FOREIGN KEY (account_tenant_id, account_user_id)
        REFERENCES users(tenant_id, id),
    CHECK (
        (account_kind = 'platform' AND account_user_id IS NULL
            AND balance_before IS NULL AND balance_after IS NULL)
        OR
        (account_kind = 'tenant' AND account_user_id IS NULL
            AND balance_before IS NOT NULL AND balance_after IS NOT NULL
            AND balance_before >= 0 AND balance_after >= 0)
        OR
        (account_kind = 'user' AND account_user_id IS NOT NULL
            AND balance_before IS NOT NULL AND balance_after IS NOT NULL
            AND balance_before >= 0 AND balance_after >= 0)
    ),
    CHECK (
        account_kind = 'platform'
        OR balance_after = balance_before + delta
    )
);

CREATE UNIQUE INDEX uq_balance_ledger_entry_account
    ON balance_ledger_entries (
        tenant_id, transaction_id, account_kind, account_tenant_id,
        COALESCE(account_user_id, 0)
    );
CREATE INDEX idx_balance_ledger_entry_user_time
    ON balance_ledger_entries (account_tenant_id, account_user_id, created_at DESC, id DESC)
    WHERE account_kind = 'user';
CREATE INDEX idx_balance_ledger_entry_tenant_time
    ON balance_ledger_entries (account_tenant_id, created_at DESC, id DESC)
    WHERE account_kind = 'tenant';

CREATE OR REPLACE FUNCTION enforce_balance_ledger_transaction_shape() RETURNS TRIGGER AS $$
DECLARE
    tx_row balance_ledger_transactions%ROWTYPE;
    target_tenant_id BIGINT;
    target_transaction_id BIGINT;
    entry_count INTEGER;
    entry_sum NUMERIC(20,8);
    platform_delta NUMERIC(20,8);
    tenant_delta NUMERIC(20,8);
    user_delta NUMERIC(20,8);
    invalid_binding_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'balance_ledger_transactions' THEN
        target_tenant_id := NEW.tenant_id;
        target_transaction_id := NEW.id;
    ELSE
        target_tenant_id := NEW.tenant_id;
        target_transaction_id := NEW.transaction_id;
    END IF;

    SELECT * INTO tx_row
    FROM balance_ledger_transactions
    WHERE tenant_id = target_tenant_id AND id = target_transaction_id;

    SELECT COUNT(*), COALESCE(SUM(delta), 0),
           COALESCE(SUM(delta) FILTER (WHERE account_kind = 'platform'), 0),
           COALESCE(SUM(delta) FILTER (WHERE account_kind = 'tenant'), 0),
           COALESCE(SUM(delta) FILTER (WHERE account_kind = 'user'), 0)
      INTO entry_count, entry_sum, platform_delta, tenant_delta, user_delta
      FROM balance_ledger_entries
     WHERE tenant_id = target_tenant_id AND transaction_id = target_transaction_id;

    IF entry_count <> 2 OR entry_sum <> 0 THEN
        RAISE EXCEPTION 'balance ledger transaction % must contain two balanced entries', target_transaction_id;
    END IF;

    SELECT COUNT(*) INTO invalid_binding_count
      FROM balance_ledger_entries entry
     WHERE entry.tenant_id = target_tenant_id
       AND entry.transaction_id = target_transaction_id
       AND (
            (entry.account_kind = 'platform'
                AND (entry.account_tenant_id <> tx_row.platform_tenant_id OR entry.account_user_id IS NOT NULL))
         OR (entry.account_kind = 'tenant'
                AND (entry.account_tenant_id <> tx_row.tenant_id OR entry.account_user_id IS NOT NULL))
         OR (entry.account_kind = 'user'
                AND (entry.account_tenant_id <> tx_row.tenant_id OR entry.account_user_id IS DISTINCT FROM tx_row.target_user_id))
       );
    IF invalid_binding_count <> 0 THEN
        RAISE EXCEPTION 'balance ledger transaction % contains an entry bound to the wrong account', target_transaction_id;
    END IF;

    IF tx_row.operation = 'platform_tenant_credit'
       AND NOT (platform_delta = -tx_row.amount AND tenant_delta = tx_row.amount AND user_delta = 0) THEN
        RAISE EXCEPTION 'invalid platform tenant credit entries';
    ELSIF tx_row.operation = 'platform_tenant_debit'
       AND NOT (platform_delta = tx_row.amount AND tenant_delta = -tx_row.amount AND user_delta = 0) THEN
        RAISE EXCEPTION 'invalid platform tenant debit entries';
    ELSIF tx_row.operation = 'platform_user_credit'
       AND NOT (platform_delta = -tx_row.amount AND user_delta = tx_row.amount AND tenant_delta = 0) THEN
        RAISE EXCEPTION 'invalid platform user credit entries';
    ELSIF tx_row.operation = 'platform_user_debit'
       AND NOT (platform_delta = tx_row.amount AND user_delta = -tx_row.amount AND tenant_delta = 0) THEN
        RAISE EXCEPTION 'invalid platform user debit entries';
    ELSIF tx_row.operation = 'tenant_user_credit'
       AND NOT (tenant_delta = -tx_row.amount AND user_delta = tx_row.amount AND platform_delta = 0) THEN
        RAISE EXCEPTION 'invalid tenant user credit entries';
    ELSIF tx_row.operation = 'tenant_user_debit'
       AND NOT (tenant_delta = tx_row.amount AND user_delta = -tx_row.amount AND platform_delta = 0) THEN
        RAISE EXCEPTION 'invalid tenant user debit entries';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER balance_ledger_transaction_shape
    AFTER INSERT ON balance_ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION enforce_balance_ledger_transaction_shape();

CREATE CONSTRAINT TRIGGER balance_ledger_transaction_complete
    AFTER INSERT ON balance_ledger_transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION enforce_balance_ledger_transaction_shape();

CREATE TRIGGER balance_ledger_transactions_append_only_update
    BEFORE UPDATE ON balance_ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
CREATE TRIGGER balance_ledger_transactions_append_only_delete
    BEFORE DELETE ON balance_ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
CREATE TRIGGER balance_ledger_entries_append_only_update
    BEFORE UPDATE ON balance_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();
CREATE TRIGGER balance_ledger_entries_append_only_delete
    BEFORE DELETE ON balance_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION enforce_money_path_append_only();

COMMIT;
