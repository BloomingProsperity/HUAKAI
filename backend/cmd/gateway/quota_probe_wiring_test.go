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
	source := string(raw)
	for _, required := range []string{
		"quotaprobe.NewWorker(quotaprobe.WorkerConfig{",
		"Accounts: quotaprobe.NewPostgresAccountLister(pgPool)",
		"Vault:    credentialVault",
		"Fetcher:  quotaprobe.NewHTTPUsageFetcher(anthropicOAuthHTTPClient, accountProxyResolver)",
		"Store:    ratelimit.NewPostgresSessionWindowStore(pgPool)",
		"Settings: platformSettingsService",
		"quotaProbeWorker.Start(ctx)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("quota probe 生产接线缺少 %q", required)
		}
	}
}
