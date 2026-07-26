package codexagentimport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestParseBatchNormalizesNestedAndFlatDocuments(t *testing.T) {
	key := importPrivateKey(t)
	input := `[{"auth_mode":"agentIdentity","agent_identity":{"agentRuntimeId":"runtime-a","agentPrivateKey":"` + key + `","taskId":"task-a"},"accountId":"account-a","chatgptUserId":"user-a","email":"a@example.test","planType":"plus","userAgent":"codex-agent/1.0","originator":"codex_cli_rs","oaiDeviceId":"device-a"},` +
		`{"agent_runtime_id":"runtime-b","agent_private_key":"` + key + `","account_id":"account-b","chatgpt_user_id":"user-b","chatgpt_plan_type":"pro"}]`
	candidates, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("count=%d", len(candidates))
	}
	for index, candidate := range candidates {
		if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexAgent {
			t.Fatalf("candidate[%d]=%+v", index, candidate)
		}
	}
	if candidates[0].ExternalAccountID != "account-a" || candidates[0].ExternalSubjectID != "user-a" || candidates[0].Subscription.Label() != "openai:plus" {
		t.Fatalf("first=%+v subscription=%+v", candidates[0], candidates[0].Subscription)
	}
	if candidates[1].Subscription.Label() != "openai:pro" {
		t.Fatalf("second subscription=%+v", candidates[1].Subscription)
	}
	var payload map[string]any
	_ = json.Unmarshal(candidates[0].Payload, &payload)
	if payload["agent_runtime_id"] != "runtime-a" || payload["task_id"] != "task-a" ||
		payload["user_agent"] != "codex-agent/1.0" || payload["originator"] != "codex_cli_rs" ||
		payload["oai_device_id"] != "device-a" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestParseRejectsRawTokenWrongModeAndInjectedEndpoint(t *testing.T) {
	if _, err := Parse("raw-token"); err == nil {
		t.Fatal("裸 token 未被拒绝")
	}
	key := importPrivateKey(t)
	wrong := `{"auth_mode":"chatgpt","agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`
	if _, err := Parse(wrong); err == nil {
		t.Fatal("错误模式未被拒绝")
	}
	input := `{"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u","base_url":"https://attacker.test"}`
	candidates, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(candidates[0].Payload), "attacker.test") {
		t.Fatalf("租户输入的目标地址进入凭据：%s", candidates[0].Payload)
	}
}

func TestParseRejectsAgentIdentityMintDirective(t *testing.T) {
	key := importPrivateKey(t)
	for _, input := range []string{
		`{"mint_agent_identity":true,"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"agent_identity":{"mintAgentIdentity":false,"agent_runtime_id":"runtime","agent_private_key":"` + key + `"},"account_id":"a","chatgpt_user_id":"u"}`,
		`{"mint":true,"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"agent_identity":{"metadata":{"mint_identity":true},"agent_runtime_id":"runtime","agent_private_key":"` + key + `"},"account_id":"a","chatgpt_user_id":"u"}`,
		`{"action":"mint_agent_identity","agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"actions":["inspect","mint_agent_identity"],"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"action":{"name":"mint"},"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"commands":[{"name":"mint_agent_identity"}],"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
		`{"operation":{"type":"mintAgentIdentity"},"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u"}`,
	} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Agent Identity 铸造指令未被拒绝：%s", input)
		}
	}
}

func TestParseAllowsUnrelatedMintMetadata(t *testing.T) {
	key := importPrivateKey(t)
	input := `{
		"minted_at":"2026-07-26T00:00:00Z",
		"admin_token":"metadata-only",
		"metadata":{"minted_by":"upstream"},
		"action":"sync_minted_at_metadata",
		"agent_runtime_id":"runtime",
		"agent_private_key":"` + key + `",
		"account_id":"a",
		"chatgpt_user_id":"u"
	}`
	candidates, err := Parse(input)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("合法元数据被误判为铸造指令：candidates=%d err=%v", len(candidates), err)
	}
}

func TestParseRejectsUnsafeHTTPHeaderMetadata(t *testing.T) {
	key := importPrivateKey(t)
	for _, metadata := range []string{
		`"user_agent":"safe\r\ninjected"`,
		`"originator":"safe\u007finvalid"`,
		`"oai_device_id":"` + strings.Repeat("x", 1025) + `"`,
	} {
		input := `{"agent_runtime_id":"runtime","agent_private_key":"` + key + `","account_id":"a","chatgpt_user_id":"u",` + metadata + `}`
		if _, err := Parse(input); err == nil {
			t.Fatalf("不安全头元数据未被拒绝：%s", metadata)
		}
	}
}

func importPrivateKey(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
