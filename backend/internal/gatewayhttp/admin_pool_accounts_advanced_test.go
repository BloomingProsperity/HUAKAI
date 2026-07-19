package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestAdminPoolAccounts_Create高级字段逐层写入并回显(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	body := `{
		"provider_id":8,"channel_id":9,"name":"advanced","account_type":"api_key","vendor":"openai","auth_mode":"api_key",
		"credentials":{"api_key":"secret"},
		"upstream_cost_ratio":0.5,
		"rpm_limit":0,"tpm_limit":1200,"window_cost_limit_cents":345,"max_sessions":6,
		"disable_cooling":true,"refresh_lead_seconds":90,
		"expires_at":"2025-01-02T03:04:05Z","tls_fingerprint_rotate":true,
		"custom_error_codes_enabled":true,"custom_error_codes":[429,529],"pool_mode":true,
		"temp_unschedulable_enabled":true,
		"temp_unschedulable_rules":[{"rule_id":"busy-529","error_code":529,"keywords":["busy"],"duration_minutes":5,"description":"拥塞","client_status":503,"client_code":"account_busy","message_mode":"custom","client_message":"账号暂不可用","affect_health":false}],
		"proxy_binding":{"mode":"proxy","proxy_id":123}
	}`
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	arg := store.insert
	if arg == nil || arg.RPMLimit == nil || *arg.RPMLimit != 0 || arg.TPMLimit == nil || *arg.TPMLimit != 1200 {
		t.Fatalf("RPM/TPM 未写入 Insert: %+v", arg)
	}
	if arg.UpstreamCostRatio == nil || *arg.UpstreamCostRatio != 0.5 {
		t.Fatalf("上游成本比例未写入 Insert: %+v", arg)
	}
	if arg.WindowCostLimitCents == nil || *arg.WindowCostLimitCents != 345 || arg.MaxSessions == nil || *arg.MaxSessions != 6 {
		t.Fatalf("窗口/会话未写入 Insert: %+v", arg)
	}
	if arg.DisableCooling == nil || !*arg.DisableCooling || arg.RefreshLeadSeconds == nil || *arg.RefreshLeadSeconds != 90 {
		t.Fatalf("冷却/刷新未写入 Insert: %+v", arg)
	}
	if !arg.ExpiresAt.Valid || !arg.ExpiresAt.Time.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) || arg.TLSFingerprintRotate == nil || !*arg.TLSFingerprintRotate {
		t.Fatalf("过期/TLS 未写入 Insert: %+v", arg)
	}
	if arg.CustomErrorCodesEnabled == nil || !*arg.CustomErrorCodesEnabled || len(arg.CustomErrorCodes) != 2 ||
		arg.CustomErrorCodes[0] != 429 || arg.CustomErrorCodes[1] != 529 || arg.PoolMode == nil || !*arg.PoolMode {
		t.Fatalf("建号残缺字段未写入 Insert: %+v", arg)
	}
	if arg.TempUnschedulableEnabled == nil || !*arg.TempUnschedulableEnabled || len(arg.TempUnschedulableRulesJSON) == 0 {
		t.Fatalf("临时停调字段未写入 Insert: %+v", arg)
	}
	if arg.ProxyID == nil || *arg.ProxyID != 123 || arg.ProxyGroupID != nil {
		t.Fatalf("create 代理绑定未互斥写入: %+v", arg)
	}

	var response providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析 create 响应: %v", err)
	}
	if response.RPMLimit != 0 || response.TPMLimit != 1200 || response.WindowCostLimitCents != 345 || response.MaxSessions != 6 {
		t.Fatalf("数值高级字段回显错误: %+v", response)
	}
	if response.UpstreamCostRatio == nil || *response.UpstreamCostRatio != 0.5 {
		t.Fatalf("上游成本比例回显错误: %+v", response)
	}
	if !response.DisableCooling || response.RefreshLeadSeconds == nil || *response.RefreshLeadSeconds != 90 || !response.TLSFingerprintRotate {
		t.Fatalf("开关/刷新回显错误: %+v", response)
	}
	if response.ExpiresAt == nil || !response.ExpiresAt.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("expires_at 回显错误: %v", response.ExpiresAt)
	}
	if !response.CustomErrorCodesEnabled || len(response.CustomErrorCodes) != 2 || response.CustomErrorCodes[0] != 429 ||
		response.CustomErrorCodes[1] != 529 || !response.PoolMode || !response.TempUnschedulableEnabled {
		t.Fatalf("建号残缺字段回显错误: %+v", response)
	}
	if response.ProxyBinding.Mode != "proxy" || response.ProxyBinding.ProxyID == nil || *response.ProxyBinding.ProxyID != 123 {
		t.Fatalf("规范化代理回显错误: %+v", response.ProxyBinding)
	}
	var rules []struct {
		RuleID          string   `json:"rule_id"`
		ErrorCode       int32    `json:"error_code"`
		Keywords        []string `json:"keywords"`
		DurationMinutes int32    `json:"duration_minutes"`
		Description     string   `json:"description"`
		ClientStatus    int      `json:"client_status"`
		ClientCode      string   `json:"client_code"`
		ClientMessage   string   `json:"client_message"`
		AffectHealth    bool     `json:"affect_health"`
	}
	if err := json.Unmarshal(response.TempUnschedulableRules, &rules); err != nil || len(rules) != 1 {
		t.Fatalf("规则回显错误: %v raw=%s", err, response.TempUnschedulableRules)
	}
	if rules[0].RuleID != "busy-529" || rules[0].ErrorCode != 529 || len(rules[0].Keywords) != 1 || rules[0].Keywords[0] != "busy" ||
		rules[0].DurationMinutes != 5 || rules[0].Description != "拥塞" || rules[0].ClientStatus != 503 ||
		rules[0].ClientCode != "account_busy" || rules[0].ClientMessage != "账号暂不可用" || rules[0].AffectHealth {
		t.Fatalf("规则回显值被改写: %+v", rules)
	}
}

func TestAdminPoolAccounts_Update高级字全量改值并回显(t *testing.T) {
	oldTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	oldRefresh := int32(10)
	oldProxyID := int64(12)
	seed := adminProviderRow(77, 7)
	seed.RPMLimit = 1
	seed.TPMLimit = 2
	seed.WindowCostLimitCents = 3
	seed.MaxSessions = 4
	seed.DisableCooling = true
	seed.RefreshLeadSeconds = &oldRefresh
	seed.ExpiresAt = pgtype.Timestamptz{Time: oldTime, Valid: true}
	seed.TLSFingerprintRotate = true
	seed.CustomErrorCodesEnabled = true
	seed.CustomErrorCodes = []int32{400}
	seed.PoolMode = true
	seed.TempUnschedulableEnabled = true
	seed.TempUnschedulableRules = []byte(`[{"error_code":500,"keywords":["old"],"duration_minutes":1}]`)
	seed.ProxyID = &oldProxyID
	store := &adminPoolStoreStub{get: &seed}

	body := `{
		"upstream_cost_ratio":2,
		"rpm_limit":101,"tpm_limit":202,"window_cost_limit_cents":303,"max_sessions":44,
		"disable_cooling":false,"refresh_lead_seconds":55,
		"expires_at":"2028-04-05T06:07:08Z","tls_fingerprint_rotate":false,
		"custom_error_codes_enabled":false,"custom_error_codes":[418,429],"pool_mode":false,
		"temp_unschedulable_enabled":false,
		"temp_unschedulable_rules":[{"rule_id":"busy-503","error_code":503,"keywords":[" busy "],"duration_minutes":9,"description":" new "}],
		"proxy_binding":{"mode":"group","proxy_group_id":"edge-group"}
	}`
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	arg := store.updateFull
	if arg == nil || arg.RPMLimit == nil || *arg.RPMLimit != 101 || arg.TPMLimit == nil || *arg.TPMLimit != 202 ||
		arg.WindowCostLimitCents == nil || *arg.WindowCostLimitCents != 303 || arg.MaxSessions == nil || *arg.MaxSessions != 44 {
		t.Fatalf("全量数值更新映射不完整: %+v", arg)
	}
	if !arg.SetUpstreamCostRatio || arg.UpstreamCostRatio == nil || *arg.UpstreamCostRatio != 2 {
		t.Fatalf("上游成本比例更新映射不完整: %+v", arg)
	}
	if arg.DisableCooling == nil || *arg.DisableCooling || !arg.SetRefreshLeadSeconds || arg.RefreshLeadSeconds == nil ||
		*arg.RefreshLeadSeconds != 55 || !arg.SetExpiresAt || !arg.ExpiresAt.Valid ||
		!arg.ExpiresAt.Time.Equal(time.Date(2028, 4, 5, 6, 7, 8, 0, time.UTC)) ||
		arg.TLSFingerprintRotate == nil || *arg.TLSFingerprintRotate {
		t.Fatalf("开关/可空字段更新映射不完整: %+v", arg)
	}
	if arg.CustomErrorCodesEnabled == nil || *arg.CustomErrorCodesEnabled || !arg.SetCustomErrorCodes ||
		len(arg.CustomErrorCodes) != 2 || arg.CustomErrorCodes[0] != 418 || arg.CustomErrorCodes[1] != 429 ||
		arg.PoolMode == nil || *arg.PoolMode || arg.TempUnschedulableEnabled == nil || *arg.TempUnschedulableEnabled ||
		!arg.SetTempUnschedulableRules || string(arg.TempUnschedulableRulesJSON) != `[{"rule_id":"busy-503","error_code":503,"keywords":["busy"],"duration_minutes":9,"description":"new","message_mode":"fixed","affect_health":true}]` {
		t.Fatalf("错误策略更新映射不完整: %+v", arg)
	}
	if !arg.SetProxyID || arg.ProxyID != nil || !arg.SetProxyGroupID || arg.ProxyGroupID == nil || *arg.ProxyGroupID != "edge-group" {
		t.Fatalf("代理组更新未互斥写入: %+v", arg)
	}

	var response providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析 update 回显: %v", err)
	}
	if response.RPMLimit != 101 || response.TPMLimit != 202 || response.WindowCostLimitCents != 303 || response.MaxSessions != 44 ||
		response.DisableCooling || response.RefreshLeadSeconds == nil || *response.RefreshLeadSeconds != 55 || response.ExpiresAt == nil ||
		!response.ExpiresAt.Equal(time.Date(2028, 4, 5, 6, 7, 8, 0, time.UTC)) || response.TLSFingerprintRotate {
		t.Fatalf("限制/时间字段未原值回显: %+v", response)
	}
	if response.UpstreamCostRatio == nil || *response.UpstreamCostRatio != 2 {
		t.Fatalf("上游成本比例未原值回显: %+v", response)
	}
	if response.CustomErrorCodesEnabled || len(response.CustomErrorCodes) != 2 || response.CustomErrorCodes[0] != 418 ||
		response.CustomErrorCodes[1] != 429 || response.PoolMode || response.TempUnschedulableEnabled {
		t.Fatalf("错误策略字段未原值回显: %+v", response)
	}
	if response.ProxyBinding.Mode != "group" || response.ProxyBinding.ProxyGroupID == nil || *response.ProxyBinding.ProxyGroupID != "edge-group" {
		t.Fatalf("代理组未原值回显: %+v", response.ProxyBinding)
	}
	var updatedRules []struct {
		RuleID          string   `json:"rule_id"`
		ErrorCode       int32    `json:"error_code"`
		Keywords        []string `json:"keywords"`
		DurationMinutes int32    `json:"duration_minutes"`
		Description     string   `json:"description"`
	}
	if err := json.Unmarshal(response.TempUnschedulableRules, &updatedRules); err != nil || len(updatedRules) != 1 ||
		updatedRules[0].RuleID != "busy-503" || updatedRules[0].ErrorCode != 503 || len(updatedRules[0].Keywords) != 1 || updatedRules[0].Keywords[0] != "busy" ||
		updatedRules[0].DurationMinutes != 9 || updatedRules[0].Description != "new" {
		t.Fatalf("规则未原值回显: err=%v rules=%+v", err, updatedRules)
	}
}

func TestAdminPoolAccounts_Update高级字段改一保余并显式清空(t *testing.T) {
	future := time.Date(2027, 2, 3, 4, 5, 6, 0, time.UTC)
	refresh := int32(75)
	group := "stable-group"
	seed := adminProviderRow(77, 7)
	seed.RPMLimit = 11
	oldCostRatio := 1.25
	seed.UpstreamCostRatio = &oldCostRatio
	seed.TPMLimit = 2200
	seed.WindowCostLimitCents = 333
	seed.MaxSessions = 7
	seed.DisableCooling = true
	seed.RefreshLeadSeconds = &refresh
	seed.ExpiresAt = pgtype.Timestamptz{Time: future, Valid: true}
	seed.TLSFingerprintRotate = true
	seed.CustomErrorCodesEnabled = true
	seed.CustomErrorCodes = []int32{429}
	seed.PoolMode = true
	seed.TempUnschedulableEnabled = true
	seed.TempUnschedulableRules = []byte(`[{"error_code":529,"keywords":[],"duration_minutes":9}]`)
	seed.ProxyGroupID = &group
	store := &adminPoolStoreStub{get: &seed}

	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"upstream_cost_ratio":null,"rpm_limit":0,"disable_cooling":false,"refresh_lead_seconds":null,"expires_at":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	arg := store.updateFull
	if arg == nil || arg.RPMLimit == nil || *arg.RPMLimit != 0 || arg.DisableCooling == nil || *arg.DisableCooling {
		t.Fatalf("目标字段未形成精确更新: %+v", arg)
	}
	if !arg.SetRefreshLeadSeconds || arg.RefreshLeadSeconds != nil || !arg.SetExpiresAt || arg.ExpiresAt.Valid {
		t.Fatalf("nullable clear 未形成 Set-flag: %+v", arg)
	}
	if !arg.SetUpstreamCostRatio || arg.UpstreamCostRatio != nil {
		t.Fatalf("成本比例 clear 未形成 Set-flag: %+v", arg)
	}
	if arg.TPMLimit != nil || arg.WindowCostLimitCents != nil || arg.MaxSessions != nil || arg.TLSFingerprintRotate != nil || arg.SetProxyID || arg.SetProxyGroupID {
		t.Fatalf("未提交高级字段被误设: %+v", arg)
	}

	var response providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析 update 响应: %v", err)
	}
	if response.RPMLimit != 0 || response.DisableCooling || response.RefreshLeadSeconds != nil || response.ExpiresAt != nil {
		t.Fatalf("目标字段更新/清除失败: %+v", response)
	}
	if response.UpstreamCostRatio != nil {
		t.Fatalf("成本比例未被清除: %+v", response)
	}
	if response.TPMLimit != 2200 || response.WindowCostLimitCents != 333 || response.MaxSessions != 7 || !response.TLSFingerprintRotate {
		t.Fatalf("其他限制字段被误清: %+v", response)
	}
	if !response.CustomErrorCodesEnabled || len(response.CustomErrorCodes) != 1 || !response.PoolMode || !response.TempUnschedulableEnabled {
		t.Fatalf("错误策略字段被误清: %+v", response)
	}
	if response.ProxyBinding.Mode != "group" || response.ProxyBinding.ProxyGroupID == nil || *response.ProxyBinding.ProxyGroupID != group {
		t.Fatalf("代理绑定被误清: %+v", response.ProxyBinding)
	}
}

func TestAdminPoolAccounts_高级字段非法输入返回400且不写Store(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "RPM 负数", body: `{"rpm_limit":-1}`},
		{name: "TPM 超出 int64", body: `{"tpm_limit":9223372036854775808}`},
		{name: "窗口成本非整数", body: `{"window_cost_limit_cents":1.5}`},
		{name: "会话数超出 int32", body: `{"max_sessions":2147483648}`},
		{name: "刷新提前量负数", body: `{"refresh_lead_seconds":-1}`},
		{name: "过期时间格式错误", body: `{"expires_at":"2026/01/01"}`},
		{name: "布尔字段 null", body: `{"disable_cooling":null}`},
		{name: "错误码越界", body: `{"custom_error_codes":[99]}`},
		{name: "规则时长非正", body: `{"temp_unschedulable_rules":[{"rule_id":"busy","error_code":529,"duration_minutes":0}]}`},
		{name: "代理模式非法", body: `{"proxy_binding":{"mode":"random"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &adminPoolStoreStub{}
			rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
			}
			if store.updateFull != nil || len(store.audits) != 0 {
				t.Fatalf("非法请求触碰 store: update=%+v audits=%d", store.updateFull, len(store.audits))
			}
		})
	}
}
