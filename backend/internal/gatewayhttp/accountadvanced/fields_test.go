package accountadvanced

import (
	"math"
	"reflect"
	"testing"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestSpecs精确覆盖统一字段集(t *testing.T) {
	want := []string{
		"rpm_limit", "tpm_limit", "window_cost_limit_cents", "max_sessions",
		"disable_cooling", "refresh_lead_seconds", "expires_at", "tls_fingerprint_rotate",
		"custom_error_codes_enabled", "custom_error_codes", "pool_mode",
		"temp_unschedulable_enabled", "temp_unschedulable_rules", "proxy_binding",
	}
	if got := Keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("高级字段 key 不一致\n got=%v\nwant=%v", got, want)
	}
	for _, spec := range Specs() {
		if !spec.Create || !spec.Update {
			t.Fatalf("字段必须同时支持 create/update: %+v", spec)
		}
	}
}

func Test缺席高级字段不翻转Create默认且Update无写入(t *testing.T) {
	mutation, err := Parse([]byte(`{"name":"只有基础字段"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if mutation.Any() {
		t.Fatalf("未提交高级字段却被判定为有更改: %+v", mutation)
	}
	var create admindb.InsertProviderAccountParams
	ApplyCreate(mutation, &create)
	if create.RPMLimit != nil || create.TPMLimit != nil || create.WindowCostLimitCents != nil || create.MaxSessions != nil ||
		create.DisableCooling != nil || create.RefreshLeadSeconds != nil || create.ExpiresAt.Valid || create.TLSFingerprintRotate != nil ||
		create.CustomErrorCodesEnabled != nil || create.CustomErrorCodes != nil || create.PoolMode != nil ||
		create.TempUnschedulableEnabled != nil || create.TempUnschedulableRulesJSON != nil || create.ProxyID != nil || create.ProxyGroupID != nil {
		t.Fatalf("create 缺席字段不应覆盖 SQL 默认: %+v", create)
	}
	var update admindb.UpdateAdminProviderAccountParams
	ApplyUpdate(mutation, &update)
	if update.RPMLimit != nil || update.TPMLimit != nil || update.WindowCostLimitCents != nil || update.MaxSessions != nil ||
		update.DisableCooling != nil || update.SetRefreshLeadSeconds || update.SetExpiresAt || update.TLSFingerprintRotate != nil ||
		update.CustomErrorCodesEnabled != nil || update.SetCustomErrorCodes || update.PoolMode != nil ||
		update.TempUnschedulableEnabled != nil || update.SetTempUnschedulableRules || update.SetProxyID || update.SetProxyGroupID {
		t.Fatalf("update 缺席字段不应生成写入: %+v", update)
	}
}

func TestParse与Create映射保留显式零值和完整字段(t *testing.T) {
	got, err := Parse([]byte(`{
		"rpm_limit":0,
		"tpm_limit":1200,
		"window_cost_limit_cents":345,
		"max_sessions":6,
		"disable_cooling":false,
		"refresh_lead_seconds":90,
		"expires_at":"2025-01-02T03:04:05Z",
		"tls_fingerprint_rotate":true,
		"custom_error_codes_enabled":true,
		"custom_error_codes":[429,529],
		"pool_mode":true,
		"temp_unschedulable_enabled":true,
		"temp_unschedulable_rules":[{"error_code":529,"keywords":[" busy ",""],"duration_minutes":5,"description":" 拥塞 "}],
		"proxy_binding":{"mode":"proxy","proxy_id":77}
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var arg admindb.InsertProviderAccountParams
	ApplyCreate(got, &arg)
	if arg.RPMLimit == nil || *arg.RPMLimit != 0 {
		t.Fatalf("显式 rpm_limit=0 被吞掉: %+v", arg.RPMLimit)
	}
	if arg.TPMLimit == nil || *arg.TPMLimit != 1200 || arg.WindowCostLimitCents == nil || *arg.WindowCostLimitCents != 345 {
		t.Fatalf("int64 映射错误: %+v", arg)
	}
	if arg.MaxSessions == nil || *arg.MaxSessions != 6 || arg.DisableCooling == nil || *arg.DisableCooling {
		t.Fatalf("int32/bool 映射错误: %+v", arg)
	}
	if arg.RefreshLeadSeconds == nil || *arg.RefreshLeadSeconds != 90 || !arg.ExpiresAt.Valid || !arg.ExpiresAt.Time.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("nullable 字段映射错误: %+v", arg)
	}
	if arg.TLSFingerprintRotate == nil || !*arg.TLSFingerprintRotate || arg.CustomErrorCodesEnabled == nil || !*arg.CustomErrorCodesEnabled {
		t.Fatalf("开关映射错误: %+v", arg)
	}
	if !reflect.DeepEqual(arg.CustomErrorCodes, []int32{429, 529}) || arg.PoolMode == nil || !*arg.PoolMode || arg.TempUnschedulableEnabled == nil || !*arg.TempUnschedulableEnabled {
		t.Fatalf("错误策略映射错误: %+v", arg)
	}
	if string(arg.TempUnschedulableRulesJSON) != `[{"error_code":529,"keywords":["busy"],"duration_minutes":5,"description":"拥塞"}]` {
		t.Fatalf("规则未规范化: %s", arg.TempUnschedulableRulesJSON)
	}
	if arg.ProxyID == nil || *arg.ProxyID != 77 || arg.ProxyGroupID != nil {
		t.Fatalf("代理映射未互斥: proxy=%v group=%v", arg.ProxyID, arg.ProxyGroupID)
	}
}

func TestUpdate只设置请求出现的字段(t *testing.T) {
	got, err := Parse([]byte(`{"rpm_limit":8,"disable_cooling":false,"expires_at":null,"refresh_lead_seconds":null,"custom_error_codes":[],"temp_unschedulable_rules":[],"proxy_binding":{"mode":"direct"}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var arg admindb.UpdateAdminProviderAccountParams
	ApplyUpdate(got, &arg)
	if arg.RPMLimit == nil || *arg.RPMLimit != 8 || arg.TPMLimit != nil || arg.WindowCostLimitCents != nil || arg.MaxSessions != nil {
		t.Fatalf("部分更新数值字段串扰: %+v", arg)
	}
	if arg.DisableCooling == nil || *arg.DisableCooling || arg.TLSFingerprintRotate != nil || arg.PoolMode != nil {
		t.Fatalf("部分更新布尔字段串扰: %+v", arg)
	}
	if !arg.SetExpiresAt || arg.ExpiresAt.Valid || !arg.SetRefreshLeadSeconds || arg.RefreshLeadSeconds != nil {
		t.Fatalf("显式 null 未形成 clear: %+v", arg)
	}
	if !arg.SetCustomErrorCodes || arg.CustomErrorCodes == nil || len(arg.CustomErrorCodes) != 0 {
		t.Fatalf("显式空错误码未形成 clear: %#v", arg.CustomErrorCodes)
	}
	if !arg.SetTempUnschedulableRules || string(arg.TempUnschedulableRulesJSON) != "[]" {
		t.Fatalf("显式空规则未形成 clear: %s", arg.TempUnschedulableRulesJSON)
	}
	if !arg.SetProxyID || arg.ProxyID != nil || !arg.SetProxyGroupID || arg.ProxyGroupID != nil {
		t.Fatalf("direct 未同时清两列: %+v", arg)
	}
}

func TestParse拒绝越界和错误形状(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "RPM 负数", body: `{"rpm_limit":-1}`},
		{name: "RPM 超出 int64", body: `{"rpm_limit":9223372036854775808}`},
		{name: "TPM 非整数", body: `{"tpm_limit":1.5}`},
		{name: "会话数超出 int32", body: `{"max_sessions":2147483648}`},
		{name: "非空字段 null", body: `{"disable_cooling":null}`},
		{name: "错误时间", body: `{"expires_at":"2026/01/01"}`},
		{name: "错误码过小", body: `{"custom_error_codes":[99]}`},
		{name: "错误码过大", body: `{"custom_error_codes":[600]}`},
		{name: "规则分钟非正", body: `{"temp_unschedulable_rules":[{"error_code":529,"duration_minutes":0}]}`},
		{name: "规则错误码越界", body: `{"temp_unschedulable_rules":[{"error_code":700,"duration_minutes":5}]}`},
		{name: "代理缺 ID", body: `{"proxy_binding":{"mode":"proxy"}}`},
		{name: "代理组为空", body: `{"proxy_binding":{"mode":"group","proxy_group_id":"  "}}`},
		{name: "代理模式非法", body: `{"proxy_binding":{"mode":"random"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.body)); err == nil {
				t.Fatalf("非法输入未被拒绝: %s", tc.body)
			}
		})
	}
}

func TestParse接受真实数值上界(t *testing.T) {
	got, err := Parse([]byte(`{"rpm_limit":9223372036854775807,"max_sessions":2147483647,"refresh_lead_seconds":2147483647}`))
	if err != nil {
		t.Fatalf("边界值应通过: %v", err)
	}
	if got.RPMLimit.Value != math.MaxInt64 || got.MaxSessions.Value != math.MaxInt32 || got.RefreshLeadSeconds.Value != math.MaxInt32 {
		t.Fatalf("边界值解析错误: %+v", got)
	}
}

func TestBindingFromColumns规范化兼容列(t *testing.T) {
	proxyID := int64(9)
	group := " eu "
	if got := BindingFromColumns(&proxyID, &group); got.Mode != "proxy" || got.ProxyID == nil || *got.ProxyID != 9 || got.ProxyGroupID != nil {
		t.Fatalf("单代理优先级错误: %+v", got)
	}
	if got := BindingFromColumns(nil, &group); got.Mode != "group" || got.ProxyGroupID == nil || *got.ProxyGroupID != "eu" {
		t.Fatalf("代理组规范化错误: %+v", got)
	}
	if got := BindingFromColumns(nil, nil); got.Mode != "direct" || got.ProxyID != nil || got.ProxyGroupID != nil {
		t.Fatalf("直连规范化错误: %+v", got)
	}
}
