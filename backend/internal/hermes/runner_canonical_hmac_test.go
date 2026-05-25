package hermes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerHMAC_CanonicalIncludesMethodPath(t *testing.T) {
	// Regression: method must be signed so a captured GET cannot be replayed as POST on the same runner path.
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	getSig := runnerSignatureFor(t, http.MethodGet, "/chat", "", 7, 42, body)
	postSig := runnerSignatureFor(t, http.MethodPost, "/chat", "", 7, 42, body)

	// Mutation check: 删除 sign() 中 method 写入 canonical string,GET/POST signature 会相等并触发此断言。
	if getSig == postSig {
		t.Fatalf("GET and POST signatures matched: %s", getSig)
	}
}

func TestRunnerHMAC_CanonicalIncludesQuery(t *testing.T) {
	// Regression: raw query must be signed so cursor/limit cannot be replay-swapped across runner reads.
	body := []byte(`{"messages":[]}`)

	first := runnerSignatureFor(t, http.MethodGet, "/conversations", "cursor=a", 7, 42, body)
	second := runnerSignatureFor(t, http.MethodGet, "/conversations", "cursor=b", 7, 42, body)

	// Mutation check: 删除 sign() 中 rawQuery 写入,两个不同 query 会得到同一 signature 并失败。
	if first == second {
		t.Fatalf("query signatures matched: %s", first)
	}
}

func TestRunnerHMAC_CanonicalIncludesTenantUser(t *testing.T) {
	// Regression: tenant/user must be signed so a runner request cannot be replayed across identities.
	body := []byte(`{"messages":[]}`)

	base := runnerSignatureFor(t, http.MethodPost, "/chat", "stream=true", 7, 42, body)
	otherTenant := runnerSignatureFor(t, http.MethodPost, "/chat", "stream=true", 8, 42, body)
	otherUser := runnerSignatureFor(t, http.MethodPost, "/chat", "stream=true", 7, 43, body)

	// Mutation check: 删除 tenant 写入会让 base/otherTenant 相等；删除 user 写入会让 base/otherUser 相等。
	if base == otherTenant || base == otherUser {
		t.Fatalf("identity signatures not discriminating: base=%s tenant=%s user=%s", base, otherTenant, otherUser)
	}
}

func TestRunnerHMAC_FreshnessRejected(t *testing.T) {
	// Regression: the checked-in runner verifier must reject a valid HMAC whose timestamp is older than 5 minutes.
	mainPath := filepath.Clean("../../deploy/hermes-runner/main.py")
	script := `
import ast
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
module = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
keep_imports = {"hashlib", "hmac", "os", "time"}
keep_assigns = {
    "SECRET_ENV",
    "HEADER_SIGNATURE",
    "HEADER_TIMESTAMP",
    "HEADER_TENANT",
    "HEADER_USER",
    "FRESHNESS_SECONDS",
}
keep_funcs = {
    "_shared_secret",
    "_raw_query",
    "_canonical",
    "_valid_timestamp",
    "_valid_signature",
}
selected = []
for node in module.body:
    if isinstance(node, ast.Import) and any(alias.name in keep_imports for alias in node.names):
        selected.append(node)
    elif isinstance(node, ast.ImportFrom) and node.module == "typing":
        selected.append(node)
    elif isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id in keep_assigns for t in node.targets):
        selected.append(node)
    elif isinstance(node, ast.FunctionDef) and node.name in keep_funcs:
        selected.append(node)
ns = {"Request": object}
exec(compile(ast.Module(body=selected, type_ignores=[]), str(path), "exec"), ns)

now = 1_700_000_000
ns["_valid_timestamp"].__defaults__ = (lambda: now,)
ns["os"].environ[ns["SECRET_ENV"]] = "runner-secret"

class URL:
    path = "/chat"

class RequestStub:
    method = "POST"
    url = URL()
    scope = {"query_string": b"stream=true"}
    headers = {}

def signed_request(ts):
    body = b'{"messages":[]}'
    req = RequestStub()
    req.headers = {
        ns["HEADER_TIMESTAMP"]: str(ts),
        ns["HEADER_TENANT"]: "7",
        ns["HEADER_USER"]: "42",
    }
    req.headers[ns["HEADER_SIGNATURE"]] = ns["hmac"].new(
        b"runner-secret",
        ns["_canonical"](str(ts), req.method, req.url.path, "stream=true", "7", "42", body),
        ns["hashlib"].sha256,
    ).hexdigest()
    return req, body

fresh_req, fresh_body = signed_request(now - 300)
if not ns["_valid_signature"](fresh_req, fresh_body):
    raise SystemExit("fresh boundary signature rejected; fixture is not valid")

stale_req, stale_body = signed_request(now - 360)
if ns["_valid_signature"](stale_req, stale_body):
    raise SystemExit("stale signature accepted")
`
	cmd := exec.Command("python3", "-c", script, mainPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runner freshness verifier failed: %v\n%s", err, out)
	}
	// Mutation check: 删除 main.py::_valid_signature 中 _valid_timestamp(ts) guard,stale signature 会被 Python fixture 接受并让本测试失败。
	if strings.Contains(string(out), "stale signature accepted") {
		t.Fatalf("runner accepted stale signature: %s", out)
	}
}

func runnerSignatureFor(t *testing.T, method, path, rawQuery string, tenantID, userID int64, body []byte) string {
	t.Helper()
	var seenSignature string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenSignature = r.Header.Get(HeaderSignature)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	client, err := NewRunnerClient(RunnerConfig{
		RunnerURL:    "http://runner.local",
		SharedSecret: "runner-secret",
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	client.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	resp, err := client.do(context.Background(), method, path, rawQuery, tenantID, userID, body, "application/json")
	if err != nil {
		t.Fatalf("runner do %s %s?%s: %v", method, path, rawQuery, err)
	}
	_ = resp.Body.Close()
	if seenSignature == "" {
		t.Fatalf("empty runner signature for %s", fmt.Sprintf("%s %s?%s", method, path, rawQuery))
	}
	return seenSignature
}
