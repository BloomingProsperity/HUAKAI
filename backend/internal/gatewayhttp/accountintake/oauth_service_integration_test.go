//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

type oauthIntakeTestExchanger struct {
	candidate credentialacq.CredentialCandidate
}

func (e oauthIntakeTestExchanger) StartOAuthFlow(
	ctx context.Context,
	store *credentialacq.PostgresSessionStore,
	in credentialacq.StartInput,
	cfg credentialacq.OAuthClientConfig,
) (credentialacq.OAuthStartResult, error) {
	return credentialacq.StartOAuthFlow(ctx, store, in, cfg)
}

func (e oauthIntakeTestExchanger) ExchangeOAuthCode(context.Context, credentialacq.Session, string) (credentialacq.CredentialCandidate, error) {
	candidate := e.candidate
	candidate.Payload = append([]byte(nil), candidate.Payload...)
	return candidate, nil
}

func TestOAuthAccountIntakeCreatesAccountAfterAuthorization(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='grok_chat' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("oauth-intake-test", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentialacq.NewExchangerRegistry()
	observation := subscriptionprofile.FromRaw(
		subscriptionprofile.VendorGrok,
		"supergrok",
		subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.TrustIssuerResponse,
		subscriptionprofile.VerificationIssuerResponse,
		"subject-"+seed.suffix,
		"",
	)
	if err := registry.RegisterExchanger("grok/xai_oauth", oauthIntakeTestExchanger{candidate: credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		Payload:              []byte(`{"access_token":"oauth-access-token","refresh_token":"oauth-refresh-token"}`),
		ExternalAccountID:    "grok-account-" + seed.suffix,
		ExternalSubjectID:    "subject-" + seed.suffix,
		ExternalAccountEmail: "owner-" + seed.suffix + "@example.com",
		AccountIDSource:      "oauth_token_response",
		Subscription:         observation,
	}}); err != nil {
		t.Fatal(err)
	}

	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	staged := NewStagedStore(pool, keys)
	service := NewOAuthService(newAccountIntakeService(t, pool), staged, sessions, registry, nil)
	start, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "集成测试 OAuth 导入",
		RedirectURI: "http://127.0.0.1:3000/admin/v1/account-imports/oauth/callback",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "oauth-" + seed.suffix, AccountType: "oauth",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.Session.ProviderAccountID != 0 || !credentialacq.IsAccountIntakeSession(start.Session) {
		t.Fatalf("授权前流程未保持未绑定账号状态：%+v", start.Session)
	}
	var accountCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 {
		t.Fatalf("授权前不应创建占位账号，实际=%d", accountCount)
	}
	var nullableAccountID *int64
	if err := pool.QueryRow(ctx, `SELECT provider_account_id FROM credential_acquisition_flow_sessions WHERE id=$1::uuid`, start.Session.ID).Scan(&nullableAccountID); err != nil {
		t.Fatal(err)
	}
	if nullableAccountID != nil {
		t.Fatalf("账号创建型 OAuth 会话必须写入 NULL，实际=%v", *nullableAccountID)
	}

	if _, err := service.CallbackForActor(ctx, start.Session.ID, start.State, "code", seed.tenantID, "other-owner", "platform_admin"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("其他操作者不应消费流程：%v", err)
	}
	if _, err := service.Callback(ctx, start.Session.ID, "wrong-state", "code"); !errors.Is(err, credentialacq.ErrStateMismatch) {
		t.Fatalf("错误 state 应被拒绝：%v", err)
	}
	stillStarted, err := sessions.Get(ctx, start.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillStarted.Status != credentialacq.StatusStarted {
		t.Fatalf("错误 state 不应销毁有效流程，状态=%s", stillStarted.Status)
	}

	planned, err := service.CallbackForActor(
		ctx, start.Session.ID, start.State, "authorization-code",
		seed.tenantID, "platform-owner", "platform_admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Plan.Items) != 1 || planned.Plan.Items[0].Action != "create" {
		t.Fatalf("预检结果=%+v，期望单账号 create", planned.Plan.Items)
	}
	confirmations := []string{}
	if planned.Plan.Items[0].MixedChannelRisk != nil && planned.Plan.Items[0].MixedChannelRisk.HighRisk {
		confirmations = append(confirmations, "confirm_mixed_channel_risk")
	}
	result, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		Confirmations: confirmations, ActorID: "platform-owner", ActorRole: "platform_admin",
		Reason: "确认 OAuth 导入",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Created != 1 || result.Summary.Failed != 0 || len(result.Items) != 1 {
		t.Fatalf("执行结果=%+v", result)
	}
	accountID := result.Items[0].ProviderAccountID
	credentialID := result.Items[0].AccountCredentialID
	if accountID <= 0 || credentialID <= 0 || !result.Items[0].ChannelHealthInitialized {
		t.Fatalf("账号、凭据或健康状态未闭环：%+v", result.Items[0])
	}

	var credentialCount, healthCount, completedLogs int
	var externalAccountID, externalSubjectID string
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int, min(external_account_id), min(external_subject_id)
FROM account_credentials
WHERE tenant_id=$1 AND provider_account_id=$2 AND id=$3`, seed.tenantID, accountID, credentialID).Scan(
		&credentialCount, &externalAccountID, &externalSubjectID,
	); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 || externalAccountID != "grok-account-"+seed.suffix || externalSubjectID != "subject-"+seed.suffix {
		t.Fatalf("凭据身份投影不完整：count=%d account=%q subject=%q", credentialCount, externalAccountID, externalSubjectID)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1 AND provider_account_id=$2`, seed.tenantID, accountID).Scan(&healthCount); err != nil {
		t.Fatal(err)
	}
	if healthCount != 1 {
		t.Fatalf("健康状态行=%d，期望 1", healthCount)
	}
	var plan, status string
	if err := pool.QueryRow(ctx, `
SELECT normalized_plan, state_status
FROM provider_account_subscription_states
WHERE tenant_id=$1 AND provider_account_id=$2`, seed.tenantID, accountID).Scan(&plan, &status); err != nil {
		t.Fatal(err)
	}
	if plan != "supergrok" || status != subscriptionprofile.StatusObserved {
		t.Fatalf("套餐系统标签投影=%s/%s，期望 supergrok/observed", plan, status)
	}
	var flowStatus, stagedStatus string
	var resultCredentialID, resultAccountID int64
	var encryptedContent []byte
	if err := pool.QueryRow(ctx, `SELECT status, result_account_credential_id, provider_account_id FROM credential_acquisition_flow_sessions WHERE id=$1::uuid`, start.Session.ID).Scan(&flowStatus, &resultCredentialID, &resultAccountID); err != nil {
		t.Fatal(err)
	}
	if flowStatus != string(credentialacq.StatusFinalized) || resultCredentialID != credentialID || resultAccountID != accountID {
		t.Fatalf("OAuth 流程终态=%s credential=%d account=%d", flowStatus, resultCredentialID, resultAccountID)
	}
	if err := pool.QueryRow(ctx, `SELECT status, encrypted_content FROM account_intake_staged_credentials WHERE id=$1::uuid`, start.Session.ID).Scan(&stagedStatus, &encryptedContent); err != nil {
		t.Fatal(err)
	}
	if stagedStatus != "completed" || encryptedContent != nil {
		t.Fatalf("暂存终态=%s encrypted=%d 字节", stagedStatus, len(encryptedContent))
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM admin_audit_events
WHERE tenant_id=$1 AND action='credential_acquisition_completed'`, seed.tenantID).Scan(&completedLogs); err != nil {
		t.Fatal(err)
	}
	if completedLogs != 1 {
		t.Fatalf("完成日志=%d，期望 1", completedLogs)
	}
	if _, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin",
	}); !errors.Is(err, credentialacq.ErrFlowReplay) && !errors.Is(err, ErrStagedCredentialReplay) {
		t.Fatalf("重复执行应被拒绝：%v", err)
	}
}
