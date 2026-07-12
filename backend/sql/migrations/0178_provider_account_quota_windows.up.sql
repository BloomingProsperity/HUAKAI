ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS session_window_7d_start timestamptz,
    ADD COLUMN IF NOT EXISTS session_window_7d_end timestamptz,
    ADD COLUMN IF NOT EXISTS session_window_7d_status text,
    ADD COLUMN IF NOT EXISTS session_window_5h_utilization numeric(5, 2),
    ADD COLUMN IF NOT EXISTS session_window_7d_utilization numeric(5, 2);
