//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

type oauthIntakeTestExchanger struct {
	candidate credentialacq.CredentialCandidate
}

type accountActivationRecorder struct {
	accounts []int64
}

func (r *accountActivationRecorder) NotifyAccountActivated(_ int64, providerAccountID int64) {
	r.accounts = append(r.accounts, providerAccountID)
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
		AccountIDSource:      accountident.SourceXAIOIDCSubject,
		Subscription:         observation,
	}}); err != nil {
		t.Fatal(err)
	}

	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	staged := NewStagedStore(pool, keys)
	activation := &accountActivationRecorder{}
	intakeService := newAccountIntakeService(t, pool).WithAccountActivationNotifier(activation)
	service := NewOAuthService(intakeService, staged, sessions, registry, nil)
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
	if len(activation.accounts) != 1 || activation.accounts[0] != accountID {
		t.Fatalf("创建提交后的即时探测事件=%v，期望 [%d]", activation.accounts, accountID)
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

	secondStart, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "重复账号 OAuth 导入",
		RedirectURI: "http://127.0.0.1:3000/admin/v1/account-imports/oauth/callback",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "oauth-" + seed.suffix, AccountType: "oauth",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := service.CallbackForActor(
		ctx, secondStart.Session.ID, secondStart.State, "second-authorization-code",
		seed.tenantID, "platform-owner", "platform_admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPlan.Plan.Items) != 1 || secondPlan.Plan.Items[0].Action != "update" {
		t.Fatalf("相同账号再次授权的预检=%+v，期望 update", secondPlan.Plan.Items)
	}
	secondConfirmations := append([]string(nil), secondPlan.Plan.Items[0].RequiredConfirmations...)
	if secondPlan.Plan.Items[0].MixedChannelRisk != nil && secondPlan.Plan.Items[0].MixedChannelRisk.HighRisk {
		secondConfirmations = append(secondConfirmations, "confirm_mixed_channel_risk")
	}
	secondResult, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: secondStart.Session.ID, PlanHash: secondPlan.PlanHash,
		Confirmations: secondConfirmations,
		ActorID:       "platform-owner", ActorRole: "platform_admin", Reason: "确认同一账号凭据轮换",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.Summary.Updated != 1 || secondResult.Summary.Failed != 0 ||
		secondResult.Items[0].ProviderAccountID != accountID || secondResult.Items[0].AccountCredentialID != credentialID {
		t.Fatalf("相同账号轮换结果=%+v", secondResult)
	}
	var secondFlowStatus, secondStagedStatus string
	var secondFlowCredentialID int64
	if err := pool.QueryRow(ctx, `SELECT status, result_account_credential_id FROM credential_acquisition_flow_sessions WHERE id=$1::uuid`, secondStart.Session.ID).Scan(&secondFlowStatus, &secondFlowCredentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM account_intake_staged_credentials WHERE id=$1::uuid`, secondStart.Session.ID).Scan(&secondStagedStatus); err != nil {
		t.Fatal(err)
	}
	if secondFlowStatus != string(credentialacq.StatusFinalized) || secondFlowCredentialID != credentialID || secondStagedStatus != "completed" {
		t.Fatalf("轮换流程未闭环：flow=%s credential=%d staged=%s", secondFlowStatus, secondFlowCredentialID, secondStagedStatus)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatalf("相同账号轮换后账号数=%d，期望仍为 1", accountCount)
	}
	if len(activation.accounts) != 2 || activation.accounts[1] != accountID {
		t.Fatalf("轮换提交后的即时探测事件=%v，期望同一账号触发两次", activation.accounts)
	}
}

// TestOAuthAccountIntakeCompletionLogFailureRollsBackBusinessState 守护 OAuth 导入的最终
// 原子边界：账号、凭据、健康、OAuth 会话绑定、暂存完成态和完成日志必须同成同败。
// 变异：把会话终态与暂存完成写移回账号事务之后，完成日志被拒时账号仍会落库，本测试转红。
func TestOAuthAccountIntakeCompletionLogFailureRollsBackBusinessState(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='grok_chat' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}

	keys, err := credentialstore.NewStaticKeyProvider("oauth-intake-rollback-test", bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger("grok/xai_oauth", oauthIntakeTestExchanger{candidate: credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		Payload:           []byte(`{"access_token":"rollback-access","refresh_token":"rollback-refresh"}`),
		ExternalAccountID: "rollback-account-" + seed.suffix,
		ExternalSubjectID: "rollback-subject-" + seed.suffix,
		AccountIDSource:   "oauth_token_response",
	}}); err != nil {
		t.Fatal(err)
	}

	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	staged := NewStagedStore(pool, keys)
	activation := &accountActivationRecorder{}
	intakeService := newAccountIntakeService(t, pool).WithAccountActivationNotifier(activation)
	service := NewOAuthService(intakeService, staged, sessions, registry, nil)
	start, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "验证完成日志失败回滚",
		RedirectURI: "http://127.0.0.1:3000/admin/v1/account-imports/oauth/callback",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "oauth-rollback-" + seed.suffix, AccountType: "oauth",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := service.CallbackForActor(
		ctx, start.Session.ID, start.State, "authorization-code",
		seed.tenantID, "platform-owner", "platform_admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	installOAuthCompletionLogRejectTrigger(t, ctx, pool, seed.suffix)

	result, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "触发完成日志失败",
	})
	if err != nil {
		t.Fatalf("执行应返回可观察的失败项而不是丢失结果：%v", err)
	}
	if result.Summary.Failed != 1 || result.Summary.Created != 0 || len(result.Items) != 1 || result.Items[0].Status != StatusFailed {
		t.Fatalf("执行结果=%+v，期望单项失败且无创建", result)
	}
	if len(activation.accounts) != 0 {
		t.Fatalf("回滚事务不得触发账号探测：%v", activation.accounts)
	}

	for label, query := range map[string]string{
		"账号": `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`,
		"凭据": `SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1`,
		"健康": `SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, seed.tenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("完成日志失败后残留%s=%d，期望整体回滚", label, count)
		}
	}

	var flowStatus, stagedStatus string
	var flowAccountID, flowCredentialID *int64
	if err := pool.QueryRow(ctx, `
SELECT status, provider_account_id, result_account_credential_id
FROM credential_acquisition_flow_sessions WHERE id=$1::uuid`, start.Session.ID).Scan(
		&flowStatus, &flowAccountID, &flowCredentialID,
	); err != nil {
		t.Fatal(err)
	}
	if flowStatus != string(credentialacq.StatusValidated) || flowAccountID != nil || flowCredentialID != nil {
		t.Fatalf("OAuth 会话被错误完成：status=%s account=%v credential=%v", flowStatus, flowAccountID, flowCredentialID)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM account_intake_staged_credentials WHERE id=$1::uuid`, start.Session.ID).Scan(&stagedStatus); err != nil {
		t.Fatal(err)
	}
	if stagedStatus != "failed" {
		t.Fatalf("暂存流程状态=%s，期望 failed", stagedStatus)
	}
	var completedLogs, failedLogs int
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE action='credential_acquisition_completed')::int,
		count(*) FILTER (WHERE action='credential_acquisition_failed')::int
	FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID).Scan(&completedLogs, &failedLogs); err != nil {
		t.Fatal(err)
	}
	if completedLogs != 0 || failedLogs != 1 {
		t.Fatalf("流程日志 completed=%d failed=%d，期望 0/1", completedLogs, failedLogs)
	}
}

func installOAuthCompletionLogRejectTrigger(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, suffix string) {
	t.Helper()
	identifier := "reject_oauth_completed_" + strings.ReplaceAll(suffix, "-", "_")
	functionSQL := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.action = 'credential_acquisition_completed' THEN
    RAISE EXCEPTION 'reject oauth completion log';
  END IF;
  RETURN NEW;
END $$`, identifier)
	if _, err := pool.Exec(ctx, functionSQL); err != nil {
		t.Fatal(err)
	}
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON admin_audit_events FOR EACH ROW EXECUTE FUNCTION %s()`, identifier, identifier)
	if _, err := pool.Exec(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}
}
