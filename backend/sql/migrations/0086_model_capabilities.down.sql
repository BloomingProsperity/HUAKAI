ALTER TABLE models
    DROP COLUMN IF EXISTS model_mode,
    DROP COLUMN IF EXISTS max_output_tokens,
    DROP COLUMN IF EXISTS capabilities;
