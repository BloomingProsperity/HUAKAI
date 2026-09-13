-- 0241:后台作业按副本心跳。主键是作业名+副本,跟随者不能盖写其他副本的成功时刻。

BEGIN;

CREATE TABLE worker_job_pulses (
    job_key TEXT NOT NULL,
    replica_id TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    last_success_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    executor BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (job_key, replica_id),
    CONSTRAINT worker_job_pulses_job_key_check CHECK (job_key <> ''),
    CONSTRAINT worker_job_pulses_replica_id_check CHECK (replica_id <> '')
);

CREATE INDEX worker_job_pulses_job_seen_idx
    ON worker_job_pulses (job_key, last_seen_at DESC);

COMMIT;
