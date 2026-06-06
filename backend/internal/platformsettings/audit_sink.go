package platformsettings

import (
	"context"
	"encoding/json"
	"strings"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const (
	AdminAuditActionUpsert = "update_platform_settings"
	AdminAuditTargetType   = "platform_setting"
)

type AdminAuditWriter interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminAuditSink struct {
	q AdminAuditWriter
}

func NewAdminAuditSink(q AdminAuditWriter) *AdminAuditSink {
	return &AdminAuditSink{q: q}
}

func (s *AdminAuditSink) WriteAdminAudit(ctx context.Context, params AuditParams) error {
	if s == nil || s.q == nil {
		return ErrStoreNotConfigured
	}
	payload, err := platformSettingAuditPayload(params)
	if err != nil {
		return err
	}
	arg := admindb.InsertAdminAuditEventParams{
		ActorID:    strings.TrimSpace(params.ActorID),
		ActorRole:  strings.TrimSpace(params.ActorRole),
		Action:     AdminAuditActionUpsert,
		TargetType: AdminAuditTargetType,
		Payload:    payload,
	}
	if params.TargetID > 0 {
		arg.TargetID = &params.TargetID
	}
	if reason := strings.TrimSpace(params.Reason); reason != "" {
		arg.Reason = &reason
	}
	if requestID := strings.TrimSpace(params.RequestID); requestID != "" {
		arg.RequestID = &requestID
	}
	_, err = s.q.InsertAdminAuditEvent(ctx, arg)
	return err
}

func platformSettingAuditPayload(params AuditParams) ([]byte, error) {
	return json.Marshal(map[string]string{
		"setting_key":     string(params.Key),
		"previous_value":  auditValueForSetting(params.Key, params.OldValue),
		"previous_source": params.OldSource,
		"new_value":       auditValueForSetting(params.Key, params.NewValue),
		"new_source":      SourceDB,
	})
}

func auditValueForSetting(key SettingKey, value string) string {
	if key == KeyModerationExternalAPIKeys && strings.TrimSpace(value) != "" {
		return "[redacted]"
	}
	return value
}

var _ AuditSink = (*AdminAuditSink)(nil)
