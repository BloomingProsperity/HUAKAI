package antigravity

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
)

// 隐私模式(防封关键):antigravity 反转号 onboard 成功后调 setUserSettings 关闭数据采集
// (telemetry),再 fetchUserInfo 二次核实。真实 Antigravity 客户端首次登录即关采集。
// 默认开;仅显式 HUAKAI_ANTIGRAVITY_PRIVACY=false 关闭(能力交运维,不做守门人)。best-effort:
// 任何失败只结构化告警,绝不阻断 onboard。
const envAntigravityPrivacy = "HUAKAI_ANTIGRAVITY_PRIVACY"

func privacyEnabled() bool {
	return os.Getenv(envAntigravityPrivacy) != "false"
}

type setUserSettingsRequest struct {
	UserSettings map[string]any `json:"user_settings"`
}

type fetchUserInfoRequest struct {
	Project string `json:"project"`
}

// userSettingsEnvelope 是 setUserSettings / fetchUserInfo 响应里 userSettings 子树。
type userSettingsEnvelope struct {
	UserSettings map[string]any `json:"userSettings"`
}

// userSettingsPrivate 判定隐私已生效:userSettings 不含 telemetryEnabled=true。
func userSettingsPrivate(m map[string]any) bool {
	if v, ok := m["telemetryEnabled"]; ok {
		if enabled, ok := v.(bool); ok && enabled {
			return false
		}
	}
	return true
}

func (r *ProjectResolver) ensurePrivacyBestEffort(ctx context.Context, accessToken, projectID string) {
	if !privacyEnabled() {
		return
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return
	}

	// 1) setUserSettings 清空采集,核实响应不含 telemetryEnabled。
	setRaw, err := r.postForProfile(ctx, r.dailyEndpoint(), "setUserSettings", accessToken,
		setUserSettingsRequest{UserSettings: map[string]any{}}, ProjectProfileAntigravity)
	if err != nil {
		slog.WarnContext(ctx, "antigravity 隐私设置失败",
			"event_class", "antigravity_privacy_set_failed", "stage", "set", "reason", err.Error())
		return
	}
	var setResp userSettingsEnvelope
	if json.Unmarshal(setRaw, &setResp) != nil || !userSettingsPrivate(setResp.UserSettings) {
		slog.WarnContext(ctx, "antigravity 隐私设置未生效",
			"event_class", "antigravity_privacy_set_failed", "stage", "set_verify")
		return
	}

	// 2) fetchUserInfo 二次核实隐私已生效(需 project_id)。
	if strings.TrimSpace(projectID) == "" {
		slog.WarnContext(ctx, "antigravity 隐私二次核实缺 project_id",
			"event_class", "antigravity_privacy_verify_skipped")
		return
	}
	infoRaw, err := r.postForProfile(ctx, r.dailyEndpoint(), "fetchUserInfo", accessToken,
		fetchUserInfoRequest{Project: projectID}, ProjectProfileAntigravity)
	if err != nil {
		slog.WarnContext(ctx, "antigravity 隐私二次核实失败",
			"event_class", "antigravity_privacy_verify_failed", "reason", err.Error())
		return
	}
	var info userSettingsEnvelope
	if json.Unmarshal(infoRaw, &info) != nil || !userSettingsPrivate(info.UserSettings) {
		slog.WarnContext(ctx, "antigravity 隐私二次核实未通过",
			"event_class", "antigravity_privacy_verify_not_private")
		return
	}
	slog.InfoContext(ctx, "antigravity 隐私设置成功", "event_class", "antigravity_privacy_set")
}
