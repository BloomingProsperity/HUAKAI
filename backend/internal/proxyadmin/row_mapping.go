package proxyadmin

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyquality"
)

func fromCreate(r admindb.CreateProxyRow) Proxy {
	return composeProxy(
		r.ID, r.TenantID, r.Name, r.Protocol, r.Host, r.Port,
		r.AuthUsername, r.GroupID, r.Status, r.LastCheckAt,
		r.QualityProbedAt, r.QualityOk, r.QualityLatencyMs, r.QualityErrorClass,
		r.QualityGrade, r.QualitySource, r.QualitySuccessAt, r.QualitySuccessLatencyMs,
		r.CreatedAt, r.UpdatedAt,
	)
}

func fromUpdate(r admindb.UpdateProxyRow) Proxy {
	return composeProxy(
		r.ID, r.TenantID, r.Name, r.Protocol, r.Host, r.Port,
		r.AuthUsername, r.GroupID, r.Status, r.LastCheckAt,
		r.QualityProbedAt, r.QualityOk, r.QualityLatencyMs, r.QualityErrorClass,
		r.QualityGrade, r.QualitySource, r.QualitySuccessAt, r.QualitySuccessLatencyMs,
		r.CreatedAt, r.UpdatedAt,
	)
}

func fromGet(r admindb.GetProxyRow) Proxy {
	return composeProxy(
		r.ID, r.TenantID, r.Name, r.Protocol, r.Host, r.Port,
		r.AuthUsername, r.GroupID, r.Status, r.LastCheckAt,
		r.QualityProbedAt, r.QualityOk, r.QualityLatencyMs, r.QualityErrorClass,
		r.QualityGrade, r.QualitySource, r.QualitySuccessAt, r.QualitySuccessLatencyMs,
		r.CreatedAt, r.UpdatedAt,
	)
}

func fromList(r admindb.ListProxiesByTenantRow) Proxy {
	return composeProxy(
		r.ID, r.TenantID, r.Name, r.Protocol, r.Host, r.Port,
		r.AuthUsername, r.GroupID, r.Status, r.LastCheckAt,
		r.QualityProbedAt, r.QualityOk, r.QualityLatencyMs, r.QualityErrorClass,
		r.QualityGrade, r.QualitySource, r.QualitySuccessAt, r.QualitySuccessLatencyMs,
		r.CreatedAt, r.UpdatedAt,
	)
}

func fromRecord(r admindb.RecordProxyQualityRow) Proxy {
	return composeProxy(
		r.ID, r.TenantID, r.Name, r.Protocol, r.Host, r.Port,
		r.AuthUsername, r.GroupID, r.Status, r.LastCheckAt,
		r.QualityProbedAt, r.QualityOk, r.QualityLatencyMs, r.QualityErrorClass,
		r.QualityGrade, r.QualitySource, r.QualitySuccessAt, r.QualitySuccessLatencyMs,
		r.CreatedAt, r.UpdatedAt,
	)
}

func composeProxy(
	id, tenantID int64, name, protocol, host string, port int32,
	authUsername, groupID *string, status string, lastCheck pgtype.Timestamptz,
	probedAt pgtype.Timestamptz, ok *bool, latency *int64, errClass string,
	grade, source *string, successAt pgtype.Timestamptz, successLatency *int64,
	createdAt, updatedAt pgtype.Timestamptz,
) Proxy {
	return Proxy{
		ID: id, TenantID: tenantID, Name: name, Protocol: protocol, Host: host, Port: port,
		AuthUsername: authUsername, GroupID: groupID, Status: status, LastCheckAt: tsPtr(lastCheck),
		Quality:   projectQuality(probedAt, ok, latency, errClass, grade, source, successAt, successLatency),
		CreatedAt: ts(createdAt), UpdatedAt: ts(updatedAt),
	}
}

func projectQuality(
	probedAt pgtype.Timestamptz, ok *bool, latency *int64, errClass string,
	grade, source *string, successAt pgtype.Timestamptz, successLatency *int64,
) Quality {
	if !probedAt.Valid {
		return Quality{EffectiveGrade: proxyquality.GradeUnknown}
	}
	now := time.Now().UTC()
	stored := ""
	if grade != nil {
		stored = *grade
	}
	src := ""
	if source != nil {
		src = *source
	}
	fresh := proxyquality.Fresh(probedAt.Time, now)
	return Quality{
		HasSnapshot:      true,
		ProbedAt:         tsPtr(probedAt),
		OK:               ok,
		LatencyMS:        latency,
		ErrorClass:       errClass,
		Grade:            stored,
		Source:           src,
		SuccessAt:        tsPtr(successAt),
		SuccessLatencyMS: successLatency,
		Fresh:            fresh,
		EffectiveGrade:   proxyquality.Effective(stored, probedAt.Time, now),
	}
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
