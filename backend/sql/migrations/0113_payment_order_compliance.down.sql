ALTER TABLE payment_orders
    DROP COLUMN IF EXISTS terms_accepted_ip,
    DROP COLUMN IF EXISTS terms_accepted_by,
    DROP COLUMN IF EXISTS terms_accepted_at,
    DROP COLUMN IF EXISTS terms_version;
