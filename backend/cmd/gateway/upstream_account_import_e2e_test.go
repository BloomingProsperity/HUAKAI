//go:build e2e_upstream

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const upstreamE2EAccountImportCapability = "advanced_account_intake"

func TestUpstreamE2E_FormalAccountImport(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL 未设")
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pgPool, err := db.Open(setupCtx, db.PoolConfig{DSN: dsn})
	if err != nil {
		setupCancel()
		t.Fatalf("打开正式导入 e2e 数据库: %v", err)
	}
	t.Cleanup(pgPool.Close)

	testCase := upstreamE2ECase{
		slug: "formal-account-import", vendor: credentialstore.VendorAnthropic,
		protocolFamily: registrydefault.ProtocolAnthropicMessages,
		model:          "claude-formal-import-e2e", authMode: credentialstore.AuthModeAPIKey,
		accountType:          upstreamE2EAccountTypeAPIKey,
		expectImportIdentity: true, skipConcurrency: true,
	}
	credential := upstreamE2ECredential{payload: []byte(`{"api_key":"synthetic-import-e2e-key","external_account_id":"synthetic-upstream-account"}`)}
	seed := seedUpstreamE2EGraph(t, setupCtx, pgPool, testCase, credential)
	setupCancel()
	binPath := buildUpstreamE2EGateway(t)
	defer os.Remove(binPath)
	addr := reserveUpstreamE2ELocalPort(t)
	processes := startUpstreamE2EGateway(t, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopUpstreamE2EGateway(processes) })
	waitForUpstreamE2EGateway(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}
	seed.providerAccountID = importUpstreamE2EAccount(t, ctx, client, addr, pgPool, seed, credential)
	assertUpstreamE2ESeedSelectable(t, ctx, pgPool, seed)
	waitForUpstreamE2EInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
}

type upstreamE2EAccountImportRequest struct {
	TenantID        int64                         `json:"tenant_id"`
	SourceKind      intake.SourceKind             `json:"source_kind"`
	DefaultVendor   string                        `json:"default_vendor"`
	DefaultAuthMode string                        `json:"default_auth_mode"`
	Content         string                        `json:"content"`
	Account         accountintake.AccountDefaults `json:"account"`
	PlanHash        string                        `json:"plan_hash,omitempty"`
	Confirmations   []string                      `json:"confirmations,omitempty"`
	Reason          string                        `json:"reason,omitempty"`
}

func seedUpstreamE2EImportAuthorization(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *upstreamE2ESeed, unique string) {
	t.Helper()
	seed.adminBearer = "hk_admin_e2e_" + strings.ReplaceAll(unique, "-", "")
	prefix := seed.adminBearer
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(seed.adminBearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成 e2e 管理令牌哈希: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `
INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, status)
VALUES ($1,$2,$3,'tenant_operator',$4,'active')
RETURNING id`, "upstream-import-e2e-"+unique, string(hash), prefix, seed.tenantID).Scan(&seed.adminTokenID); err != nil {
		t.Fatalf("写入 e2e 租户管理员令牌: %v", err)
	}
	if _, err := pgPool.Exec(ctx, `
INSERT INTO tenant_admin_capability_grants (
    tenant_id, capability, enabled, updated_by, reason, granted_at, revoked_at
) VALUES ($1,$2,true,$3,$4,clock_timestamp(),NULL)`,
		seed.tenantID, upstreamE2EAccountImportCapability,
		fmt.Sprintf("admin_token:%d", seed.adminTokenID), "真实上游端到端测试正式导入授权",
	); err != nil {
		t.Fatalf("写入 e2e 账号导入能力授权: %v", err)
	}
}

func importUpstreamE2EAccount(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr string,
	pgPool *pgxpool.Pool,
	seed *upstreamE2ESeed,
	credential upstreamE2ECredential,
) int64 {
	t.Helper()
	if len(credential.payload) == 0 {
		t.Fatal("正式账号导入测试缺少结构化凭据载荷")
	}
	authMode := credentialstore.Normalize(seed.testCase.authMode)
	if authMode == "" {
		authMode = credentialstore.AuthModeAPIKey
	}
	content := string(credential.payload)
	request := upstreamE2EAccountImportRequest{
		TenantID: seed.tenantID, SourceKind: intake.SourceJSON,
		DefaultVendor: seed.testCase.vendor, DefaultAuthMode: authMode,
		Content: content,
		Account: accountintake.AccountDefaults{
			ProviderID: seed.providerID, ChannelID: seed.channelID,
			ExactName:       seed.testCase.slug + "-正式导入-" + uuid.NewString(),
			AccountType:     seed.testCase.accountType,
			CapConcurrency:  int32Pointer(seed.testCase.accountCap()),
			Priority:        int32Pointer(100),
			ModelAllowList:  []string{seed.testCase.model},
			CapabilityFlags: []string{"stream", "tools", "vision", "json", "audio", "file"},
		},
	}

	var planned accountintake.PlanResult
	planBody := postUpstreamE2EAccountImport(t, ctx, client, addr,
		"/admin/v1/credentials/account-imports/plan", seed.adminBearer, request)
	defer privacy.Zeroize(planBody)
	if err := json.Unmarshal(planBody, &planned); err != nil {
		t.Fatalf("解析账号导入预检响应: %v body=%s", err, safeUpstreamE2EBody(planBody))
	}
	if planned.PlanHash == "" || planned.Plan.Summary.Create != 1 || len(planned.Plan.Items) != 1 {
		t.Fatalf("账号导入预检未形成唯一创建动作: %s", safeUpstreamE2EBody(planBody))
	}
	request.PlanHash = planned.PlanHash
	request.Confirmations = append([]string(nil), planned.Plan.Items[0].RequiredConfirmations...)
	request.Reason = "真实上游端到端测试正式导入"

	var executed accountintake.ExecutionResult
	executeBody := postUpstreamE2EAccountImport(t, ctx, client, addr,
		"/admin/v1/credentials/account-imports/execute", seed.adminBearer, request)
	defer privacy.Zeroize(executeBody)
	if err := json.Unmarshal(executeBody, &executed); err != nil {
		t.Fatalf("解析账号导入执行响应: %v body=%s", err, safeUpstreamE2EBody(executeBody))
	}
	if executed.Summary.Created != 1 || len(executed.Items) != 1 ||
		executed.Items[0].Status != accountintake.StatusCreated || executed.Items[0].ProviderAccountID <= 0 {
		t.Fatalf("正式导入没有原子创建账号与凭据: %s", safeUpstreamE2EBody(executeBody))
	}
	if !executed.Items[0].ChannelHealthInitialized {
		t.Fatalf("正式导入未初始化渠道健康: %s", safeUpstreamE2EBody(executeBody))
	}
	accountID := executed.Items[0].ProviderAccountID
	assertUpstreamE2EImportedAccount(t, ctx, pgPool, seed, accountID, executed.Items[0])
	assertUpstreamE2EImportResponseRedacted(t, credential.payload, planBody, executeBody)
	return accountID
}

func assertUpstreamE2EImportResponseRedacted(t *testing.T, credentialPayload []byte, responses ...[]byte) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(credentialPayload, &decoded); err != nil {
		t.Fatalf("解析待检查的凭据载荷: %v", err)
	}
	secrets := upstreamE2ECredentialSecrets(decoded)
	for _, response := range responses {
		if bytes.Contains(response, credentialPayload) {
			t.Fatal("账号导入响应回显了完整凭据载荷")
		}
		for _, secret := range secrets {
			if bytes.Contains(response, []byte(secret)) {
				t.Fatal("账号导入响应回显了凭据中的敏感字段")
			}
		}
	}
}

func upstreamE2ECredentialSecrets(value any) []string {
	var out []string
	var walk func(any, string)
	walk = func(current any, parentKey string) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(child, strings.ToLower(strings.TrimSpace(key)))
			}
		case []any:
			for _, child := range typed {
				walk(child, parentKey)
			}
		case string:
			if !upstreamE2ESecretField(parentKey) || strings.TrimSpace(typed) == "" {
				return
			}
			out = append(out, typed)
		}
	}
	walk(value, "")
	return out
}

func upstreamE2ESecretField(name string) bool {
	compact := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	if compact == "token" || strings.HasSuffix(compact, "token") ||
		strings.Contains(compact, "apikey") || strings.Contains(compact, "privatekey") ||
		strings.Contains(compact, "secret") {
		return true
	}
	switch compact {
	case "authorization", "authheadervalue", "cookie", "sessionkey", "password",
		"pkce", "code", "verifier", "awsaccesskeyid", "encryptedtaskid":
		return true
	}
	return false
}

func postUpstreamE2EAccountImport(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	addr string,
	path string,
	adminBearer string,
	payload upstreamE2EAccountImportRequest,
) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码账号导入请求: %v", err)
	}
	defer privacy.Zeroize(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造账号导入请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminBearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("调用账号导入接口: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatalf("读取账号导入响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("账号导入接口 %s status=%d，响应正文因可能包含凭据已隐去", path, resp.StatusCode)
	}
	return responseBody
}

func assertUpstreamE2EImportedAccount(
	t *testing.T,
	ctx context.Context,
	pgPool *pgxpool.Pool,
	seed *upstreamE2ESeed,
	accountID int64,
	item accountintake.ExecutionItem,
) {
	t.Helper()
	var (
		enabled         bool
		credentialState string
		credentialCount int
		modelAllowList  []string
		capabilityFlags []string
		tags            []string
		externalAccount string
		identitySource  string
	)
	if err := pgPool.QueryRow(ctx, `
SELECT pa.enabled, pa.credential_state, pa.model_allow_list, pa.capability_flags, pa.tags,
       count(ac.id)::int, COALESCE(max(ac.external_account_id), ''),
       COALESCE(max(ac.external_identity_source), '')
FROM provider_accounts pa
LEFT JOIN account_credentials ac
  ON ac.tenant_id=pa.tenant_id AND ac.provider_account_id=pa.id AND ac.state='active'
WHERE pa.tenant_id=$1 AND pa.id=$2
GROUP BY pa.id`, seed.tenantID, accountID).Scan(
		&enabled, &credentialState, &modelAllowList, &capabilityFlags, &tags,
		&credentialCount, &externalAccount, &identitySource,
	); err != nil {
		t.Fatalf("读取正式导入账号投影: %v", err)
	}
	if !enabled || credentialState != "valid" || credentialCount != 1 {
		t.Fatalf("正式导入账号不可运行: enabled=%t credential_state=%s credentials=%d",
			enabled, credentialState, credentialCount)
	}
	if !stringSliceContains(modelAllowList, seed.testCase.model) {
		t.Fatalf("正式导入账号缺少模型白名单 %q: %v", seed.testCase.model, modelAllowList)
	}
	for _, required := range []string{"stream", "tools", "vision", "json", "audio", "file"} {
		if !stringSliceContains(capabilityFlags, required) {
			t.Fatalf("正式导入账号缺少能力 %q: %v", required, capabilityFlags)
		}
	}
	if seed.testCase.expectImportIdentity && (externalAccount == "" || identitySource != "import_payload") {
		t.Fatalf("正式导入身份投影缺失: external_account=%q source=%q", externalAccount, identitySource)
	}
	if !seed.testCase.expectImportIdentity && identitySource != "" && identitySource != "import_payload" {
		t.Fatalf("正式导入身份来源异常: %q", identitySource)
	}
	for _, label := range item.SystemLabels {
		if stringSliceContains(tags, label) {
			t.Fatalf("系统套餐标签不得混入人工标签: label=%q tags=%v", label, tags)
		}
		var storedLabel string
		if err := pgPool.QueryRow(ctx, `
SELECT vendor || ':' || normalized_plan
FROM provider_account_subscription_states
WHERE tenant_id=$1 AND provider_account_id=$2`, seed.tenantID, accountID).Scan(&storedLabel); err != nil {
			t.Fatalf("套餐标签 %q 未持久化到独立投影: %v", label, err)
		}
		if storedLabel != label {
			t.Fatalf("套餐投影标签=%q，执行响应标签=%q", storedLabel, label)
		}
	}
}

func int32Pointer(value int) *int32 {
	converted := int32(value)
	return &converted
}

func stringSliceContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
