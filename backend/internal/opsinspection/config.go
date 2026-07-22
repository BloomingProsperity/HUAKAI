package opsinspection

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// 控制每日巡检的环境变量。启用开关默认为 FALSE（需主动开启），这样未经配置的
// 部署永远不会开始发送邮件——与保留期 worker 的默认关闭姿态保持一致。
const (
	EnvEnabled   = "HUAKAI_DAILY_OPS_INSPECTION_ENABLED"
	EnvInterval  = "HUAKAI_DAILY_OPS_INSPECTION_INTERVAL"
	EnvRecipient = "HUAKAI_ADMIN_NOTIFICATION_EMAIL"

	// DefaultInterval 是 EnvInterval 未设置时的运行节奏。
	DefaultInterval = 24 * time.Hour
)

// Config 是在接线时读取并解析出的 worker 配置。
type Config struct {
	Enabled  bool
	Interval time.Duration
	// Recipient 是解析出的管理员地址。为空表示「未解析出收件人」——这种情况下
	// worker 绝不能启动。
	Recipient string
	// RecipientSource 记录 Recipient 的来源（"setting" / "env" / "none"），
	// 用于启动日志那一行。
	RecipientSource string
}

// EnabledFromEnv 报告每日巡检是否被主动开启。除真值标记（true/1/yes/on）之外的
// 任何值都视为 OFF——这是安全默认值。
func EnabledFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvEnabled))) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// IntervalFromEnv 解析运行间隔；未设置 => DefaultInterval。格式错误或非正数会返回
// 错误，这样拼写错误会在启动时显式报错，而不是悄悄以某个意外节奏调度。
func IntervalFromEnv() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(EnvInterval))
	if raw == "" {
		return DefaultInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s：无效时长 %q：%w", EnvInterval, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s：时长必须为正数，当前为 %s", EnvInterval, raw)
	}
	return d, nil
}

// settingGetter 是收件人解析器所需的 platformsettings.Service 的只读子集。
// 保持为窄接口，这样测试无需 store 即可对它打桩。
type settingGetter interface {
	Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// ResolveRecipient 按以下优先级解析管理员地址：
//  1. admin_notification_email 平台设置（存在且非空时），
//  2. HUAKAI_ADMIN_NOTIFICATION_EMAIL 环境变量兜底，
//  3. 都没有——返回 ("", "none")。
//
// 设置读取出错会下沉到环境变量兜底（绝不因 DB 短暂抖动而阻塞启动）。返回的
// source 字符串会写入启动日志。
func ResolveRecipient(ctx context.Context, settings settingGetter) (string, string) {
	if settings != nil {
		if s, err := settings.Get(ctx, platformsettings.KeyAdminNotificationEmail); err == nil {
			if v := strings.TrimSpace(s.Value); v != "" {
				return v, "setting"
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvRecipient)); v != "" {
		return v, "env"
	}
	return "", "none"
}

// LoadConfig 从环境变量 + 平台设置解析出完整的 worker 配置。它本身不决定是否启动
// ——由调用方检查 Enabled && Recipient。
func LoadConfig(ctx context.Context, settings settingGetter) (Config, error) {
	interval, err := IntervalFromEnv()
	if err != nil {
		return Config{}, err
	}
	recipient, source := ResolveRecipient(ctx, settings)
	return Config{
		Enabled:         EnabledFromEnv(),
		Interval:        interval,
		Recipient:       recipient,
		RecipientSource: source,
	}, nil
}
