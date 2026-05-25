BEGIN;

DELETE FROM billing_pricing_versions
WHERE tenant_id = 0
  AND version = 'public_default_v1'
  AND created_by_actor = 'migration:0030_public_pricing_scope';

COMMIT;
