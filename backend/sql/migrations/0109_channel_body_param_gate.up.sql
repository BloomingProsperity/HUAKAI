-- 0109_channel_body_param_gate.up.sql
--
-- Channel-level opt-in request body parameter gates.

BEGIN;

ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS body_param_strips text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN IF NOT EXISTS param_override jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN channels.body_param_strips IS
    'Opt-in top-level request JSON fields, plus stream_options.include_obfuscation, stripped before upstream dispatch. Empty array preserves passthrough.';
COMMENT ON COLUMN channels.param_override IS
    'Opt-in top-level request JSON field overrides applied before body_param_strips. Empty object preserves passthrough.';

COMMIT;
