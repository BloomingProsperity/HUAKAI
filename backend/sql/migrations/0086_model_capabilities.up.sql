ALTER TABLE models
    ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN max_output_tokens integer,
    ADD COLUMN model_mode text;
