ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS terms_version text NULL,
    ADD COLUMN IF NOT EXISTS terms_accepted_at timestamptz NULL,
    ADD COLUMN IF NOT EXISTS terms_accepted_by bigint NULL,
    ADD COLUMN IF NOT EXISTS terms_accepted_ip inet NULL;
