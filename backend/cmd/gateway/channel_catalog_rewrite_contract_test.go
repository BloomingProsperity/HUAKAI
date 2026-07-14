package main

import (
	"os"
	"strings"
	"testing"
)

var channelRewriteFields = []struct {
	jsonKey string
	goName  string
}{
	{jsonKey: "body_param_strips", goName: "BodyParamStrips"},
	{jsonKey: "param_override", goName: "ParamOverride"},
	{jsonKey: "sensitive_words", goName: "SensitiveWords"},
}

func TestChannelCatalogRewriteOpenAPI三类Schema字段齐全(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	for _, schema := range []string{
		"AdminChannelCatalogItem",
		"AdminChannelCatalogCreateRequest",
		"AdminChannelCatalogUpdateRequest",
	} {
		block := openAPIComponentSchema(string(raw), schema)
		if block == "" {
			t.Fatalf("OpenAPI 缺 schema %s", schema)
		}
		for _, field := range channelRewriteFields {
			if !schemaHasProperty(block, field.jsonKey) {
				t.Errorf("OpenAPI %s 缺字段 %s", schema, field.jsonKey)
			}
		}
	}
}

func TestChannelCatalogRewriteSQL源与手改生成码映射齐全(t *testing.T) {
	sourceRaw, err := os.ReadFile("../../sql/queries/admin_channel_catalog_mutations.sql")
	if err != nil {
		t.Fatalf("读取渠道 mutation SQL: %v", err)
	}
	generatedRaw, err := os.ReadFile("../../internal/db/admin/admin_channel_catalog_mutations.sql.go")
	if err != nil {
		t.Fatalf("读取渠道手改生成码: %v", err)
	}
	listSourceRaw, err := os.ReadFile("../../sql/queries/admin_provider_channel_catalog.sql")
	if err != nil {
		t.Fatalf("读取渠道 list SQL: %v", err)
	}
	listGeneratedRaw, err := os.ReadFile("../../internal/db/admin/admin_provider_channel_catalog.sql.go")
	if err != nil {
		t.Fatalf("读取渠道 list 手改生成码: %v", err)
	}

	source := string(sourceRaw)
	generated := string(generatedRaw)
	insertSource := textSection(t, source, "-- name: CreateChannel :one", "-- name: UpdateChannel :one")
	updateSource := textSection(t, source, "-- name: UpdateChannel :one", "-- name: SoftDeleteChannel :one")
	insertGeneratedSQL := textSection(t, generated, "const createChannel =", "type CreateChannelParams struct")
	insertGeneratedArgs := textSection(t, generated, "func (q *Queries) CreateChannel", "const softDeleteChannel =")
	updateGeneratedSQL := textSection(t, generated+"\n// 文件结束", "const updateChannel =", "// 文件结束")
	listSource := textSection(t, string(listSourceRaw), "-- name: ListAdminChannelsByTenant :many", "-- name: CreateChannelTestTemplate :one")
	listGenerated := textSection(t, string(listGeneratedRaw), "const listAdminChannelsByTenant =", "const listAdminProvidersByTenant =")
	getSource := textSection(t, string(listSourceRaw), "-- name: GetAdminChannel :one", "-- name: CreateChannelTestTemplate :one")
	getGenerated := textSection(t, string(listGeneratedRaw), "const getAdminChannel =", "const getChannelTestTemplate =")
	for _, field := range channelRewriteFields {
		if !strings.Contains(insertSource, "\n    "+field.jsonKey+",") ||
			!strings.Contains(insertSource, "sqlc.narg("+field.jsonKey+")") {
			t.Errorf("SQL 源 INSERT 缺列或参数映射 %s", field.jsonKey)
		}
		if !strings.Contains(updateSource, "\n    "+field.jsonKey+" = CASE") ||
			!strings.Contains(updateSource, "sqlc.arg(set_"+field.jsonKey+")") ||
			!strings.Contains(updateSource, "sqlc.narg("+field.jsonKey+")") {
			t.Errorf("SQL 源 UPDATE 缺 presence 或值映射 %s", field.jsonKey)
		}
		if !strings.Contains(insertGeneratedSQL, "\n    "+field.jsonKey+",") ||
			!strings.Contains(insertGeneratedArgs, "\n\t\targ."+field.goName+",") {
			t.Errorf("手改生成码 INSERT 缺列或 Go 参数映射 %s", field.jsonKey)
		}
		if !strings.Contains(updateGeneratedSQL, "\n    "+field.jsonKey+" = CASE") ||
			!strings.Contains(updateGeneratedSQL, "arg.Set"+field.goName) ||
			!strings.Contains(updateGeneratedSQL, "arg."+field.goName) {
			t.Errorf("手改生成码 UPDATE 缺 presence 或值映射 %s", field.jsonKey)
		}
		if !strings.Contains(listSource, "\n    "+field.jsonKey+",") ||
			!strings.Contains(listGenerated, "&i."+field.goName) {
			t.Errorf("list SELECT/Scan 缺回显映射 %s", field.jsonKey)
		}
		if !strings.Contains(getSource, "\n    "+field.jsonKey+",") ||
			!strings.Contains(getGenerated, "&i."+field.goName) {
			t.Errorf("get SELECT/Scan 缺回显映射 %s", field.jsonKey)
		}
	}
	for _, snippet := range []string{"tenant_id = sqlc.arg(tenant_id)", "id = sqlc.arg(id)", "deleted_at IS NULL"} {
		if !strings.Contains(getSource, snippet) {
			t.Errorf("get SQL 缺租户、主键或软删围栏片段 %q", snippet)
		}
	}
}
