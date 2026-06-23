package config

import (
	"os"
	"strconv"
	"strings"
)

// DefaultKeyQuota 描述新建自助 API key 时种入的保守默认配额(防滥用兜底)。
//
// 背景:中转站对外卖额度时,新用户 key 若默认 0 限制,单 key 可在烧完余额前并发猛刷最贵模型、
// 打爆上游账号池。成熟项目的频次限流默认均为不限、仅靠余额兜底(并都建议反着设默认);HUAKAI
// 据此出厂即种保守默认,运营者可经下列 env 调整或整体关闭。
type DefaultKeyQuota struct {
	// Enabled 总开关:false 则建 key 不种任何默认策略(回到"无限直到运营手动配")。
	Enabled bool
	// RPM 每分钟请求上限(requests / fixed-60s);<=0 视为该维度不种。
	RPM int
	// ConcurrencyMax 并发上限(concurrency / windowless);<=0 视为该维度不种。
	ConcurrencyMax int
}

const (
	// EnvDefaultKeyQuotaEnabled 默认 key 配额总开关(默认 true)。
	EnvDefaultKeyQuotaEnabled = "HUAKAI_QUOTA_DEFAULT_KEY_LIMITS_ENABLED"
	// EnvDefaultKeyQuotaRPM 默认每分钟请求上限(默认 60)。
	EnvDefaultKeyQuotaRPM = "HUAKAI_QUOTA_DEFAULT_KEY_RPM"
	// EnvDefaultKeyQuotaConcurrency 默认并发上限(默认 5)。
	EnvDefaultKeyQuotaConcurrency = "HUAKAI_QUOTA_DEFAULT_KEY_CONCURRENCY"

	defaultKeyQuotaRPMFactory         = 60
	defaultKeyQuotaConcurrencyFactory = 5
)

// LoadDefaultKeyQuota 从 env 读默认 key 配额。出厂保守默认(启用 / RPM 60 / 并发 5),
// 运维可经 env 覆盖;解析失败或缺省一律回退出厂默认,绝不因配置错误把保护关掉。
func LoadDefaultKeyQuota() DefaultKeyQuota {
	out := DefaultKeyQuota{
		Enabled:        true,
		RPM:            defaultKeyQuotaRPMFactory,
		ConcurrencyMax: defaultKeyQuotaConcurrencyFactory,
	}
	if raw := strings.TrimSpace(os.Getenv(EnvDefaultKeyQuotaEnabled)); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			out.Enabled = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv(EnvDefaultKeyQuotaRPM)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			out.RPM = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv(EnvDefaultKeyQuotaConcurrency)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			out.ConcurrencyMax = v
		}
	}
	return out
}
