-- 0066_s1_020_proxy_backfill_validation.up.sql
--
-- Forward validator for databases that already applied the original 0038 before
-- the S1-020 fail-closed preflight was added. 0038 dropped
-- provider_accounts.proxy_url, so rows that the old 0038 converted to
-- proxy_id=NULL cannot be distinguished from explicit direct-connect accounts
-- in a forward-only migration. If operators suspect malformed non-empty
-- proxy_url values existed before 0038, restore from a pre-0038 backup, fix or
-- clear those values, then rerun the patched 0038 path.
--
-- This migration validates the recoverable evidence that still exists after
-- 0038: imported proxy rows must not contain malformed host/port/protocol data.

BEGIN;

DO $$
DECLARE
    malformed_count bigint;
    malformed_ids   text;
BEGIN
    IF to_regclass('public.proxies') IS NULL THEN
        RAISE EXCEPTION 'cannot apply 0066 S1-020 validation: proxies table is missing; run migration 0038 first';
    END IF;

    SELECT count(*), string_agg(p.id::text, ', ' ORDER BY p.id)
    INTO malformed_count, malformed_ids
    FROM proxies p
    WHERE p.name LIKE 'imported-%'
      AND (
          p.protocol NOT IN ('http', 'https', 'socks5')
          OR p.host IS NULL
          OR p.host = ''
          OR p.host !~ '^[^:/@?#]+$'
          OR p.port < 1
          OR p.port > 65535
      );

    IF malformed_count > 0 THEN
        RAISE EXCEPTION
            'cannot validate S1-020 proxy backfill: % imported proxy row(s) have malformed fields, proxy ids=%; repair from pre-0038 proxy_url source or disable affected accounts before retrying',
            malformed_count,
            malformed_ids;
    END IF;
END $$;

COMMIT;
