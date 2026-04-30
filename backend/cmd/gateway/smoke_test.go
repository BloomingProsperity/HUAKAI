//go:build smoke

// Phase C.4 end-to-end smoke test. Builds the gateway binary, runs it in
// a subprocess pointed at the dev PostgreSQL container, posts one chat
// completions request, and asserts BOTH HTTP correctness AND PG row state.
//
// This is the gating gate for Phase C — if all 5 PG-state assertions pass
// the binary is genuinely billing through real DB rows.

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

const (
	smokeBearer        = "phase-c-smoke-bearer"
	smokeBinaryName    = "gateway-smoke.exe"
	smokeBootRetries   = 30
	smokeBootRetryWait = 200 * time.Millisecond
)

func TestPhaseC_Smoke_ChatCompletions(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open dev pool: %v", err)
	}
	defer pgPool.Close()

	seed := seedSmokeGraph(t, ctx, pgPool)

	binPath := buildGateway(t)
	defer os.Remove(binPath)

	addr := reserveLocalPort(t)
	cmd := startGateway(t, ctx, binPath, dsn, addr, seed)
	t.Cleanup(func() { stopGateway(cmd) })

	waitForGateway(t, addr)

	// POST request.
	body := fmt.Sprintf(`{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true,"pool_group_id":%d}`, seed.poolGroupID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/v1/chat/completions", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+smokeBearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200; got %d body=%s", resp.StatusCode, string(raw))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream; got %q", got)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(respBody) == 0 {
		t.Fatalf("empty SSE response body")
	}
	if !bytes.Contains(respBody, []byte("data:")) {
		t.Fatalf("response body has no SSE data: lines: %s", respBody)
	}

	// Assert PG state. Must look up the seeded tenant's claim row.
	checkPGState(t, ctx, pgPool, seed)
}

type smokeSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
}

func seedSmokeGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) *smokeSeed {
	t.Helper()
	unique := uuid.NewString()
	s := &smokeSeed{}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"smoke-tenant-"+unique,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	s.apiKeyID = s.tenantID*100 + 1
	s.userID = s.tenantID*100 + 2

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, s.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM tenants WHERE id=$1`, s.tenantID)
	})

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		s.tenantID, "smoke-p-"+unique, "Provider "+unique,
	).Scan(&s.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "smoke-pg-"+unique,
	).Scan(&s.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		s.tenantID, s.poolGroupID, "smoke-ch-"+unique,
	).Scan(&s.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count
		) VALUES ($1, $2, $3, $4, 'api_key', 4, 2) RETURNING id`,
		s.tenantID, s.providerID, s.channelID, "smoke-acct-"+unique,
	).Scan(&s.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	return s
}

func buildGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRoot(t)
	// Build into the module root so ./gateway-smoke.exe is findable from
	// any cwd where the binary subprocess starts. The build is robust
	// against both `go test ./cmd/gateway` (cwd=cmd/gateway) and
	// `go test -c + manual ./smoke.test.exe` (cwd=$pwd) — Codex pass1+2
	// caught both wrong-cwd scenarios.
	binPath := moduleRoot + "/" + smokeBinaryName
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build gateway from %s: %v", moduleRoot, err)
	}
	return binPath
}

func goModuleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := string(bytes.TrimSpace(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatalf("not in a Go module")
	}
	// gomod is .../go.mod; strip the trailing /go.mod.
	const suffix = "/go.mod"
	const winSuffix = `\go.mod`
	switch {
	case len(gomod) > len(suffix) && gomod[len(gomod)-len(suffix):] == suffix:
		return gomod[:len(gomod)-len(suffix)]
	case len(gomod) > len(winSuffix) && gomod[len(gomod)-len(winSuffix):] == winSuffix:
		return gomod[:len(gomod)-len(winSuffix)]
	default:
		t.Fatalf("unexpected GOMOD path: %q", gomod)
		return ""
	}
}

// reserveLocalPort opens a TCP listener on a random localhost port,
// closes it, and returns the addr the gateway should bind to. There is
// a TOCTOU race between Close() and the gateway re-binding, but the
// alternative (the gateway picking a port and writing it to stdout) is
// more code for a Phase C smoke test.
func reserveLocalPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	return addr
}

func startGateway(t *testing.T, _ context.Context, binPath, dsn, addr string, seed *smokeSeed) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_SMOKE_BEARER_TOKEN="+smokeBearer,
		"HUAKAI_SMOKE_TENANT_ID="+strconv.FormatInt(seed.tenantID, 10),
		"HUAKAI_SMOKE_API_KEY_ID="+strconv.FormatInt(seed.apiKeyID, 10),
		"HUAKAI_SMOKE_USER_ID="+strconv.FormatInt(seed.userID, 10),
	)
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	go drainPipe("gateway-stderr", stderr)
	go drainPipe("gateway-stdout", stdout)
	return cmd
}

func drainPipe(label string, r io.ReadCloser) {
	if r == nil {
		return
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, scanner.Text())
	}
}

func stopGateway(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func waitForGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < smokeBootRetries; i++ {
		// We don't have /healthz; use a non-API GET that should 404 quickly.
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(smokeBootRetryWait)
	}
	t.Fatalf("gateway did not start listening on %s within %v",
		addr, time.Duration(smokeBootRetries)*smokeBootRetryWait)
}

func checkPGState(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *smokeSeed) {
	t.Helper()

	var claimID int64
	var status string
	if err := pgPool.QueryRow(ctx,
		`SELECT id, status FROM billing_ledger_claims WHERE tenant_id=$1 AND status='committed'`,
		seed.tenantID,
	).Scan(&claimID, &status); err != nil {
		t.Fatalf("PG check 1 (committed claim): %v", err)
	}
	if status != "committed" {
		t.Fatalf("PG check 1: expected committed; got %q", status)
	}

	var usageCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM usage_records WHERE claim_id=$1`, claimID,
	).Scan(&usageCount); err != nil {
		t.Fatalf("PG check 2 (usage_records): %v", err)
	}
	if usageCount != 1 {
		t.Fatalf("PG check 2: expected 1 usage_record; got %d", usageCount)
	}

	var eventCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'`, claimID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("PG check 3 (billing_events): %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("PG check 3: expected 1 claim_committed event; got %d", eventCount)
	}

	var inFlight int32
	if err := pgPool.QueryRow(ctx,
		`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, seed.providerAccountID,
	).Scan(&inFlight); err != nil {
		t.Fatalf("PG check 4 (in_flight_count): %v", err)
	}
	// seed had in_flight=2; +1 acquire then -1 release on settle = back to 2.
	if inFlight != 2 {
		t.Fatalf("PG check 4: expected in_flight 2 (round-trip); got %d", inFlight)
	}

	var slotCount int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*) FROM pool_slot_acquisitions WHERE claim_id=$1 AND status='released_success'`, claimID,
	).Scan(&slotCount); err != nil {
		t.Fatalf("PG check 5 (released slot): %v", err)
	}
	if slotCount != 1 {
		t.Fatalf("PG check 5: expected 1 released_success slot; got %d", slotCount)
	}
}
