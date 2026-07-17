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

func TestProviderAccountAdvancedSQL源覆盖全部存储列(t *testing.T) {
	raw, err := os.ReadFile("../../sql/queries/admin_provider_account_mutations.sql")
	if err != nil {
		t.Fatalf("读取账号 mutation SQL: %v", err)
	}
	generatedRaw, err := os.ReadFile("../../internal/db/admin/admin_provider_account_mutations.sql.go")
	if err != nil {
		t.Fatalf("读取手改 sqlc 生成码: %v", err)
	}
	source := string(raw)
	generated := string(generatedRaw)
	insertSource := textSection(t, source, "-- name: InsertProviderAccount :one", "-- name: UpdateAdminProviderAccount :one")
	updateSource := textSection(t, source, "-- name: UpdateAdminProviderAccount :one", "-- name: UpdateProviderAccountEnabled :exec")
	insertGeneratedSQL := textSection(t, generated, "const insertProviderAccount =", "type InsertProviderAccountParams struct")
	insertGeneratedArgs := textSection(t, generated, "func (q *Queries) InsertProviderAccount", "const updateAdminProviderAccount =")
	updateGeneratedSQL := textSection(t, generated, "const updateAdminProviderAccount =", "type UpdateAdminProviderAccountParams struct")
	updateGeneratedArgs := textSection(t, generated, "func (q *Queries) UpdateAdminProviderAccount", "const softDeleteProviderAccount =")

	type storageField struct {
		column string
		arg    string
		goArg  string
	}
	fieldsByKey := map[string][]storageField{
		"rpm_limit":                  {{column: "rpm_limit", arg: "rpm_limit", goArg: "RPMLimit"}},
		"tpm_limit":                  {{column: "tpm_limit", arg: "tpm_limit", goArg: "TPMLimit"}},
		"window_cost_limit_cents":    {{column: "window_cost_limit_cents", arg: "window_cost_limit_cents", goArg: "WindowCostLimitCents"}},
		"max_sessions":               {{column: "max_sessions", arg: "max_sessions", goArg: "MaxSessions"}},
		"disable_cooling":            {{column: "disable_cooling", arg: "disable_cooling", goArg: "DisableCooling"}},
		"refresh_lead_seconds":       {{column: "refresh_lead_seconds", arg: "refresh_lead_seconds", goArg: "RefreshLeadSeconds"}},
		"expires_at":                 {{column: "expires_at", arg: "expires_at", goArg: "ExpiresAt"}},
		"tls_fingerprint_rotate":     {{column: "tls_fingerprint_rotate", arg: "tls_fingerprint_rotate", goArg: "TLSFingerprintRotate"}},
		"custom_error_codes_enabled": {{column: "custom_error_codes_enabled", arg: "custom_error_codes_enabled", goArg: "CustomErrorCodesEnabled"}},
		"custom_error_codes":         {{column: "custom_error_codes", arg: "custom_error_codes", goArg: "CustomErrorCodes"}},
		"pool_mode":                  {{column: "pool_mode", arg: "pool_mode", goArg: "PoolMode"}},
		"temp_unschedulable_enabled": {{column: "temp_unschedulable_enabled", arg: "temp_unschedulable_enabled", goArg: "TempUnschedulableEnabled"}},
		"temp_unschedulable_rules":   {{column: "temp_unschedulable_rules", arg: "temp_unschedulable_rules", goArg: "TempUnschedulableRulesJSON"}},
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
			if !strings.Contains(insertSource, "\n    "+field.column+",") ||
				!strings.Contains(insertSource, "sqlc.narg("+field.arg+")") {
				t.Errorf("SQL 源 INSERT 缺字段或参数映射 %s", field.column)
			}
			if !strings.Contains(updateSource, "\n    "+field.column+" =") ||
				!strings.Contains(updateSource, "sqlc.narg("+field.arg+")") {
				t.Errorf("SQL 源 UPDATE 缺字段或参数映射 %s", field.column)
			}
			if !strings.Contains(insertGeneratedSQL, "\n    "+field.column+",") ||
				!strings.Contains(insertGeneratedArgs, "\n\t\targ."+field.goArg+",") {
				t.Errorf("手改生成码 INSERT 缺列或 Go 参数映射 %s", field.column)
			}
			if !strings.Contains(updateGeneratedSQL, "\n    "+field.column+" =") ||
				!strings.Contains(updateGeneratedArgs, "\n\t\targ."+field.goArg+",") {
				t.Errorf("手改生成码 UPDATE 缺列或 Go 参数映射 %s", field.column)
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
