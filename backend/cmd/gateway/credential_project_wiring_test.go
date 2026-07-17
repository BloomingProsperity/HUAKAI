package main

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type routeProjectEnricherStub struct{}

func (routeProjectEnricherStub) Enrich(context.Context, string, []byte) (projectenrich.Result, error) {
	return projectenrich.Result{}, nil
}

func TestCredentialProjectDependenciesReachBothAdminRoutes(t *testing.T) {
	enricher := routeProjectEnricherStub{}
	store := &credentialstore.Store{}
	d := &deps{
		cfg:             &config.Config{},
		credentialStore: store,
		projectEnricher: enricher,
	}
	projectDeps := credentialProjectRouteDeps(d)
	if projectDeps.Store != store || projectDeps.Enricher != enricher {
		t.Fatalf("手动解析路由依赖未接线：store=%T enricher=%T", projectDeps.Store, projectDeps.Enricher)
	}
	acquisitionDeps := credentialAcquisitionRouteDeps(d)
	if acquisitionDeps.Credentials != store || acquisitionDeps.ProjectEnricher != enricher {
		t.Fatalf("采集 finalize 依赖未接线：credentials=%T enricher=%T", acquisitionDeps.Credentials, acquisitionDeps.ProjectEnricher)
	}
}

func TestCredentialProjectProductionWiringKeepsResolverInjection(t *testing.T) {
	raw, err := os.ReadFile("wiring.go")
	if err != nil {
		t.Fatalf("读取 wiring.go 失败：%v", err)
	}
	for name, pattern := range map[string]string{
		"采集与手动动作": `projectEnricher:\s+credentialProjectEnricher`,
		"刷新链":     `DefaultModeAdapterRegistryWithProjectResolverAndRuntimeOAuth\(antigravityProjectResolver, cfg\.VendorOAuth\)`,
	} {
		matched, matchErr := regexp.Match(pattern, raw)
		if matchErr != nil {
			t.Fatalf("%s 接线断言无效：%v", name, matchErr)
		}
		if !matched {
			t.Fatalf("production wiring 缺少%s的 project resolver 注入", name)
		}
	}
}
