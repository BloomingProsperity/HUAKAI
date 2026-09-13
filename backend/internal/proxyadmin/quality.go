package proxyadmin

import (
	"context"
	"errors"
	"strings"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyquality"
	"github.com/jackc/pgx/v5"
)

// RecordQuality 把一次固定 canary 探测写入权威快照。失败不得清空上次成功。
func (s *Service) RecordQuality(ctx context.Context, tenantID, id int64, in QualityWrite) (Proxy, error) {
	if tenantID <= 0 || id <= 0 {
		return Proxy{}, ErrInvalidInput
	}
	source := strings.TrimSpace(in.Source)
	if source != proxyquality.SourceManual && source != proxyquality.SourcePeriodic {
		return Proxy{}, ErrInvalidInput
	}
	if in.LatencyMS < 0 {
		return Proxy{}, ErrInvalidInput
	}
	if s == nil || s.q == nil {
		return Proxy{}, ErrStoreNotConfigured
	}
	grade := proxyquality.Grade(in.OK, in.LatencyMS)
	ok := in.OK
	latency := in.LatencyMS
	row, err := s.q.RecordProxyQuality(ctx, admindb.RecordProxyQualityParams{
		QualityOk:         &ok,
		QualityLatencyMs:  &latency,
		QualityErrorClass: strings.TrimSpace(in.ErrorClass),
		QualityGrade:      &grade,
		QualitySource:     &source,
		TenantID:          tenantID,
		ID:                id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Proxy{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromRecord(row), nil
}
