package main

import (
	"os"
	"strings"
	"testing"
)

func TestProviderAccountRateLimitRecoveryIsInjectedIntoProductionRoutes(t *testing.T) {
	raw, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	source := string(raw)
	const wiring = "RateLimitRecovery: provideraccountrecovery.NewService(provideraccountrecovery.NewPostgresStore(d.pgPool), d.channelHealth),"
	if !strings.Contains(source, wiring) {
		t.Fatalf("production provider-account recovery wiring missing %q", wiring)
	}
}
