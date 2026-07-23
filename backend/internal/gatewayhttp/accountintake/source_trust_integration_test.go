//go:build integration_pg

package accountintake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/crssource"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func TestClaudeCookie两种来源完成账号事务和一次性流程(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	keys, err := credentialstore.NewStaticKeyProvider("cookie-source-test", bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		setupToken bool
		source     intake.SourceKind
		mode       string
		secrets    []string
	}{
		{
			name: "OAuth Cookie", source: intake.SourceClaudeCookie,
			mode:    credentialstore.AuthModeClaudeAIOAuth,
			secrets: []string{"cookie-access", "cookie-refresh"},
		},
		{
			name: "Setup Cookie", setupToken: true, source: intake.SourceClaudeSetupCookie,
			mode:    credentialstore.AuthModeClaudeSetupToken,
			secrets: []string{"cookie-setup"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seed := seedAccountIntake(t, ctx, pool)
			if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session'
WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
				t.Fatal(err)
			}
			staged := NewStagedStore(pool, keys)
			service := NewCookieService(
				NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())),
				staged,
				newCookieIntegrationExchanger(t, test.setupToken, seed.suffix),
			)
			planned, err := service.Plan(ctx, CookiePlanInput{
				TenantID: seed.tenantID, SessionKey: "session", SetupToken: test.setupToken,
				Account: AccountDefaults{
					ProviderID: seed.providerID, ChannelID: seed.channelID,
					NamePrefix: "cookie-" + seed.suffix, AccountType: "oauth",
					Extra: []byte(`{"z":1, "a":{"second":2,"first":1}}`),
				},
				ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
			})
			if err != nil {
				t.Fatal(err)
			}
			if planned.Plan.SourceKind != test.source || planned.Plan.Summary.Create != 1 {
				t.Fatalf("计划来源或动作错误：%+v", planned.Plan)
			}
			var encryptedContent []byte
			var planInputText string
			if err := pool.QueryRow(ctx, `
SELECT encrypted_content, plan_input::text
FROM account_intake_staged_credentials WHERE id=$1::uuid`,
				planned.FlowID).Scan(&encryptedContent, &planInputText); err != nil {
				t.Fatal(err)
			}
			if len(encryptedContent) == 0 {
				t.Fatal("暂存行缺少加密凭据")
			}
			for _, secret := range test.secrets {
				if bytes.Contains(encryptedContent, []byte(secret)) || strings.Contains(planInputText, secret) {
					t.Fatal("暂存行泄漏了凭据明文")
				}
			}
			result, err := service.Execute(ctx, CookieExecuteInput{
				TenantID: seed.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
				Confirmations: planned.Plan.Items[0].RequiredConfirmations,
				ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
			})
			if err != nil || result.Summary.Created != 1 || result.Summary.Failed != 0 {
				t.Fatalf("执行结果=%+v err=%v", result, err)
			}
			var stagedStatus string
			var accountCount, credentialCount, healthCount int
			if err := pool.QueryRow(ctx, `
SELECT status FROM account_intake_staged_credentials WHERE id=$1::uuid`, planned.FlowID).Scan(&stagedStatus); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID).Scan(&accountCount); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND auth_mode=$2`,
				seed.tenantID, test.mode).Scan(&credentialCount); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1`, seed.tenantID).Scan(&healthCount); err != nil {
				t.Fatal(err)
			}
			if stagedStatus != "completed" || accountCount != 1 || credentialCount != 1 || healthCount != 1 {
				t.Fatalf("终态 staged=%s account=%d credential=%d health=%d",
					stagedStatus, accountCount, credentialCount, healthCount)
			}
			if _, err := service.Execute(ctx, CookieExecuteInput{
				TenantID: seed.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
				ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
			}); !errors.Is(err, ErrStagedCredentialReplay) {
				t.Fatalf("重复执行 err=%v，期望 ErrStagedCredentialReplay", err)
			}
		})
	}
}

func newCookieIntegrationExchanger(t *testing.T, setupToken bool, suffix string) *claudecookie.Exchanger {
	t.Helper()
	requests := 0
	client := &http.Client{Transport: oauthIntakeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if req.Method != http.MethodGet || req.URL.String() != "https://claude.ai/api/organizations" {
				t.Fatalf("组织请求=%s %s", req.Method, req.URL)
			}
			cookie, err := req.Cookie("sessionKey")
			if err != nil || cookie.Value != "session" {
				t.Fatalf("组织请求缺少受控 Cookie：cookie=%v err=%v", cookie, err)
			}
			return cookieIntegrationResponse(http.StatusOK,
				fmt.Sprintf(`[{"uuid":"org-%s","name":"Test"}]`, suffix)), nil
		case 2:
			if req.Method != http.MethodPost ||
				req.URL.String() != "https://claude.ai/v1/oauth/org-"+suffix+"/authorize" {
				t.Fatalf("授权请求=%s %s", req.Method, req.URL)
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if setupToken && body["scope"] != "user:inference" {
				t.Fatalf("Setup Cookie scope=%q", body["scope"])
			}
			if !setupToken &&
				(!strings.Contains(body["scope"], "user:profile") || !strings.Contains(body["scope"], "user:mcp_servers")) {
				t.Fatalf("OAuth Cookie scope=%q", body["scope"])
			}
			callback := "https://platform.claude.com/oauth/code/callback?code=auth-code&state=" +
				url.QueryEscape(body["state"])
			return cookieIntegrationResponse(http.StatusOK,
				fmt.Sprintf(`{"redirect_uri":%q}`, callback)), nil
		case 3:
			if req.Method != http.MethodPost || req.URL.String() != "https://platform.claude.com/v1/oauth/token" {
				t.Fatalf("换码请求=%s %s", req.Method, req.URL)
			}
			if setupToken {
				return cookieIntegrationResponse(http.StatusOK,
					fmt.Sprintf(`{"access_token":"cookie-setup","account":{"uuid":"cookie-setup-%s"}}`, suffix)), nil
			}
			return cookieIntegrationResponse(http.StatusOK,
				fmt.Sprintf(`{"access_token":"cookie-access","refresh_token":"cookie-refresh",`+
					`"token_type":"Bearer","scope":"user:inference","expires_in":3600,`+
					`"account":{"uuid":"cookie-oauth-%s"}}`, suffix)), nil
		default:
			t.Fatalf("Cookie 转换器发出多余请求：%d", requests)
			return nil, nil
		}
	})}
	return claudecookie.New(client)
}

func cookieIntegrationResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestCookie完成日志失败时账号事务整体回滚且暂存可重试(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session'
WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("cookie-rollback-test", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	activation := &accountActivationRecorder{}
	staged := NewStagedStore(pool, keys)
	service := NewCookieService(
		NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())).
			WithAccountActivationNotifier(activation),
		staged,
		cookieSourceExchangerStub{result: claudecookie.Result{
			ImportContent: `{"access_token":"rollback-access","refresh_token":"rollback-refresh",` +
				`"external_account_id":"rollback-cookie-account"}`,
			OrganizationID: "rollback-org", AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		}},
	)
	planned, err := service.Plan(ctx, CookiePlanInput{
		TenantID: seed.tenantID, SessionKey: "session",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "cookie-rollback-" + seed.suffix, AccountType: "oauth",
		},
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	installOAuthCompletionLogRejectTrigger(t, ctx, pool, seed.suffix)
	result, err := service.Execute(ctx, CookieExecuteInput{
		TenantID: seed.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
		Confirmations: planned.Plan.Items[0].RequiredConfirmations,
		ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
	})
	if err != nil {
		t.Fatalf("逐项失败应返回完整结果：%v", err)
	}
	if result.Summary.Failed != 1 || result.Summary.Created != 0 ||
		len(result.Items) != 1 || result.Items[0].Status != StatusFailed {
		t.Fatalf("完成日志失败结果=%+v，期望单项失败且无创建", result)
	}
	if len(activation.accounts) != 0 {
		t.Fatalf("回滚事务不得触发账号采集：%v", activation.accounts)
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
			t.Fatalf("完成日志失败后残留%s=%d", label, count)
		}
	}
	var status string
	var secretPresent bool
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NOT NULL
FROM account_intake_staged_credentials WHERE id=$1::uuid AND tenant_id=$2`,
		planned.FlowID, seed.tenantID).Scan(&status, &secretPresent); err != nil {
		t.Fatal(err)
	}
	if status != "staged" || !secretPresent {
		t.Fatalf("失败后暂存不可重试 status=%s secret_present=%v", status, secretPresent)
	}
	var completedLogs int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM admin_audit_events
WHERE tenant_id=$1 AND action='credential_acquisition_completed'`,
		seed.tenantID).Scan(&completedLogs); err != nil {
		t.Fatal(err)
	}
	if completedLogs != 0 {
		t.Fatalf("事务回滚后仍留下完成日志=%d", completedLogs)
	}
}

func TestCookie执行入口拒绝错身份哈希和过期且零副作用(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)

	owner, service, planned := planCookieExecutionFlow(t, ctx, pool, "scope")
	other := seedAccountIntake(t, ctx, pool)
	for name, input := range map[string]CookieExecuteInput{
		"错误租户": {
			TenantID: other.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
			Confirmations: planned.Plan.Items[0].RequiredConfirmations,
			ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
		},
		"错误操作者": {
			TenantID: owner.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
			Confirmations: planned.Plan.Items[0].RequiredConfirmations,
			ActorID:       "other-actor", ActorRole: admin.RolePlatformAdmin,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Execute(ctx, input); !errors.Is(err, ErrStagedCredentialNotFound) {
				t.Fatalf("err=%v，期望 ErrStagedCredentialNotFound", err)
			}
		})
	}
	assertCookieExecutionFacts(t, ctx, pool, owner.tenantID, 0)
	assertStagedFlowState(t, ctx, pool, planned.FlowID, owner.tenantID, "staged", true)
	result, err := service.Execute(ctx, CookieExecuteInput{
		TenantID: owner.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
		Confirmations: planned.Plan.Items[0].RequiredConfirmations,
		ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
	})
	if err != nil || result.Summary.Created != 1 {
		t.Fatalf("原身份无法执行：result=%+v err=%v", result, err)
	}
	assertCookieExecutionFacts(t, ctx, pool, owner.tenantID, 1)

	driftOwner, driftService, driftPlan := planCookieExecutionFlow(t, ctx, pool, "drift")
	if _, err := driftService.Execute(ctx, CookieExecuteInput{
		TenantID: driftOwner.tenantID, FlowID: driftPlan.FlowID, PlanHash: strings.Repeat("0", 64),
		Confirmations: driftPlan.Plan.Items[0].RequiredConfirmations,
		ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
	}); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("计划漂移 err=%v，期望 ErrPlanChanged", err)
	}
	assertCookieExecutionFacts(t, ctx, pool, driftOwner.tenantID, 0)
	assertStagedFlowState(t, ctx, pool, driftPlan.FlowID, driftOwner.tenantID, "staged", true)

	expiredOwner, expiredService, expiredPlan := planCookieExecutionFlow(t, ctx, pool, "expired")
	if _, err := pool.Exec(ctx, `
UPDATE account_intake_staged_credentials
SET expires_at=clock_timestamp()-interval '1 second'
WHERE id=$1::uuid AND tenant_id=$2`, expiredPlan.FlowID, expiredOwner.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := expiredService.Execute(ctx, CookieExecuteInput{
		TenantID: expiredOwner.tenantID, FlowID: expiredPlan.FlowID, PlanHash: expiredPlan.PlanHash,
		Confirmations: expiredPlan.Plan.Items[0].RequiredConfirmations,
		ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
	}); !errors.Is(err, ErrStagedCredentialExpired) {
		t.Fatalf("过期执行 err=%v，期望 ErrStagedCredentialExpired", err)
	}
	assertCookieExecutionFacts(t, ctx, pool, expiredOwner.tenantID, 0)
	assertStagedFlowState(t, ctx, pool, expiredPlan.FlowID, expiredOwner.tenantID, "expired", false)
}

func TestCookie并发执行同一流程只提交一次(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	owner, service, planned := planCookieExecutionFlow(t, ctx, pool, "concurrent")

	type executionOutcome struct {
		result ExecutionResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan executionOutcome, 2)
	for range 2 {
		go func() {
			<-start
			result, err := service.Execute(ctx, CookieExecuteInput{
				TenantID: owner.tenantID, FlowID: planned.FlowID, PlanHash: planned.PlanHash,
				Confirmations: planned.Plan.Items[0].RequiredConfirmations,
				ActorID:       "platform-owner", ActorRole: admin.RolePlatformAdmin,
			})
			outcomes <- executionOutcome{result: result, err: err}
		}()
	}
	close(start)

	completed, rejected := 0, 0
	for range 2 {
		outcome := <-outcomes
		switch {
		case outcome.err == nil && outcome.result.Summary.Created == 1 &&
			outcome.result.Summary.Failed == 0:
			completed++
		case errors.Is(outcome.err, ErrStagedCredentialReplay),
			errors.Is(outcome.err, ErrPlanChanged):
			rejected++
		case outcome.err == nil && outcome.result.Summary.Created == 0 &&
			outcome.result.Summary.Failed == 1 && len(outcome.result.Items) == 1 &&
			(outcome.result.Items[0].Code == "credential_flow_replayed" ||
				outcome.result.Items[0].Code == "account_plan_changed" ||
				outcome.result.Items[0].Code == "plan_stale"):
			rejected++
		default:
			t.Fatalf("并发执行出现未知结果：result=%+v err=%v", outcome.result, outcome.err)
		}
	}
	if completed != 1 || rejected != 1 {
		t.Fatalf("并发执行 completed=%d rejected=%d，期望 1/1", completed, rejected)
	}
	assertCookieExecutionFacts(t, ctx, pool, owner.tenantID, 1)
	assertStagedFlowState(t, ctx, pool, planned.FlowID, owner.tenantID, "completed", false)
}

func planCookieExecutionFlow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	label string,
) (accountIntakeSeed, *CookieService, CookiePlanResult) {
	t.Helper()
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session'
WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider(
		"cookie-execute-"+label, bytes.Repeat([]byte{byte(len(label) + 1)}, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewCookieService(
		NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())),
		NewStagedStore(pool, keys),
		cookieSourceExchangerStub{result: claudecookie.Result{
			ImportContent: fmt.Sprintf(
				`{"access_token":"%s-access","refresh_token":"%s-refresh","external_account_id":"%s-account"}`,
				label, label, label,
			),
			OrganizationID: "org-" + label, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		}},
	)
	planned, err := service.Plan(ctx, CookiePlanInput{
		TenantID: seed.tenantID, SessionKey: "session",
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "cookie-" + label + "-" + seed.suffix, AccountType: "oauth",
		},
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return seed, service, planned
}

func assertCookieExecutionFacts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	want int,
) {
	t.Helper()
	for label, query := range map[string]string{
		"账号": `SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND deleted_at IS NULL`,
		"凭据": `SELECT count(*)::int FROM account_credentials WHERE tenant_id=$1 AND state='active' AND deleted_at IS NULL`,
		"健康": `SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1`,
		"完成日志": `SELECT count(*)::int FROM admin_audit_events
			WHERE tenant_id=$1 AND action='credential_acquisition_completed'`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, tenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s数量=%d，期望 %d", label, count, want)
		}
	}
}

func assertStagedFlowState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	flowID string,
	tenantID int64,
	wantStatus string,
	wantSecret bool,
) {
	t.Helper()
	var status string
	var secretPresent bool
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NOT NULL
FROM account_intake_staged_credentials
WHERE id=$1::uuid AND tenant_id=$2`, flowID, tenantID).Scan(&status, &secretPresent); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || secretPresent != wantSecret {
		t.Fatalf("暂存终态 status=%s secret_present=%v，期望 %s/%v",
			status, secretPresent, wantStatus, wantSecret)
	}
}

func Test暂存拒绝未固定时间且不写数据库(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("stage-time-test", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStagedStore(pool, keys)
	_, err = store.Stage(ctx, StageInput{
		TenantID: seed.tenantID, ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		SourceKind: string(intake.SourceClaudeCookie),
		Vendor:     credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		PlanInput: PlanInput{
			TenantID: seed.tenantID, SourceKind: intake.SourceClaudeCookie,
			DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
		},
		PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Content:  `{"access_token":"secret"}`,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v，期望 ErrInvalidInput", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_intake_staged_credentials WHERE tenant_id=$1`, seed.tenantID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("零时间暂存留下 %d 行", rows)
	}
	_, err = store.Stage(ctx, StageInput{
		TenantID: seed.tenantID, ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		SourceKind: string(intake.SourceClaudeCookie),
		Vendor:     credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		PlanInput: PlanInput{
			TenantID: seed.tenantID, SourceKind: intake.SourceJSON,
			DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
			Now: store.nowTime(),
		},
		PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Content:  `{"access_token":"secret"}`,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("来源前像错配 err=%v，期望 ErrInvalidInput", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_intake_staged_credentials WHERE tenant_id=$1`, seed.tenantID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("来源前像错配留下 %d 行", rows)
	}
}

type crsSourceIntegrationStub struct {
	export crssource.Export
}

func (s crsSourceIntegrationStub) Fetch(context.Context, crssource.Input) (crssource.Export, error) {
	return s.export, nil
}

func TestCRS六类来源完成计划暂存执行和终态回流(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("crs-source-test", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	type sourceCase struct {
		sourceType  string
		vendor      string
		authMode    string
		accountType string
		protocol    string
		credentials map[string]any
	}
	sources := []sourceCase{
		{
			sourceType: "claude", vendor: credentialstore.VendorAnthropic,
			authMode: credentialstore.AuthModeClaudeSetupToken, accountType: "oauth",
			protocol: "anthropic_claude_session", credentials: map[string]any{"setup_token": "setup-secret"},
		},
		{
			sourceType: "claude_console", vendor: credentialstore.VendorAnthropic,
			authMode: credentialstore.AuthModeAPIKey, accountType: "api_key",
			protocol: "anthropic_messages", credentials: map[string]any{"api_key": "anthropic-key"},
		},
		{
			sourceType: "openai_oauth", vendor: credentialstore.VendorOpenAI,
			authMode: credentialstore.AuthModeChatGPTOAuth, accountType: "oauth",
			protocol: "openai_codex", credentials: map[string]any{"access_token": "openai-access", "refresh_token": "openai-refresh"},
		},
		{
			sourceType: "openai_responses", vendor: credentialstore.VendorOpenAI,
			authMode: credentialstore.AuthModeAPIKey, accountType: "api_key",
			protocol: "openai_responses", credentials: map[string]any{"api_key": "openai-key"},
		},
		{
			sourceType: "gemini_oauth", vendor: credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeCodeAssist, accountType: "oauth",
			protocol: "gemini_code_assist", credentials: map[string]any{"refresh_token": "gemini-refresh"},
		},
		{
			sourceType: "gemini_api_key", vendor: credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeAIStudioAPIKey, accountType: "api_key",
			protocol: "gemini_messages", credentials: map[string]any{"api_key": "gemini-key"},
		},
	}
	destinations := make(map[string]AccountDefaults, len(sources))
	for index, source := range sources {
		var providerID, channelID int64
		if index == 0 {
			providerID, channelID = seed.providerID, seed.channelID
			if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol=$1 WHERE id=$2 AND tenant_id=$3`,
				source.protocol, providerID, seed.tenantID); err != nil {
				t.Fatal(err)
			}
		} else {
			code := fmt.Sprintf("crs-%d-%s", index, seed.suffix)
			if err := pool.QueryRow(ctx, `
INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1,$2,$3,$4) RETURNING id`,
				seed.tenantID, code, code, source.protocol).Scan(&providerID); err != nil {
				t.Fatal(err)
			}
			var poolGroupID int64
			if err := pool.QueryRow(ctx, `
INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`,
				seed.tenantID, "crs-pool-"+code).Scan(&poolGroupID); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `
INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`,
				seed.tenantID, poolGroupID, "crs-channel-"+code).Scan(&channelID); err != nil {
				t.Fatal(err)
			}
		}
		destinations[source.sourceType] = AccountDefaults{
			ProviderID: providerID, ChannelID: channelID,
			NamePrefix: source.sourceType, AccountType: source.accountType,
			Extra: []byte(`{"z":1,"a":{"second":2,"first":1}}`),
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/web/auth/login":
			_, _ = w.Write([]byte(`{"success":true,"token":"crs-admin-token"}`))
		case "/admin/sync/export-accounts":
			if r.Header.Get("Authorization") != "Bearer crs-admin-token" ||
				r.URL.Query().Get("include_secrets") != "true" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{
  "success":true,
  "data":{
    "claudeAccounts":[{"id":"source-0","name":"Claude","authType":"setup-token","isActive":true,"schedulable":true,"credentials":{"access_token":"setup-secret"}}],
    "claudeConsoleAccounts":[{"id":"source-1","name":"Claude Console","isActive":true,"schedulable":true,"credentials":{"api_key":"anthropic-key"}}],
    "openaiOAuthAccounts":[{"id":"source-2","name":"OpenAI OAuth","isActive":true,"schedulable":true,"credentials":{"access_token":"openai-access","refresh_token":"openai-refresh"}}],
    "openaiResponsesAccounts":[{"id":"source-3","name":"OpenAI Responses","isActive":true,"schedulable":true,"credentials":{"api_key":"openai-key"}}],
    "geminiOAuthAccounts":[{"id":"source-4","name":"Gemini OAuth","isActive":true,"schedulable":true,"credentials":{"refresh_token":"gemini-refresh"}}],
    "geminiApiKeyAccounts":[{"id":"source-5","name":"Gemini Key","isActive":true,"schedulable":true,"credentials":{"api_key":"gemini-key"}}]
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	crsClient := crssource.New(server.Client(), crssource.Policy{
		AllowedHosts: []string{parsed.Hostname()}, AllowPrivateHosts: true, AllowInsecureHTTP: true,
	})
	service := NewCRSService(
		NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())).
			WithProjectEnricher(&projectEnricherStub{result: projectenrich.Result{
				Payload: []byte(`{"refresh_token":"gemini-refresh","project_id":"gemini-project"}`),
			}}),
		NewStagedStore(pool, keys),
		crsClient,
	)
	planned, err := service.Plan(ctx, CRSPlanInput{
		TenantID: seed.tenantID, BaseURL: server.URL,
		Username: "operator", Password: "secret", Destinations: destinations,
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		RequestID: "req-crs-plan", Reason: "CRS 六类来源导入",
	})
	if err != nil || planned.Summary.Ready != len(sources) || planned.Summary.Failed != 0 || planned.Summary.Conflict != 0 {
		t.Fatalf("CRS 计划=%+v err=%v", planned.Summary, err)
	}
	entries := make([]CRSExecuteEntry, 0, len(planned.Items))
	for _, item := range planned.Items {
		if item.FlowID == "" || item.Plan == nil || len(item.Plan.Items) != 1 {
			t.Fatalf("CRS 项未完整暂存：%+v", item)
		}
		entries = append(entries, CRSExecuteEntry{
			FlowID: item.FlowID, PlanHash: item.PlanHash,
			Confirmations: item.Plan.Items[0].RequiredConfirmations,
		})
	}
	executed, err := service.Execute(ctx, CRSExecuteInput{
		TenantID: seed.tenantID, Entries: entries,
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		RequestID: "req-crs-execute", Reason: "确认 CRS 六类来源导入",
	})
	if err != nil || executed.Summary.Completed != len(sources) ||
		executed.Summary.Failed != 0 || executed.Summary.Conflict != 0 {
		detail, _ := json.Marshal(executed)
		t.Fatalf("CRS 执行=%+v detail=%s err=%v", executed.Summary, detail, err)
	}
	for _, source := range sources {
		var count int
		if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_credentials
WHERE tenant_id=$1 AND vendor=$2 AND auth_mode=$3 AND state='active'`,
			seed.tenantID, source.vendor, source.authMode).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("CRS %s/%s 凭据数=%d", source.vendor, source.authMode, count)
		}
	}
	var completedFlows, accountsCount, healthCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_intake_staged_credentials
WHERE tenant_id=$1 AND source_kind='crs_sync' AND status='completed'`,
		seed.tenantID).Scan(&completedFlows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND deleted_at IS NULL`,
		seed.tenantID).Scan(&accountsCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM channel_health_state WHERE tenant_id=$1`,
		seed.tenantID).Scan(&healthCount); err != nil {
		t.Fatal(err)
	}
	if completedFlows != len(sources) || accountsCount != len(sources) || healthCount != len(sources) {
		t.Fatalf("CRS 终态 flows=%d accounts=%d health=%d", completedFlows, accountsCount, healthCount)
	}
}

func TestCRSClaudeOAuth来源完成归一暂存执行和终态回流(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session'
WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("crs-claude-oauth-test", bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/web/auth/login":
			_, _ = w.Write([]byte(`{"success":true,"token":"crs-admin-token"}`))
		case "/admin/sync/export-accounts":
			if r.Header.Get("Authorization") != "Bearer crs-admin-token" ||
				r.URL.Query().Get("include_secrets") != "true" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{
  "success":true,
  "data":{
    "claudeAccounts":[{
      "id":"claude-oauth-source",
      "name":"Claude OAuth",
      "authType":"oauth",
      "isActive":true,
      "schedulable":true,
      "credentials":{"access_token":"claude-access","refresh_token":"claude-refresh"}
    }]
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := crssource.New(server.Client(), crssource.Policy{
		AllowedHosts: []string{parsed.Hostname()}, AllowPrivateHosts: true, AllowInsecureHTTP: true,
	})
	store := credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())
	service := NewCRSService(
		NewService(pool, store),
		NewStagedStore(pool, keys),
		client,
	)
	planned, err := service.Plan(ctx, CRSPlanInput{
		TenantID: seed.tenantID, BaseURL: server.URL,
		Username: "operator", Password: "secret",
		Destinations: map[string]AccountDefaults{
			"claude": {
				ProviderID: seed.providerID, ChannelID: seed.channelID,
				NamePrefix: "claude-oauth", AccountType: "oauth",
			},
		},
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		RequestID: "req-crs-claude-oauth-plan", Reason: "CRS Claude OAuth 导入",
	})
	if err != nil || planned.Summary.Ready != 1 || planned.Summary.Failed != 0 ||
		planned.Summary.Conflict != 0 || len(planned.Items) != 1 {
		t.Fatalf("Claude OAuth CRS 计划=%+v err=%v", planned, err)
	}
	item := planned.Items[0]
	if item.Vendor != credentialstore.VendorAnthropic ||
		item.AuthMode != credentialstore.AuthModeClaudeAIOAuth ||
		item.FlowID == "" || item.Plan == nil || len(item.Plan.Items) != 1 {
		t.Fatalf("Claude OAuth CRS 归一或暂存不完整：%+v", item)
	}
	executed, err := service.Execute(ctx, CRSExecuteInput{
		TenantID: seed.tenantID,
		Entries: []CRSExecuteEntry{{
			FlowID: item.FlowID, PlanHash: item.PlanHash,
			Confirmations: item.Plan.Items[0].RequiredConfirmations,
		}},
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		RequestID: "req-crs-claude-oauth-execute", Reason: "确认 CRS Claude OAuth 导入",
	})
	if err != nil || executed.Summary.Completed != 1 || executed.Summary.Conflict != 0 ||
		executed.Summary.Failed != 0 || len(executed.Items) != 1 ||
		executed.Items[0].Result == nil || len(executed.Items[0].Result.Items) != 1 {
		t.Fatalf("Claude OAuth CRS 执行=%+v err=%v", executed, err)
	}
	accountID := executed.Items[0].Result.Items[0].ProviderAccountID
	record, err := store.ResolveActive(ctx, seed.tenantID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer privacy.Zeroize(record.PlaintextPayload)
	var payload map[string]any
	if json.Unmarshal(record.PlaintextPayload, &payload) != nil ||
		payload["access_token"] != "claude-access" || payload["refresh_token"] != "claude-refresh" ||
		record.Vendor != credentialstore.VendorAnthropic ||
		record.AuthMode != credentialstore.AuthModeClaudeAIOAuth {
		t.Fatal("Claude OAuth 活动凭据未按归一结果恢复")
	}
	var completedFlows, accountsCount, healthCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM account_intake_staged_credentials
WHERE tenant_id=$1 AND source_kind='crs_sync' AND status='completed'`,
		seed.tenantID).Scan(&completedFlows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM provider_accounts
WHERE tenant_id=$1 AND deleted_at IS NULL`, seed.tenantID).Scan(&accountsCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM channel_health_state
WHERE tenant_id=$1`, seed.tenantID).Scan(&healthCount); err != nil {
		t.Fatal(err)
	}
	if completedFlows != 1 || accountsCount != 1 || healthCount != 1 {
		t.Fatalf("Claude OAuth CRS 终态 flows=%d accounts=%d health=%d",
			completedFlows, accountsCount, healthCount)
	}
}

func TestCRS错配矩阵不写暂存账号凭据(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("crs-invalid-test", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	accounts := []crssource.Account{
		{SourceType: "claude", SourceID: "1", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth},
		{SourceType: "claude", SourceID: "2", Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeAPIKey},
		{SourceType: "claude_console", SourceID: "3", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey},
		{SourceType: "claude_console", SourceID: "4", Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth},
		{SourceType: "openai_oauth", SourceID: "5", Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth},
		{SourceType: "openai_oauth", SourceID: "6", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey},
		{SourceType: "openai_responses", SourceID: "7", Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeAPIKey},
		{SourceType: "openai_responses", SourceID: "8", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth},
		{SourceType: "gemini_oauth", SourceID: "9", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth},
		{SourceType: "gemini_oauth", SourceID: "10", Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAIStudioAPIKey},
		{SourceType: "gemini_api_key", SourceID: "11", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey},
		{SourceType: "gemini_api_key", SourceID: "12", Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist},
	}
	destinations := map[string]AccountDefaults{}
	for _, sourceType := range []string{
		"claude", "claude_console", "openai_oauth", "openai_responses", "gemini_oauth", "gemini_api_key",
	} {
		destinations[sourceType] = AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: sourceType, AccountType: "oauth",
		}
	}
	service := NewCRSService(
		NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry())),
		NewStagedStore(pool, keys),
		crsSourceIntegrationStub{export: crssource.Export{
			BaseURL: "https://invalid-crs.example.test", Accounts: accounts,
		}},
	)
	planned, err := service.Plan(ctx, CRSPlanInput{
		TenantID: seed.tenantID, BaseURL: "https://ignored.example.test",
		Username: "operator", Password: "secret", Destinations: destinations,
		ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
	})
	if err != nil || planned.Summary.Ready != 0 || planned.Summary.Failed != len(accounts) {
		t.Fatalf("错配计划=%+v err=%v", planned.Summary, err)
	}
	for _, item := range planned.Items {
		if item.Code != "source_mapping_invalid" || item.FlowID != "" || item.Plan != nil {
			t.Fatalf("错配项未在写库前拒绝：%+v", item)
		}
	}
	for table, condition := range map[string]string{
		"account_intake_staged_credentials": "tenant_id=$1",
		"provider_accounts":                 "tenant_id=$1 AND deleted_at IS NULL",
		"account_credentials":               "tenant_id=$1 AND deleted_at IS NULL",
	} {
		var count int
		query := fmt.Sprintf("SELECT count(*)::int FROM %s WHERE %s", table, condition)
		if err := pool.QueryRow(ctx, query, seed.tenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("错配 CRS 在 %s 留下 %d 行", table, count)
		}
	}
}

func Test暂存计划真实JSONB往返后哈希和原始字节不变(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	seed := seedAccountIntake(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE providers SET upstream_protocol='anthropic_claude_session'
WHERE id=$1 AND tenant_id=$2`, seed.providerID, seed.tenantID); err != nil {
		t.Fatal(err)
	}
	keys, err := credentialstore.NewStaticKeyProvider("stage-jsonb-test", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, credentialstore.NewStore(pool, keys, credentialstore.DefaultHandlerRegistry()))
	store := NewStagedStore(pool, keys)
	input := PlanInput{
		TenantID: seed.tenantID, SourceKind: intake.SourceClaudeCookie,
		DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Content: `{"access_token":"jsonb-access","refresh_token":"jsonb-refresh"}`,
		Account: AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			NamePrefix: "jsonb", AccountType: "oauth",
			Extra:                  json.RawMessage(`{"z":1, "a":{"second":2,"first":1}}`),
			TempUnschedulableRules: json.RawMessage(`[{"z":2, "a":1}]`),
		},
		Now: store.nowTime(),
	}
	planned, err := service.Plan(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(ctx, StageInput{
		TenantID: seed.tenantID, ActorID: "platform-owner", ActorRole: admin.RolePlatformAdmin,
		SourceKind: string(input.SourceKind), Vendor: input.DefaultVendor, AuthMode: input.DefaultAuthMode,
		PlanInput: input, PlanHash: planned.PlanHash, Content: input.Content,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadForExecution(ctx, seed.tenantID, "platform-owner", staged.ID, planned.PlanHash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.PlanInput.Account.Extra, input.Account.Extra) ||
		!bytes.Equal(loaded.PlanInput.Account.TempUnschedulableRules, input.Account.TempUnschedulableRules) {
		t.Fatalf("真实 JSONB 往返改变原始字节：extra=%s rules=%s",
			loaded.PlanInput.Account.Extra, loaded.PlanInput.Account.TempUnschedulableRules)
	}
	replanned, err := service.Plan(ctx, loaded.PlanInput)
	if err != nil || replanned.PlanHash != planned.PlanHash {
		t.Fatalf("真实 JSONB 往返后计划漂移：before=%s after=%s err=%v",
			planned.PlanHash, replanned.PlanHash, err)
	}
}

func Test暂存领取隔离租户并在并发下只成功一次(t *testing.T) {
	ctx := context.Background()
	pool := openAccountIntakePool(t, ctx)
	owner := seedAccountIntake(t, ctx, pool)
	other := seedAccountIntake(t, ctx, pool)
	keys, err := credentialstore.NewStaticKeyProvider("stage-claim-test", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStagedStore(pool, keys)
	stage := func(planHash string) StagedCredential {
		t.Helper()
		result, err := store.Stage(ctx, StageInput{
			TenantID: owner.tenantID, ActorID: "shared-actor", ActorRole: admin.RolePlatformAdmin,
			SourceKind: string(intake.SourceClaudeCookie),
			Vendor:     credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
			PlanInput: PlanInput{
				TenantID: owner.tenantID, SourceKind: intake.SourceClaudeCookie,
				DefaultVendor: credentialstore.VendorAnthropic, DefaultAuthMode: credentialstore.AuthModeClaudeAIOAuth,
				Account: AccountDefaults{
					ProviderID: owner.providerID, ChannelID: owner.channelID,
					NamePrefix: "claim", AccountType: "oauth",
				},
				Now: store.nowTime(),
			},
			PlanHash: planHash, Content: `{"access_token":"claim-secret"}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	crossTenant := stage(strings.Repeat("a", 64))
	if _, err := store.Claim(ctx, other.tenantID, "shared-actor", crossTenant.ID, strings.Repeat("a", 64)); !errors.Is(err, ErrStagedCredentialNotFound) {
		t.Fatalf("跨租户领取 err=%v，期望 ErrStagedCredentialNotFound", err)
	}
	if _, err := store.Claim(ctx, owner.tenantID, "shared-actor", crossTenant.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("跨租户拒绝后原租户无法领取：%v", err)
	}

	wrongActor := stage(strings.Repeat("f", 64))
	if _, err := store.Claim(ctx, owner.tenantID, "other-actor", wrongActor.ID, strings.Repeat("f", 64)); !errors.Is(err, ErrStagedCredentialNotFound) {
		t.Fatalf("错误操作者领取 err=%v，期望 ErrStagedCredentialNotFound", err)
	}
	var wrongActorStatus string
	var wrongActorSecretPresent bool
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NOT NULL
FROM account_intake_staged_credentials WHERE id=$1::uuid AND tenant_id=$2`,
		wrongActor.ID, owner.tenantID).Scan(&wrongActorStatus, &wrongActorSecretPresent); err != nil {
		t.Fatal(err)
	}
	if wrongActorStatus != "staged" || !wrongActorSecretPresent {
		t.Fatalf("错误操作者消耗了暂存凭据 status=%s secret_present=%v",
			wrongActorStatus, wrongActorSecretPresent)
	}
	if _, err := store.Claim(ctx, owner.tenantID, "shared-actor", wrongActor.ID, strings.Repeat("f", 64)); err != nil {
		t.Fatalf("错误操作者被拒后原操作者无法领取：%v", err)
	}

	concurrent := stage(strings.Repeat("b", 64))
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, claimErr := store.Claim(ctx, owner.tenantID, "shared-actor", concurrent.ID, strings.Repeat("b", 64))
			results <- claimErr
		}()
	}
	close(start)
	successes, replays := 0, 0
	for range 2 {
		switch claimErr := <-results; {
		case claimErr == nil:
			successes++
		case errors.Is(claimErr, ErrStagedCredentialReplay):
			replays++
		default:
			t.Fatalf("并发领取返回未知错误：%v", claimErr)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("并发领取 successes=%d replays=%d", successes, replays)
	}
	var status string
	var secretCleared bool
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NULL
FROM account_intake_staged_credentials WHERE id=$1::uuid AND tenant_id=$2`,
		concurrent.ID, owner.tenantID).Scan(&status, &secretCleared); err != nil {
		t.Fatal(err)
	}
	if status != "claimed" || !secretCleared {
		t.Fatalf("并发领取终态 status=%s secret_cleared=%v", status, secretCleared)
	}

	planChanged := stage(strings.Repeat("c", 64))
	if _, err := store.Claim(ctx, owner.tenantID, "shared-actor", planChanged.ID, strings.Repeat("d", 64)); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("计划漂移 err=%v，期望 ErrPlanChanged", err)
	}
	var secretPresent bool
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NOT NULL
FROM account_intake_staged_credentials WHERE id=$1::uuid AND tenant_id=$2`,
		planChanged.ID, owner.tenantID).Scan(&status, &secretPresent); err != nil {
		t.Fatal(err)
	}
	if status != "staged" || !secretPresent {
		t.Fatalf("计划漂移错误消耗了暂存凭据 status=%s secret_present=%v", status, secretPresent)
	}
	if _, err := store.Claim(ctx, owner.tenantID, "shared-actor", planChanged.ID, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("计划漂移拒绝后原计划无法领取：%v", err)
	}

	expired := stage(strings.Repeat("e", 64))
	if _, err := pool.Exec(ctx, `
UPDATE account_intake_staged_credentials
SET expires_at=clock_timestamp()-interval '1 second'
WHERE id=$1::uuid AND tenant_id=$2`, expired.ID, owner.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, owner.tenantID, "shared-actor", expired.ID, strings.Repeat("e", 64)); !errors.Is(err, ErrStagedCredentialExpired) {
		t.Fatalf("过期领取 err=%v，期望 ErrStagedCredentialExpired", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT status, encrypted_content IS NULL
FROM account_intake_staged_credentials WHERE id=$1::uuid AND tenant_id=$2`,
		expired.ID, owner.tenantID).Scan(&status, &secretCleared); err != nil {
		t.Fatal(err)
	}
	if status != "expired" || !secretCleared {
		t.Fatalf("过期领取终态 status=%s secret_cleared=%v", status, secretCleared)
	}
}
