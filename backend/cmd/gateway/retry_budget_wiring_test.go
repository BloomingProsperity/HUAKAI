package main

import (
	"strings"
	"testing"
)

func clearTenantRetryBudgetEnv(t *testing.T) {
	t.Helper()
	t.Setenv(tenantRetryBudgetEnv, "")
	t.Setenv(tenantRetryWindowEnv, "")
}

func TestWiring_TenantRetryBudgetDefaultDisabled(t *testing.T) {
	clearTenantRetryBudgetEnv(t)

	budget, err := loadTenantRetryBudgetFromEnv()
	if err != nil {
		t.Fatalf("loadTenantRetryBudgetFromEnv default: %v", err)
	}
	for i := 0; i < 20; i++ {
		if !budget.Allow(7) {
			t.Fatalf("default budget denied retry %d; HUAKAI_TENANT_RETRY_BUDGET unset must be unlimited", i+1)
		}
	}
}

func TestWiring_TenantRetryBudgetEnvConfiguresLimit(t *testing.T) {
	clearTenantRetryBudgetEnv(t)
	t.Setenv(tenantRetryBudgetEnv, "2")
	t.Setenv(tenantRetryWindowEnv, "1m")

	budget, err := loadTenantRetryBudgetFromEnv()
	if err != nil {
		t.Fatalf("loadTenantRetryBudgetFromEnv configured: %v", err)
	}
	if !budget.Allow(7) || !budget.Allow(7) {
		t.Fatal("first two configured retries should be allowed")
	}
	if budget.Allow(7) {
		t.Fatal("third configured retry should be denied")
	}
}

func TestWiring_TenantRetryBudgetRejectsInvalidEnv(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		value string
	}{
		{name: "budget text", env: tenantRetryBudgetEnv, value: "two"},
		{name: "budget negative", env: tenantRetryBudgetEnv, value: "-1"},
		{name: "window text", env: tenantRetryWindowEnv, value: "soon"},
		{name: "window zero", env: tenantRetryWindowEnv, value: "0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearTenantRetryBudgetEnv(t)
			t.Setenv(tc.env, tc.value)

			_, err := loadTenantRetryBudgetFromEnv()
			if err == nil {
				t.Fatal("expected invalid retry budget env to fail loud")
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Fatalf("err=%v want env name %s", err, tc.env)
			}
		})
	}
}
