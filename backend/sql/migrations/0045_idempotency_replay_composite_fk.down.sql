BEGIN;

ALTER TABLE idempotency_replay_records
    DROP CONSTRAINT IF EXISTS idempotency_replay_records_claim_id_fkey;
ALTER TABLE idempotency_replay_records
    ADD CONSTRAINT idempotency_replay_records_claim_id_fkey
    FOREIGN KEY (claim_id) REFERENCES billing_ledger_claims (id);

COMMIT;
