package main

import (
	"os"
	"strings"
	"testing"
)

func TestGatewayWiringInjectsAndStartsQuotaProbe(t *testing.T) {
	raw, err := os.ReadFile("wiring.go")
	if err != nil {
		t.Fatalf("读取 wiring.go: %v", err)
	}
	source := strings.Join(strings.Fields(string(raw)), " ")
	for _, required := range []string{
		"quotaprobe.NewWorker(quotaprobe.WorkerConfig{",
		"Accounts: quotaprobe.NewPostgresAccountLister(pgPool)",
		"Vault:    credentialVault",
		"Fetcher:  quotaprobe.NewHTTPUsageFetcher(anthropicOAuthHTTPClient, accountProxyResolver)",
		"Store:    ratelimit.NewPostgresSessionWindowStore(pgPool)",
		"Settings: platformSettingsService",
		"LeaderLease: workerlease.NewPostgres(pgPool, quotaProbeLeaderLockKey, \"quota_probe\")",
		"workerCtx, cancelWorkers := context.WithCancel(context.Background())",
		"rt.cancelWorkers = cancelWorkers",
		"name: \"proxy health worker\"",
		"wait: proxyHealthWorker.Wait",
		"name: \"TLS profile health worker\"",
		"wait: tlsProfileHealthWorker.Wait",
		"name: \"window cost worker\"",
		"wait: windowCostWorker.Wait",
		"quotaProbeWorker.Start(workerCtx)",
		"name: \"quota probe worker\"",
		"wait: quotaProbeWorker.Wait",
	} {
		required = strings.Join(strings.Fields(required), " ")
		if !strings.Contains(source, required) {
			t.Fatalf("quota probe 生产接线缺少 %q", required)
		}
	}
	if strings.Contains(source, "quotaProbeWorker.Start(ctx)") {
		t.Fatal("quota probe 不得继续绑定进程信号 ctx，否则会在 HTTP 排空前停止")
	}
}
