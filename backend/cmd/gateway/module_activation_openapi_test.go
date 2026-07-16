package main

import (
	"os"
	"strings"
	"testing"
)

func TestModuleActivationOpenAPIContractIsFullyWired(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI: %v", err)
	}
	spec := string(raw)

	for _, path := range []string{
		"/admin/v1/modules",
		"/v1/admin/modules",
		"/v1/hermes/context",
	} {
		block := openAPIIndentedBlock(spec, "  "+path+":", "\n  /")
		if !strings.Contains(block, "$ref: '#/components/schemas/ModulesResponse'") {
			t.Fatalf("OpenAPI %s 未引用 ModulesResponse", path)
		}
	}

	for _, schema := range []string{
		"ModuleActivationEndpoint",
		"ModuleActivationSnapshot",
		"ModuleProbeResult",
		"ModuleCatalogOverlay",
		"ModuleView",
		"ModulesResponse",
	} {
		if !strings.Contains(spec, "\n    "+schema+":\n") {
			t.Fatalf("OpenAPI 缺 schema %s", schema)
		}
	}

	activation := openAPIIndentedBlock(spec, "    ModuleActivationSnapshot:", "\n    ModuleProbeResult:")
	for _, property := range []string{
		"declared",
		"constructed",
		"injected",
		"active",
		"shared_safe",
		"observable",
		"verified",
		"backend",
		"mode",
		"traffic_percent",
		"endpoints",
	} {
		if !strings.Contains(activation, "\n        "+property+":") {
			t.Fatalf("ModuleActivationSnapshot 缺字段 %s", property)
		}
	}
}

func openAPIIndentedBlock(spec, startMarker, nextMarker string) string {
	start := strings.Index(spec, "\n"+startMarker+"\n")
	if start < 0 {
		return ""
	}
	block := spec[start+1:]
	if end := strings.Index(block[len(startMarker)+1:], nextMarker); end >= 0 {
		return block[:len(startMarker)+1+end]
	}
	return block
}
