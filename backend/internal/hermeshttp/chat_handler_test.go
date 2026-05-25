package hermeshttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

func TestStartChatRejectsDisabledHermesBeforeRunnerCall(t *testing.T) {
	runnerCalled := false
	transport := chatRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		runnerCalled = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	runner, err := hermes.NewRunnerClient(hermes.RunnerConfig{
		RunnerURL:    "http://runner.local",
		SharedSecret: "secret",
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	svc := hermes.NewService(&chatStoreStub{settings: dbhermes.HermesSetting{
		TenantID: 7, UserID: 42, Enabled: false, APISource: hermes.APISourceManaged,
	}})
	router := NewRouter(svc, runner)
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"messages":[]}`))
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, sessionauth.Identity{
		TenantID: 7, APIKeyID: 11, UserID: 42,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"hermes_disabled"}` {
		t.Fatalf("body=%s want hermes_disabled flat error", rec.Body.String())
	}
	if runnerCalled {
		t.Fatalf("runner was called even though Hermes is disabled for the user")
	}
}

func TestCopyProxyResponseFlushesAfterEachChunk(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       &chunkedReadCloser{chunks: []string{"data: one\n\n", "data: two\n\n"}},
	}
	rec := &flushCountingRecorder{ResponseRecorder: httptest.NewRecorder()}

	copyProxyResponse(rec, resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "data: one\n\ndata: two\n\n" {
		t.Fatalf("body=%q want both SSE chunks", got)
	}
	if rec.flushes < 3 {
		t.Fatalf("flushes=%d want at least header flush plus one flush per chunk", rec.flushes)
	}
}

func TestWriteHermesErrorProfileInUse(t *testing.T) {
	rec := httptest.NewRecorder()

	writeHermesError(rec, hermes.ErrProfileInUse)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"profile_in_use","detail":"profile is currently used by settings"}` {
		t.Fatalf("body=%s want profile_in_use flat error", got)
	}
}

func TestWriteHermesErrorAuditRecordFailedLogsAndReturns503(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})
	rec := httptest.NewRecorder()

	writeHermesError(rec, fmt.Errorf("%w: insert failed", hermes.ErrAuditRecordFailed))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hermes_backend_error") {
		t.Fatalf("body=%s want hermes_backend_error", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "hermes audit insert failed") {
		t.Fatalf("logs=%q want audit failure log", logs.String())
	}
}

type chatStoreStub struct {
	settings dbhermes.HermesSetting
}

func (s *chatStoreStub) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *chatStoreStub) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	return 0, nil
}

func (s *chatStoreStub) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *chatStoreStub) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	return 0, nil
}

func (s *chatStoreStub) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *chatStoreStub) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return s.settings, nil
}

func (s *chatStoreStub) InsertAuditEvent(context.Context, dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	return dbhermes.HermesAuditEvent{}, nil
}

func (s *chatStoreStub) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *chatStoreStub) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *chatStoreStub) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return false, nil
}

func (s *chatStoreStub) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

type flushCountingRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushCountingRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

type chunkedReadCloser struct {
	chunks []string
	index  int
}

func (r *chunkedReadCloser) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	return copy(p, chunk), nil
}

func (r *chunkedReadCloser) Close() error {
	return nil
}

type chatRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
