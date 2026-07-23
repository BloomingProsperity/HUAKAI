package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
)

func TestProviderAccountAdvancedFieldMirrors完全一致(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/src/features/accounts/advancedFields.json")
	if err != nil {
		// 前端已按 Owner 决定整体抛弃重写,mirror 文件暂缺;前端重建带回该文件后
		// 本测试自动恢复守护前后端高级字段规格一致(accountadvanced.Specs())。
		t.Skip("前端 mirror 文件暂缺(前端重写中),跳过前后端字段一致性校验")
	}
	var frontend []accountadvanced.FieldSpec
	if err := json.Unmarshal(raw, &frontend); err != nil {
		t.Fatalf("解析前端高级字段 mirror: %v", err)
	}
	if backend := accountadvanced.Specs(); !reflect.DeepEqual(frontend, backend) {
		t.Fatalf("前后端高级字段规格漂移\nfrontend=%+v\nbackend=%+v", frontend, backend)
	}
}

func TestProviderAccountAdvancedOpenAPI三类Schema字段齐全(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	for _, schema := range []string{"ProviderAccount", "ProviderAccountCreate", "ProviderAccountUpdate"} {
		block := openAPIComponentSchema(string(raw), schema)
		if block == "" {
			t.Fatalf("OpenAPI 缺 schema %s", schema)
		}
		for _, key := range accountadvanced.Keys() {
			if !schemaHasProperty(block, key) {
				t.Errorf("OpenAPI %s 缺高级字段 %s", schema, key)
			}
		}
	}
}

func TestProviderAccountTempRuleOpenAPI与严格写合同一致(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	source := string(raw)
	rule := openAPIComponentSchema(source, "ProviderAccountTempRule")
	if rule == "" {
		t.Fatal("OpenAPI 缺 ProviderAccountTempRule")
	}
	for _, field := range []string{
		"rule_id", "error_code", "keywords", "duration_minutes", "description",
		"client_status", "client_code", "message_mode", "client_message", "affect_health",
	} {
		if !schemaHasProperty(rule, field) {
			t.Errorf("ProviderAccountTempRule 缺字段 %s", field)
		}
	}
	for _, contract := range []string{
		"required: [rule_id, error_code, keywords, duration_minutes, message_mode, affect_health]",
		"pattern: '^[a-z][a-z0-9._-]{0,63}$'",
		"maxItems: 16",
		"maximum: 525600",
		"pattern: '^[a-z][a-z0-9_]{0,63}$'",
		"enum: [fixed, custom, upstream_safe]",
	} {
		if !strings.Contains(rule, contract) {
			t.Errorf("ProviderAccountTempRule 缺严格约束 %q", contract)
		}
	}
	for _, schema := range []string{"ProviderAccount", "ProviderAccountCreate", "ProviderAccountUpdate"} {
		block := openAPIComponentSchema(source, schema)
		property := textSection(t, block, "        temp_unschedulable_rules:", "        proxy_binding:")
		if !strings.Contains(property, "maxItems: 64") {
			t.Errorf("OpenAPI %s 未限制规则最多 64 条", schema)
		}
	}
	providerAccount := openAPIComponentSchema(source, "ProviderAccount")
	customCodes := textSection(t, providerAccount, "        custom_error_codes:", "        rate_limit_recovery:")
	if !strings.Contains(customCodes, "items: { type: integer, format: int32, minimum: 100, maximum: 599 }") {
		t.Error("ProviderAccount custom_error_codes 响应合同仍不是整数数组")
	}
}

func TestProviderAccountSubscriptionOpenAPI合同完整(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	source := string(raw)
	for schema, fields := range map[string][]string{
		"ProviderAccountSubscriptionProfile": {
			"vendor", "plan", "label", "raw_plan", "scope", "source", "trust",
			"verification", "status", "mapping_version", "first_observed_at", "observed_at", "changed_at",
		},
		"ProviderAccount":               {"subscription", "system_labels"},
		"ProviderAccountHealthSnapshot": {"subscription", "system_labels"},
		"AccountIntakePlanItem":         {"subscription", "system_labels"},
		"AccountIntakeExecutionItem":    {"subscription", "system_labels"},
		"AccountCredentialSubscriptionObservation": {
			"vendor", "plan", "raw_plan", "scope", "subject_ref", "workspace_ref",
			"source", "trust", "verification", "status", "mapping_version", "error_class",
		},
		"AccountCredentialMetadata": {"external_subject_id", "subscription"},
	} {
		block := openAPIComponentSchema(source, schema)
		if block == "" {
			t.Fatalf("OpenAPI 缺 schema %s", schema)
		}
		for _, field := range fields {
			if !schemaHasProperty(block, field) {
				t.Errorf("OpenAPI %s 缺套餐合同字段 %s", schema, field)
			}
		}
	}
	plan := openAPIComponentSchema(source, "AccountIntakePlan")
	if !strings.Contains(plan, "enum: [account-intake-v2]") {
		t.Error("账号导入套餐合同已变更，但 contract_version 未锁定 account-intake-v2")
	}
	listRoute := textSection(t, source, "  /admin/v1/provider-accounts:", "    post:")
	for _, parameter := range []string{
		"system_label", "subscription_vendor", "subscription_plan", "subscription_scope",
		"subscription_status", "subscription_source",
	} {
		if !strings.Contains(listRoute, "- name: "+parameter) {
			t.Errorf("账号列表 OpenAPI 缺套餐筛选参数 %s", parameter)
		}
	}
}

func TestAccountIntakeOpenAPI区分客户端来源和服务端来源(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	source := string(raw)
	wantClientEnum := "enum: [cli_import, json_import, csv_import, claude_setup_token]"
	for _, schema := range []string{"AccountIntakePlanRequest", "AccountIntakeExecuteRequest"} {
		block := openAPIComponentSchema(source, schema)
		if !strings.Contains(block, wantClientEnum) {
			t.Errorf("%s 未锁定客户端可选来源", schema)
		}
		for _, forbidden := range []string{
			"claude_cookie", "claude_setup_cookie", "codex_agent_identity",
			"crs_sync", "account_bundle", "oauth",
		} {
			if strings.Contains(textSection(t, block, "        source_kind:", "        default_vendor:"), forbidden) {
				t.Errorf("%s 暴露服务端来源 %s", schema, forbidden)
			}
		}
	}
	response := openAPIComponentSchema(source, "AccountIntakePlan")
	for _, serverSource := range []string{
		"claude_cookie", "claude_setup_cookie", "codex_agent_identity",
		"crs_sync", "account_bundle", "oauth",
	} {
		if !strings.Contains(response, serverSource) {
			t.Errorf("账号导入计划响应缺服务端来源 %s", serverSource)
		}
	}
}

func TestProviderAccountAdvancedSQL源覆盖全部存储列(t *testing.T) {
	raw, err := os.ReadFile("../../sql/queries/admin_provider_account_mutations.sql")
	if err != nil {
		t.Fatalf("读取账号 mutation SQL: %v", err)
	}
	generatedRaw, err := os.ReadFile("../../internal/db/admin/admin_provider_account_mutations.sql.go")
	if err != nil {
		t.Fatalf("读取 sqlc 生成码: %v", err)
	}
	compatRaw, err := os.ReadFile("../../internal/db/admin/admin_provider_account_mutation_compat.go")
	if err != nil {
		t.Fatalf("读取账号 mutation 稳定包装层: %v", err)
	}
	source := string(raw)
	generated := string(generatedRaw)
	compat := string(compatRaw)
	insertSource := textSection(t, source, "-- name: InsertProviderAccountRaw :one", "-- name: UpdateAdminProviderAccountRaw :one")
	updateSource := textSection(t, source, "-- name: UpdateAdminProviderAccountRaw :one", "-- name: UpdateProviderAccountEnabled :exec")
	insertGeneratedSQL := textSection(t, generated, "const insertProviderAccountRaw =", "type InsertProviderAccountRawParams struct")
	insertGeneratedArgs := textSection(t, generated, "func (q *Queries) InsertProviderAccountRaw", "const softDeleteProviderAccount =")
	updateGeneratedSQL := textSection(t, generated, "const updateAdminProviderAccountRaw =", "type UpdateAdminProviderAccountRawParams struct")
	updateGeneratedArgs := textSection(t, generated, "func (q *Queries) UpdateAdminProviderAccountRaw", "const updateProviderAccountEnabled =")

	type storageField struct {
		column   string
		arg      string
		goArg    string
		rawGoArg string
	}
	fieldsByKey := map[string][]storageField{
		"upstream_cost_ratio":        {{column: "upstream_cost_ratio", arg: "upstream_cost_ratio", goArg: "UpstreamCostRatio"}},
		"rpm_limit":                  {{column: "rpm_limit", arg: "rpm_limit", goArg: "RPMLimit", rawGoArg: "RpmLimit"}},
		"tpm_limit":                  {{column: "tpm_limit", arg: "tpm_limit", goArg: "TPMLimit", rawGoArg: "TpmLimit"}},
		"window_cost_limit_cents":    {{column: "window_cost_limit_cents", arg: "window_cost_limit_cents", goArg: "WindowCostLimitCents"}},
		"max_sessions":               {{column: "max_sessions", arg: "max_sessions", goArg: "MaxSessions"}},
		"disable_cooling":            {{column: "disable_cooling", arg: "disable_cooling", goArg: "DisableCooling"}},
		"refresh_lead_seconds":       {{column: "refresh_lead_seconds", arg: "refresh_lead_seconds", goArg: "RefreshLeadSeconds"}},
		"expires_at":                 {{column: "expires_at", arg: "expires_at", goArg: "ExpiresAt"}},
		"tls_fingerprint_rotate":     {{column: "tls_fingerprint_rotate", arg: "tls_fingerprint_rotate", goArg: "TLSFingerprintRotate", rawGoArg: "TlsFingerprintRotate"}},
		"custom_error_codes_enabled": {{column: "custom_error_codes_enabled", arg: "custom_error_codes_enabled", goArg: "CustomErrorCodesEnabled"}},
		"custom_error_codes":         {{column: "custom_error_codes", arg: "custom_error_codes", goArg: "CustomErrorCodes"}},
		"pool_mode":                  {{column: "pool_mode", arg: "pool_mode", goArg: "PoolMode"}},
		"temp_unschedulable_enabled": {{column: "temp_unschedulable_enabled", arg: "temp_unschedulable_enabled", goArg: "TempUnschedulableEnabled"}},
		"temp_unschedulable_rules":   {{column: "temp_unschedulable_rules", arg: "temp_unschedulable_rules", goArg: "TempUnschedulableRulesJSON", rawGoArg: "TempUnschedulableRules"}},
		"proxy_binding": {
			{column: "proxy_id", arg: "proxy_id", goArg: "ProxyID"},
			{column: "proxy_group_id", arg: "proxy_group_id", goArg: "ProxyGroupID"},
		},
	}
	for _, key := range accountadvanced.Keys() {
		fields, ok := fieldsByKey[key]
		if !ok {
			t.Fatalf("高级字段 %s 缺 SQL 存储映射守卫", key)
		}
		for _, field := range fields {
			rawGoArg := field.rawGoArg
			if rawGoArg == "" {
				rawGoArg = field.goArg
			}
			if !strings.Contains(insertSource, "\n    "+field.column+",") ||
				!strings.Contains(insertSource, "sqlc.narg("+field.arg+")") {
				t.Errorf("SQL 源 INSERT 缺字段或参数映射 %s", field.column)
			}
			if !strings.Contains(updateSource, "\n    "+field.column+" =") ||
				!strings.Contains(updateSource, "sqlc.narg("+field.arg+")") {
				t.Errorf("SQL 源 UPDATE 缺字段或参数映射 %s", field.column)
			}
			if !strings.Contains(insertGeneratedSQL, "\n    "+field.column+",") ||
				!strings.Contains(insertGeneratedArgs, "\n\t\targ."+rawGoArg+",") {
				t.Errorf("生成码 INSERT 缺列或 Go 参数映射 %s", field.column)
			}
			if !strings.Contains(updateGeneratedSQL, "\n    "+field.column+" =") ||
				!strings.Contains(updateGeneratedArgs, "\n\t\targ."+rawGoArg+",") {
				t.Errorf("生成码 UPDATE 缺列或 Go 参数映射 %s", field.column)
			}
			if !strings.Contains(compat, "arg."+field.goArg) || !strings.Contains(compat, rawGoArg+":") {
				t.Errorf("稳定包装层缺字段映射 %s", field.column)
			}
		}
	}
}

func textSection(t *testing.T, source, start, end string) string {
	t.Helper()
	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		t.Fatalf("契约文本缺起点 %q", start)
	}
	remainder := source[startIndex+len(start):]
	endIndex := strings.Index(remainder, end)
	if endIndex < 0 {
		t.Fatalf("契约文本缺终点 %q", end)
	}
	return source[startIndex : startIndex+len(start)+endIndex]
}

func openAPIComponentSchema(source, name string) string {
	lines := strings.Split(source, "\n")
	start := -1
	for index, line := range lines {
		if line == "    "+name+":" {
			start = index
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && strings.HasSuffix(line, ":") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func schemaHasProperty(block, key string) bool {
	needle := "        " + key + ":"
	for _, line := range strings.Split(block, "\n") {
		if line == needle || strings.HasPrefix(line, needle+" ") {
			return true
		}
	}
	return false
}
