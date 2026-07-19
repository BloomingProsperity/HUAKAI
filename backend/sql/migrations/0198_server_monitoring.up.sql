BEGIN;

CREATE TABLE server_monitor_nodes (
    node_id                text        PRIMARY KEY,
    display_name           text        NOT NULL,
    identity_source        text        NOT NULL CHECK (identity_source IN ('configured', 'runtime_identity_hash')),
    identity_stable        boolean     NOT NULL,
    source_kind            text        NOT NULL CHECK (source_kind = 'builtin'),
    view_scope             text        NOT NULL CHECK (view_scope IN ('host', 'container', 'process_only')),
    session_id             uuid        NOT NULL,
    session_started_at     timestamptz NOT NULL,
    last_sequence          bigint      NOT NULL CHECK (last_sequence > 0),
    last_activity_at       timestamptz NOT NULL,
    last_success_at        timestamptz,
    last_error_at          timestamptz,
    last_recovered_at      timestamptz,
    collection_status      text        NOT NULL CHECK (collection_status IN ('success', 'partial', 'failed')),
    active_error_classes   text[]      NOT NULL DEFAULT '{}',
    os_name                text        NOT NULL,
    os_arch                text        NOT NULL,
    metrics                jsonb       NOT NULL,
    metric_states          jsonb       NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT server_monitor_nodes_node_id_check
        CHECK (node_id ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    CONSTRAINT server_monitor_nodes_display_name_check
        CHECK (length(display_name) BETWEEN 1 AND 128),
    CONSTRAINT server_monitor_nodes_platform_check
        CHECK (length(os_name) BETWEEN 1 AND 32 AND length(os_arch) BETWEEN 1 AND 32),
    CONSTRAINT server_monitor_nodes_identity_stability_check
        CHECK ((identity_source = 'configured') = identity_stable),
    CONSTRAINT server_monitor_nodes_activity_order_check
        CHECK (last_activity_at >= session_started_at)
);

CREATE TABLE server_monitor_samples (
    node_id                text        NOT NULL REFERENCES server_monitor_nodes(node_id) ON DELETE CASCADE,
    bucket_at              timestamptz NOT NULL,
    collected_at           timestamptz NOT NULL,
    received_at            timestamptz NOT NULL DEFAULT now(),
    session_id             uuid        NOT NULL,
    session_started_at     timestamptz NOT NULL,
    sequence               bigint      NOT NULL CHECK (sequence > 0),
    view_scope             text        NOT NULL CHECK (view_scope IN ('host', 'container', 'process_only')),
    collection_status      text        NOT NULL CHECK (collection_status IN ('success', 'partial', 'failed')),
    active_error_classes   text[]      NOT NULL DEFAULT '{}',
    metrics                jsonb       NOT NULL,
    metric_states          jsonb       NOT NULL,
    PRIMARY KEY (node_id, bucket_at),
    CONSTRAINT server_monitor_samples_bucket_check
        CHECK (bucket_at = date_trunc('minute', bucket_at)),
    CONSTRAINT server_monitor_samples_session_sequence_key
        UNIQUE (node_id, session_id, sequence),
    CONSTRAINT server_monitor_samples_activity_order_check
        CHECK (collected_at >= session_started_at)
);

CREATE INDEX idx_server_monitor_nodes_activity
    ON server_monitor_nodes (last_activity_at DESC, node_id);

CREATE INDEX idx_server_monitor_nodes_status
    ON server_monitor_nodes (collection_status, last_activity_at DESC, node_id);

CREATE INDEX idx_server_monitor_samples_time
    ON server_monitor_samples (bucket_at DESC, node_id);

COMMENT ON TABLE server_monitor_nodes IS
    '网关内置实例监测的当前投影；不保存真实主机名、IP、硬件序列或进程命令行。';
COMMENT ON TABLE server_monitor_samples IS
    '网关内置实例的一分钟历史快照；空时间桶保持为空，不伪造零值样本。';

COMMIT;
