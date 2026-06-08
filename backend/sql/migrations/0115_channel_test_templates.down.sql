-- Roll back HUAKAI channel health probe test templates.

BEGIN;

DROP INDEX IF EXISTS idx_channel_test_templates_tenant_created;
DROP INDEX IF EXISTS uq_channel_test_templates_tenant_name;
DROP TABLE IF EXISTS channel_test_templates;

COMMIT;
