ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS session_window_7d_start,
    DROP COLUMN IF EXISTS session_window_7d_end,
    DROP COLUMN IF EXISTS session_window_7d_status,
    DROP COLUMN IF EXISTS session_window_5h_utilization,
    DROP COLUMN IF EXISTS session_window_7d_utilization;
