package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

func qStrPtr(s string) *string { return &s }

// qnum 直接构造一个有效的 pgtype.Numeric(整数,Exp=0)。
func qnum(i int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(i), Exp: 0, Valid: true}
}

func fakeQuotaPolicies() []dbquota.QuotaPolicy {
	return []dbquota.QuotaPolicy{
		{
			TenantID: 7, ID: 1, ScopeKind: "tenant", ScopeID: "*", Metric: "tokens",
			WindowKind: "rolling", WindowSeconds: 3600,
			LimitValue: qnum(1000000), BurstValue: qnum(5000),
			Mode: "hard", Priority: 10, Enabled: true,
			CreatedByActor:      qStrPtr("SENTINEL-ACTOR-must-not-leak"),
			LastModifiedByActor: qStrPtr("SENTINEL-MODIFIER-must-not-leak"),
		},
		{
			TenantID: 7, ID: 2, ScopeKind: "api_key", ScopeID: "k-99", Metric: "requests",
			WindowKind: "fixed", WindowSeconds: 60,
			LimitValue: qnum(60), Mode: "soft", Priority: 5, Enabled: false,
		},
	}
}

func TestQuotaPolicyListSpec(t *testing.T) {
	deps := QuotaPolicyListDeps{
		List: func(_ context.Context, params dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error) {
			if params.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", params.TenantID)
			}
			if params.PageLimit != quotaPolicyListLimit {
				t.Fatalf("PageLimit 应为 quotaPolicyListLimit=%d, got %d", quotaPolicyListLimit, params.PageLimit)
			}
			return fakeQuotaPolicies(), nil
		},
	}
	spec := QuotaPolicyListSpec(deps)

	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["policy_count"].(int) != 2 {
		t.Fatalf("policy_count 应 2, got %v", res.Summary["policy_count"])
	}
	if res.Summary["enabled_count"].(int) != 1 {
		t.Fatalf("enabled_count 应 1, got %v", res.Summary["enabled_count"])
	}

	items := res.Summary["items"].([]map[string]any)
	p0 := items[0]
	if p0["scope_kind"] != "tenant" || p0["metric"] != "tokens" || p0["mode"] != "hard" {
		t.Fatalf("policy[0] 投影错: %v", p0)
	}
	// 数值 → float64。
	if p0["limit_value"].(float64) != 1000000 {
		t.Fatalf("limit_value 应 1000000(float64), got %v(%T)", p0["limit_value"], p0["limit_value"])
	}
	if p0["burst_value"].(float64) != 5000 {
		t.Fatalf("burst_value 应 5000, got %v", p0["burst_value"])
	}
	// 第二条无 BurstValue(零值 Numeric.Valid=false)→ nil。
	if items[1]["burst_value"] != nil {
		t.Fatalf("policy[1] 无 burst 应 nil, got %v", items[1]["burst_value"])
	}

	// 绝不投 actor 标识 / tenant_id。
	for _, leakKey := range []string{"created_by_actor", "last_modified_by_actor", "tenant_id"} {
		if _, has := p0[leakKey]; has {
			t.Fatalf("泄露字段 %q: %v", leakKey, p0)
		}
	}

	// §14 no-leak:整个 Summary 序列化后不含 actor 哨兵。
	blob, _ := json.Marshal(res.Summary)
	for _, sentinel := range []string{"SENTINEL-ACTOR", "SENTINEL-MODIFIER"} {
		if strings.Contains(string(blob), sentinel) {
			t.Fatalf("actor 标识泄露: 命中哨兵 %q\n%s", sentinel, blob)
		}
	}
}

func TestQuotaPolicyListNilDep(t *testing.T) {
	_, err := QuotaPolicyListSpec(QuotaPolicyListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestQuotaPolicyListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := QuotaPolicyListDeps{
		List: func(_ context.Context, _ dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error) {
			return nil, sentinel
		},
	}
	_, err := QuotaPolicyListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
