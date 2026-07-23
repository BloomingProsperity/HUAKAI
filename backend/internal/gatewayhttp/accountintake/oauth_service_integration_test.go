//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

type oauthIntakeTestExchanger struct {
	candidate credentialacq.CredentialCandidate
}

type oauthIntakeRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn oauthIntakeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
	// 该夹具只验证授权码完成后的账号事务，不得回落到生产默认注册表，
	// 否则厂商默认登录方式变化会把测试悄悄改成另一种授权协议。
	return credentialacq.StartOAuthFlowWithRegistry(ctx, store, in, cfg, credentialacq.NewExchangerRegistry())
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
	blocked, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: secondStart.Session.ID, PlanHash: secondPlan.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "缺确认时不得消费临时凭据",
	})
	if err != nil {
		t.Fatalf("缺确认预检失败：%v", err)
	}
	if blocked.Summary.Conflict != 1 || len(blocked.Items) != 1 || blocked.Items[0].Code != "confirmation_required" {
		t.Fatalf("缺确认结果=%+v，期望 confirmation_required", blocked)
	}
	var retryableStatus string
	var retryableCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT status, encrypted_content FROM account_intake_staged_credentials WHERE id=$1::uuid`, secondStart.Session.ID).Scan(&retryableStatus, &retryableCiphertext); err != nil {
		t.Fatal(err)
	}
	if retryableStatus != "staged" || len(retryableCiphertext) == 0 {
		t.Fatalf("缺确认后临时凭据不可重试：status=%s encrypted=%d 字节", retryableStatus, len(retryableCiphertext))
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

func TestXAIAccountIntakeUsesDeviceCodeAndPollsIntoCreatePlan(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='grok_chat' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("xai-device-intake-test", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: oauthIntakeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://auth.x.ai/oauth2/device/code" {
			t.Fatalf("xAI 启动请求端点=%s", req.URL)
		}
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.PostForm.Get("client_id") == "" || req.PostForm.Get("scope") == "" {
			t.Fatalf("xAI 设备码缺少固定客户端合同：%v", req.PostForm)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(
			`{"device_code":"device-1","user_code":"XAI-1234","verification_uri":"https://auth.x.ai/activate","expires_in":900,"interval":5}`,
		))}, nil
	})}
	poller := func(_ context.Context, session credentialacq.Session) (credentialacq.CredentialCandidate, error) {
		return credentialacq.CredentialCandidate{
			TenantID: session.TenantID, Vendor: session.Vendor, AuthMode: session.AuthMode, ActorID: session.ActorID,
			Payload:           []byte(`{"access_token":"xai-access","refresh_token":"xai-refresh","oidc_identity_verified":true,"team_id":"team-device","sub":"subject-device","email":"device@example.test","client_id_source":"public_cli_client"}`),
			ExternalAccountID: "team-device", ExternalSubjectID: "subject-device",
			ExternalAccountEmail: "device@example.test", AccountIDSource: accountident.SourceXAIOIDCSubject,
		}, nil
	}
	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	service := NewOAuthService(
		newAccountIntakeService(t, pool), NewStagedStore(pool, keys), sessions,
		credentialacq.DefaultExchangerRegistry(), poller,
	)
	start, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Client: credentialacq.OAuthClientConfig{HTTPClient: httpClient},
		Account: AccountDefaults{ProviderID: seed.providerID, ChannelID: seed.channelID, NamePrefix: "xai-device-" + seed.suffix, AccountType: "oauth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.AuthType != credentialacq.AuthTypeDeviceCode || start.UserCode != "XAI-1234" || start.State != "" || start.Session.Status != credentialacq.StatusWaitingForUser {
		t.Fatalf("xAI 账号导入未进入设备码等待态：%+v", start)
	}
	planned, retryAfter, err := service.Poll(ctx, seed.tenantID, "platform-owner", start.Session.ID, "req-xai-device")
	if err != nil || retryAfter != 0 {
		t.Fatalf("xAI 设备码轮询：retry=%s err=%v", retryAfter, err)
	}
	if len(planned.Plan.Items) != 1 || planned.Plan.Items[0].Action != "create" ||
		!strings.HasPrefix(planned.Plan.Items[0].Identity.ExternalAccountID, "account_") ||
		!planned.Plan.Items[0].Identity.SubjectIdentityPresent || !planned.Plan.Items[0].Identity.SubjectIdentityTrusted {
		t.Fatalf("xAI 设备码未进入带复合身份的创建预检：%+v", planned.Plan.Items)
	}
	var accounts int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 0 {
		t.Fatalf("预检阶段不应提前创建账号，实际=%d", accounts)
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
	removeCompletionLogReject := installOAuthCompletionLogRejectTrigger(t, ctx, pool, seed.suffix)

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
	var stagedCiphertext []byte
	if err := pool.QueryRow(ctx, `SELECT status, encrypted_content FROM account_intake_staged_credentials WHERE id=$1::uuid`, start.Session.ID).Scan(&stagedStatus, &stagedCiphertext); err != nil {
		t.Fatal(err)
	}
	if stagedStatus != "staged" || len(stagedCiphertext) == 0 {
		t.Fatalf("暂存流程不可重试：status=%s encrypted=%d 字节", stagedStatus, len(stagedCiphertext))
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

	removeCompletionLogReject()
	retried, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "完成日志恢复后重试",
	})
	if err != nil {
		t.Fatalf("恢复后重试失败：%v", err)
	}
	if retried.Summary.Created != 1 || retried.Summary.Failed != 0 || len(activation.accounts) != 1 {
		t.Fatalf("恢复后结果=%+v activation=%v，期望创建一次", retried, activation.accounts)
	}
	if err := pool.QueryRow(ctx, `
SELECT f.status, s.status
FROM credential_acquisition_flow_sessions f
JOIN account_intake_staged_credentials s ON s.id=f.id
WHERE f.id=$1::uuid`, start.Session.ID).Scan(&flowStatus, &stagedStatus); err != nil {
		t.Fatal(err)
	}
	if flowStatus != string(credentialacq.StatusFinalized) || stagedStatus != "completed" {
		t.Fatalf("重试成功后流程未闭环：flow=%s staged=%s", flowStatus, stagedStatus)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE action='credential_acquisition_completed')::int,
		count(*) FILTER (WHERE action='credential_acquisition_failed')::int
	FROM admin_audit_events WHERE tenant_id=$1`, seed.tenantID).Scan(&completedLogs, &failedLogs); err != nil {
		t.Fatal(err)
	}
	if completedLogs != 1 || failedLogs != 1 {
		t.Fatalf("恢复后流程日志 completed=%d failed=%d，期望 1/1", completedLogs, failedLogs)
	}
}

func TestOAuthAccountIntakePermanentCandidateFailureErasesSecret(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='grok_chat' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("oauth-intake-terminal-test", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger("grok/xai_oauth", oauthIntakeTestExchanger{candidate: credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		Payload:           []byte(`{}`),
		ExternalAccountID: "terminal-account-" + seed.suffix,
		ExternalSubjectID: "terminal-subject-" + seed.suffix,
		AccountIDSource:   "oauth_token_response",
	}}); err != nil {
		t.Fatal(err)
	}
	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	service := NewOAuthService(
		newAccountIntakeService(t, pool),
		NewStagedStore(pool, keys),
		sessions,
		registry,
		nil,
	)
	start, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "验证永久失败立即擦除",
		RedirectURI: "http://127.0.0.1:3000/admin/v1/account-imports/oauth/callback",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "oauth-terminal-" + seed.suffix, AccountType: "oauth",
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
	if len(planned.Plan.Items) != 1 || planned.Plan.Items[0].Action != intake.ActionFail ||
		planned.Plan.Items[0].Code != "invalid_credential" {
		t.Fatalf("永久失败预检=%+v，期望 invalid_credential", planned.Plan.Items)
	}
	result, err := service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "执行永久失败候选",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Failed != 1 || result.Items[0].Code != "invalid_credential" {
		t.Fatalf("永久失败结果=%+v", result)
	}
	var flowStatus, stagedStatus string
	var encrypted []byte
	if err := pool.QueryRow(ctx, `
SELECT f.status, s.status, s.encrypted_content
FROM credential_acquisition_flow_sessions f
JOIN account_intake_staged_credentials s ON s.id=f.id
WHERE f.id=$1::uuid`, start.Session.ID).Scan(&flowStatus, &stagedStatus, &encrypted); err != nil {
		t.Fatal(err)
	}
	if flowStatus != string(credentialacq.StatusFailed) || stagedStatus != "failed" || encrypted != nil {
		t.Fatalf("永久失败未闭环：flow=%s staged=%s encrypted=%d 字节", flowStatus, stagedStatus, len(encrypted))
	}
	var accountCount, failureLogs int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID).Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM admin_audit_events
WHERE tenant_id=$1 AND action='credential_acquisition_failed'`, seed.tenantID).Scan(&failureLogs); err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || failureLogs != 1 {
		t.Fatalf("永久失败残留：account=%d failure_logs=%d", accountCount, failureLogs)
	}
}

func TestOAuthAccountIntakeCorruptStagedCandidateTerminatesBeforeExecution(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE providers SET upstream_protocol='grok_chat' WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("oauth-intake-corrupt-test", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentialacq.NewExchangerRegistry()
	if err := registry.RegisterExchanger("grok/xai_oauth", oauthIntakeTestExchanger{candidate: credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		Payload:           []byte(`{"access_token":"corrupt-access","refresh_token":"corrupt-refresh"}`),
		ExternalAccountID: "corrupt-account-" + seed.suffix,
		ExternalSubjectID: "corrupt-subject-" + seed.suffix,
		AccountIDSource:   "oauth_token_response",
	}}); err != nil {
		t.Fatal(err)
	}
	sessions := credentialacq.NewPostgresSessionStoreWithKeys(pool, keys)
	service := NewOAuthService(
		newAccountIntakeService(t, pool),
		NewStagedStore(pool, keys),
		sessions,
		registry,
		nil,
	)
	start, err := service.Start(ctx, OAuthStartInput{
		TenantID: seed.tenantID, Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "验证损坏候选立即终止",
		RedirectURI: "http://127.0.0.1:3000/admin/v1/account-imports/oauth/callback",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "oauth-corrupt-" + seed.suffix, AccountType: "oauth",
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
	if len(planned.Plan.Items) != 1 || planned.Plan.Items[0].Action != intake.ActionCreate {
		t.Fatalf("测试前提失效：%+v", planned.Plan.Items)
	}
	if _, err := pool.Exec(ctx, `
UPDATE account_intake_staged_credentials
SET encrypted_content=set_byte(encrypted_content, 0, get_byte(encrypted_content, 0) # 1)
WHERE id=$1::uuid`, start.Session.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.Execute(ctx, OAuthExecuteInput{
		TenantID: seed.tenantID, FlowID: start.Session.ID, PlanHash: planned.PlanHash,
		ActorID: "platform-owner", ActorRole: "platform_admin", Reason: "执行损坏候选",
	})
	if !errors.Is(err, ErrStagedCredentialCorrupt) {
		t.Fatalf("损坏候选 err=%v，期望 ErrStagedCredentialCorrupt", err)
	}
	var flowStatus, stagedStatus string
	var encrypted []byte
	if err := pool.QueryRow(ctx, `
SELECT f.status, s.status, s.encrypted_content
FROM credential_acquisition_flow_sessions f
JOIN account_intake_staged_credentials s ON s.id=f.id
WHERE f.id=$1::uuid`, start.Session.ID).Scan(&flowStatus, &stagedStatus, &encrypted); err != nil {
		t.Fatal(err)
	}
	if flowStatus != string(credentialacq.StatusFailed) || stagedStatus != "failed" || encrypted != nil {
		t.Fatalf("损坏候选未闭环：flow=%s staged=%s encrypted=%d 字节", flowStatus, stagedStatus, len(encrypted))
	}
	var failureLogs int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM admin_audit_events
WHERE tenant_id=$1 AND action='credential_acquisition_failed'`, seed.tenantID).Scan(&failureLogs); err != nil {
		t.Fatal(err)
	}
	if failureLogs != 1 {
		t.Fatalf("损坏候选 failure_logs=%d，期望 1", failureLogs)
	}
}

func installOAuthCompletionLogRejectTrigger(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, suffix string) func() {
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
	remove := func() {
		_, _ = pool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON admin_audit_events`, identifier))
		_, _ = pool.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, identifier))
	}
	t.Cleanup(remove)
	return remove
}
