package credentialacq

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestParseCodexAgentIdentityContentNormalizesBatchAndPinsMode(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(der)
	input, _ := json.Marshal([]map[string]any{
		{"agent_runtime_id": "runtime-a", "agent_private_key": encoded, "chatgpt_account_id": "account-a", "chatgpt_user_id": "user-a", "task_id": "task-a", "vendor": "attacker"},
		{"agentIdentity": map[string]any{"agentRuntimeId": "runtime-b", "agentPrivateKey": encoded, "accountId": "account-b", "chatgptUserId": "user-b", "chatgptAccountIsFedramp": true}},
	})
	candidates, err := ParseCodexAgentIdentityContent(string(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates=%d want 2", len(candidates))
	}
	for _, candidate := range candidates {
		if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeCodexAgentIdentity {
			t.Fatalf("mode=%s/%s", candidate.Vendor, candidate.AuthMode)
		}
		var payload map[string]any
		if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if _, leaked := payload["vendor"]; leaked {
			t.Fatalf("导入方路由字段泄入凭据: %v", payload)
		}
	}
	var second map[string]any
	if err := json.Unmarshal(candidates[1].Payload, &second); err != nil {
		t.Fatal(err)
	}
	if second["fedramp"] != true {
		t.Fatalf("fedramp=%v want true", second["fedramp"])
	}
	invalidFedRAMP, _ := json.Marshal(map[string]any{
		"runtime_id": "runtime-c", "private_key_pkcs8": encoded,
		"upstream_account_id": "account-c", "upstream_user_id": "user-c",
		"fedramp": "true",
	})
	if _, err := ParseCodexAgentIdentityContent(string(invalidFedRAMP)); err == nil {
		t.Fatal("非布尔 fedramp 被接受")
	}
	conflictingFedRAMP, _ := json.Marshal(map[string]any{
		"runtime_id": "runtime-d", "private_key_pkcs8": encoded,
		"upstream_account_id": "account-d", "upstream_user_id": "user-d",
		"fedramp": true, "chatgptAccountIsFedramp": false,
	})
	if _, err := ParseCodexAgentIdentityContent(string(conflictingFedRAMP)); err == nil {
		t.Fatal("冲突 fedramp 别名被接受")
	}
}

func TestParseCodexAgentIdentityContentRejectsTokenAndNonEd25519Key(t *testing.T) {
	for _, input := range []string{
		"plain-token",
		`{"runtime_id":"r","private_key_pkcs8":"bm90LWtleQ==","upstream_account_id":"a","upstream_user_id":"u"}`,
		`[{"runtime_id":"r"},"token"]`,
		`{"runtime_id":"r","private_key_pkcs8":"bm90LWtleQ==","upstream_account_id":"a","upstream_user_id":"u","fedramp":"true"}`,
	} {
		if _, err := ParseCodexAgentIdentityContent(input); err == nil {
			t.Fatalf("非法输入被接受: %s", input)
		}
	}
}
