-- 0242:租户代理线路质量快照。连通、延迟、错误类、等级与上次成功分列；跟随探测写入，不盖掉历史成功。

BEGIN;

ALTER TABLE proxies
    ADD COLUMN quality_probed_at timestamptz,
    ADD COLUMN quality_ok boolean,
    ADD COLUMN quality_latency_ms bigint,
    ADD COLUMN quality_error_class text NOT NULL DEFAULT '',
    ADD COLUMN quality_grade text,
    ADD COLUMN quality_source text,
    ADD COLUMN quality_success_at timestamptz,
    ADD COLUMN quality_success_latency_ms bigint;

ALTER TABLE proxies
    ADD CONSTRAINT proxies_quality_grade_check
        CHECK (quality_grade IS NULL OR quality_grade IN ('excellent', 'good', 'degraded', 'poor')),
    ADD CONSTRAINT proxies_quality_source_check
        CHECK (quality_source IS NULL OR quality_source IN ('manual', 'periodic')),
    ADD CONSTRAINT proxies_quality_latency_check
        CHECK (quality_latency_ms IS NULL OR quality_latency_ms >= 0),
    ADD CONSTRAINT proxies_quality_success_latency_check
        CHECK (quality_success_latency_ms IS NULL OR quality_success_latency_ms >= 0);

COMMENT ON COLUMN proxies.quality_probed_at IS '最近一次线路探测完成时刻；过期后不得把存档等级当成新鲜优质。';
COMMENT ON COLUMN proxies.quality_ok IS '最近一次探测是否打通固定 canary。';
COMMENT ON COLUMN proxies.quality_latency_ms IS '最近一次探测往返毫秒。';
COMMENT ON COLUMN proxies.quality_error_class IS '最近一次失败的粗粒度分类；成功时为空串。';
COMMENT ON COLUMN proxies.quality_grade IS '按最近一次连通与延迟写入的质量档，不含地区。';
COMMENT ON COLUMN proxies.quality_source IS '最近一次探测来源：人工或周期。';
COMMENT ON COLUMN proxies.quality_success_at IS '最近一次成功探测时刻；失败不得清空。';
COMMENT ON COLUMN proxies.quality_success_latency_ms IS '最近一次成功探测延迟。';

COMMIT;
