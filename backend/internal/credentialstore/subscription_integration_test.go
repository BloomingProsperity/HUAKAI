//go:build integration_pg

package credentialstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func TestSubscriptionProjectionCreateRefreshRotateAndConflict(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "subscription-lifecycle")
	t.Cleanup(func() { cleanupSubscriptionFixture(t, context.Background(), pool, fixture) })
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())

	plus := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "plus", subscriptionprofile.SourceIDTokenClaim,
		subscriptionprofile.TrustUnverifiedJWT, subscriptionprofile.VerificationUnverified,
		"subject-1", "account-1",
	)
	created, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"access-1","refresh_token":"refresh-1","chatgpt_plan_type":"plus"}`),
		ActorID: "owner", Subscription: plus,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Subscription.Label() != "openai:plus" || created.Subscription.Status != subscriptionprofile.StatusObserved {
		t.Fatalf("创建返回套餐=%+v", created.Subscription)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "plus", subscriptionprofile.StatusObserved, subscriptionprofile.SourceIDTokenClaim)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 1)

	record, err := store.getRecord(ctx, fixture.tenantID, fixture.providerAccountID, created.ID, true)
	if err != nil {
		t.Fatalf("读取刷新凭据: %v", err)
	}
	if err := store.SaveRefreshSuccess(ctx, record,
		[]byte(`{"access_token":"access-2","refresh_token":"refresh-2"}`),
		time.Now().UTC().Add(time.Hour), "refresh_succeeded"); err != nil {
		t.Fatalf("SaveRefreshSuccess: %v", err)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "plus", subscriptionprofile.StatusObserved, subscriptionprofile.SourceIDTokenClaim)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 1)

	pro := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "pro", subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.TrustIssuerResponse, subscriptionprofile.VerificationIssuerResponse,
		"subject-1", "account-1",
	)
	rotated, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-3","refresh_token":"refresh-3","chatgpt_plan_type":"pro"}`),
		ActorID: "owner", Subscription: pro,
	})
	if err != nil {
		t.Fatalf("Rotate pro: %v", err)
	}
	if rotated.Subscription.Label() != "openai:pro" {
		t.Fatalf("强证据升级返回=%+v", rotated.Subscription)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "pro", subscriptionprofile.StatusObserved, subscriptionprofile.SourceOAuthResponse)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 2)

	weakerSame := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "pro", subscriptionprofile.SourceImportPayload,
		subscriptionprofile.TrustImported, subscriptionprofile.VerificationUnverified,
		"subject-1", "account-1",
	)
	if _, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-same","refresh_token":"refresh-same","chatgpt_plan_type":"pro"}`),
		ActorID: "owner", Subscription: weakerSame,
	}); err != nil {
		t.Fatalf("Rotate same-plan weaker evidence: %v", err)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "pro", subscriptionprofile.StatusObserved, subscriptionprofile.SourceOAuthResponse)
	assertSubscriptionTrust(t, ctx, pool, fixture, subscriptionprofile.TrustIssuerResponse)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 3)

	weakerPlus := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "plus", subscriptionprofile.SourceImportPayload,
		subscriptionprofile.TrustImported, subscriptionprofile.VerificationUnverified,
		"subject-1", "account-1",
	)
	conflicted, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-4","refresh_token":"refresh-4","chatgpt_plan_type":"plus"}`),
		ActorID: "owner", Subscription: weakerPlus,
	})
	if err != nil {
		t.Fatalf("Rotate weaker conflict: %v", err)
	}
	if conflicted.Subscription.Plan != "pro" || conflicted.Subscription.Status != subscriptionprofile.StatusConflict ||
		conflicted.Subscription.ErrorClass != "weaker_subscription_evidence_conflict" {
		t.Fatalf("弱证据不应覆盖强证据：%+v", conflicted.Subscription)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "pro", subscriptionprofile.StatusConflict, subscriptionprofile.SourceOAuthResponse)
	assertSubscriptionTrust(t, ctx, pool, fixture, subscriptionprofile.TrustIssuerResponse)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 4)
}

func TestSubscriptionRefreshUsesFreshIssuerFieldWhenIDTokenUnchanged(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "subscription-refresh-evidence")
	t.Cleanup(func() { cleanupSubscriptionFixture(t, context.Background(), pool, fixture) })
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())

	plus := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "plus", subscriptionprofile.SourceIDTokenClaim,
		subscriptionprofile.TrustUnverifiedJWT, subscriptionprofile.VerificationUnverified,
		"subject-refresh", "",
	)
	created, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"access-old","refresh_token":"refresh-old","id_token":"same-id","chatgpt_plan_type":"plus"}`),
		ActorID: "owner", Subscription: plus,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	record, err := store.getRecord(ctx, fixture.tenantID, fixture.providerAccountID, created.ID, true)
	if err != nil {
		t.Fatalf("读取刷新凭据: %v", err)
	}
	if err := store.SaveRefreshSuccess(ctx, record,
		[]byte(`{"access_token":"access-new","refresh_token":"refresh-new","id_token":"same-id","chatgpt_plan_type":"pro"}`),
		time.Now().UTC().Add(time.Hour), "refresh_succeeded"); err != nil {
		t.Fatalf("SaveRefreshSuccess: %v", err)
	}
	assertSubscriptionState(t, ctx, pool, fixture, "pro", subscriptionprofile.StatusObserved, subscriptionprofile.SourceCredentialRefresh)
	assertSubscriptionTrust(t, ctx, pool, fixture, subscriptionprofile.TrustIssuerResponse)
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 2)
}

func TestSubscriptionRepeatedStrongObservationRefreshesFreshnessOnly(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "subscription-repeat-freshness")
	t.Cleanup(func() { cleanupSubscriptionFixture(t, context.Background(), pool, fixture) })
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())

	pro := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorOpenAI, "pro", subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.TrustIssuerResponse, subscriptionprofile.VerificationIssuerResponse,
		"subject-repeat", "account-repeat",
	)
	created, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"access-old","refresh_token":"refresh-old","chatgpt_plan_type":"pro"}`),
		ActorID: "owner", Subscription: pro,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	baseline := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
		UPDATE provider_account_subscription_states
		SET first_observed_at=$3, observed_at=$3, changed_at=$3
		WHERE tenant_id=$1 AND provider_account_id=$2`,
		fixture.tenantID, fixture.providerAccountID, baseline,
	); err != nil {
		t.Fatalf("设置确定性时间基线: %v", err)
	}

	if _, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-new","refresh_token":"refresh-new","chatgpt_plan_type":"pro"}`),
		ActorID: "owner", Subscription: pro,
	}); err != nil {
		t.Fatalf("Rotate same strong plan: %v", err)
	}

	var observedAt, changedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT observed_at, changed_at
		FROM provider_account_subscription_states
		WHERE tenant_id=$1 AND provider_account_id=$2`,
		fixture.tenantID, fixture.providerAccountID,
	).Scan(&observedAt, &changedAt); err != nil {
		t.Fatalf("读取套餐时间轴: %v", err)
	}
	if !observedAt.After(baseline) {
		t.Fatalf("重复有效观测必须刷新 observed_at：got=%s baseline=%s", observedAt, baseline)
	}
	if !changedAt.Equal(baseline) {
		t.Fatalf("套餐语义未变化时不得刷新 changed_at：got=%s baseline=%s", changedAt, baseline)
	}
	assertSubscriptionObservationCount(t, ctx, pool, fixture, 2)
}

func TestSubscriptionWriteFailureRollsBackCredentialAndLog(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "subscription-rollback")
	t.Cleanup(func() { cleanupSubscriptionFixture(t, context.Background(), pool, fixture) })
	cleanupRejector := installSubscriptionInsertRejector(t, ctx, pool)
	t.Cleanup(cleanupRejector)

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	_, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"access-rollback","refresh_token":"refresh-rollback","chatgpt_plan_type":"plus"}`),
		ActorID: "owner",
	})
	if err == nil {
		t.Fatal("套餐观测写入失败时 Create 必须失败")
	}
	if got := countCredentialRows(t, ctx, pool, fixture.tenantID, fixture.providerAccountID); got != 0 {
		t.Fatalf("事务失败后残留 %d 条凭据", got)
	}
	for table, query := range map[string]string{
		"subscription observations": `SELECT count(*) FROM provider_account_subscription_observations WHERE tenant_id=$1 AND provider_account_id=$2`,
		"subscription states":       `SELECT count(*) FROM provider_account_subscription_states WHERE tenant_id=$1 AND provider_account_id=$2`,
		"credential logs":           `SELECT count(*) FROM credential_audit_events WHERE tenant_id=$1 AND provider_account_id=$2`,
	} {
		var count int64
		if scanErr := pool.QueryRow(ctx, query, fixture.tenantID, fixture.providerAccountID).Scan(&count); scanErr != nil {
			t.Fatalf("查询 %s: %v", table, scanErr)
		}
		if count != 0 {
			t.Fatalf("事务失败后 %s 残留 %d 条", table, count)
		}
	}
}

func assertSubscriptionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture credentialAuditTxFixture, plan, status, source string) {
	t.Helper()
	var gotPlan, gotStatus, gotSource string
	if err := pool.QueryRow(ctx, `
		SELECT normalized_plan, state_status, source_type
		FROM provider_account_subscription_states
		WHERE tenant_id=$1 AND provider_account_id=$2`,
		fixture.tenantID, fixture.providerAccountID,
	).Scan(&gotPlan, &gotStatus, &gotSource); err != nil {
		t.Fatalf("查询套餐当前投影: %v", err)
	}
	if gotPlan != plan || gotStatus != status || gotSource != source {
		t.Fatalf("套餐当前投影=(%s,%s,%s)，期望=(%s,%s,%s)", gotPlan, gotStatus, gotSource, plan, status, source)
	}
}

func assertSubscriptionObservationCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture credentialAuditTxFixture, want int64) {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM provider_account_subscription_observations
		WHERE tenant_id=$1 AND provider_account_id=$2`, fixture.tenantID, fixture.providerAccountID,
	).Scan(&count); err != nil {
		t.Fatalf("统计套餐观测历史: %v", err)
	}
	if count != want {
		t.Fatalf("套餐观测历史=%d，期望 %d", count, want)
	}
}

func assertSubscriptionTrust(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture credentialAuditTxFixture, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `
SELECT trust_level
FROM provider_account_subscription_states
WHERE tenant_id=$1 AND provider_account_id=$2`, fixture.tenantID, fixture.providerAccountID).Scan(&got); err != nil {
		t.Fatalf("读取套餐证据等级: %v", err)
	}
	if got != want {
		t.Fatalf("套餐 trust=%q，期望 %q", got, want)
	}
}

func cleanupSubscriptionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture credentialAuditTxFixture) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM provider_account_subscription_states WHERE tenant_id=$1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `ALTER TABLE provider_account_subscription_observations DISABLE TRIGGER provider_account_subscription_observations_append_only`)
	_, _ = pool.Exec(ctx, `DELETE FROM provider_account_subscription_observations WHERE tenant_id=$1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `ALTER TABLE provider_account_subscription_observations ENABLE TRIGGER provider_account_subscription_observations_append_only`)
	cleanupCredentialAuditTxFixture(t, ctx, pool, fixture)
}

func installSubscriptionInsertRejector(t *testing.T, ctx context.Context, pool *pgxpool.Pool) func() {
	t.Helper()
	suffix := strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "_")
	fn := pgx.Identifier{"public", "huakai_test_reject_subscription_" + suffix}.Sanitize()
	trigger := pgx.Identifier{"huakai_test_reject_subscription_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'forced subscription insert failure';
		END;
		$$`, fn)); err != nil {
		t.Fatalf("创建套餐拒绝函数: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON provider_account_subscription_observations
		FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, fn)); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
		t.Fatalf("创建套餐拒绝触发器: %v", err)
	}
	return func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON provider_account_subscription_observations`, trigger))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	}
}
