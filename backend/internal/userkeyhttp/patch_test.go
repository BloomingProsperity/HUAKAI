package userkeyhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeyhttp"
)

// fakeKeyServicePatch stubs only the Patch method; others panic to catch misuse.
type fakeKeyServicePatch struct {
	patchResult userkey.PatchResult
	patchErr    error
	patchCalled *userkey.PatchRequest
}

func (f *fakeKeyServicePatch) Issue(_ context.Context, _ userkey.IssueRequest) (userkey.IssueResult, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) List(_ context.Context, _ userkey.ListRequest) ([]userkey.KeyDescriptor, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Get(_ context.Context, _, _, _ int64) (userkey.KeyDescriptor, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Revoke(_ context.Context, _ userkey.RevokeRequest) (userkey.RevokeResult, error) {
	panic("not implemented")
}
func (f *fakeKeyServicePatch) Patch(_ context.Context, req userkey.PatchRequest) (userkey.PatchResult, error) {
	f.patchCalled = &req
	return f.patchResult, f.patchErr
}

func buildPatchRouter(svc userkeyhttp.UserKeyService) chi.Router {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident := sessionauth.SessionIdentity{TenantID: 1, UserID: 2}
			ctx := sessionauth.ContextWithSession(r.Context(), ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	userkeyhttp.MountUserAPIKeyRoutes(r, userkeyhttp.Deps{Service: svc})
	return r
}

// TestKeyPatchPartial is the discriminating test for KEY-026.
//
// MUTATION: PATCH resets omitted fields (e.g., sets status="" when status
// not provided) -> service receives non-nil Status -> status cleared -> RED.
func TestKeyPatchPartial(t *testing.T) {
	const keyID = "42"
	wantName := "new-name"
	wantStatus := "active"

	svc := &fakeKeyServicePatch{
		patchResult: userkey.PatchResult{APIKeyID: 42, Name: wantName, Status: wantStatus},
	}
	r := buildPatchRouter(svc)

	// PATCH with only name — status must NOT be set in the request to the service.
	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPatch, "/"+keyID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the service received nil Status (name-only patch).
	if svc.patchCalled == nil {
		t.Fatal("Patch was not called")
	}
	if svc.patchCalled.Status != nil {
		t.Errorf("MUTATION: Status must be nil when not provided in PATCH body, got %q", *svc.patchCalled.Status)
	}
	if svc.patchCalled.Name == nil || *svc.patchCalled.Name != "new-name" {
		t.Errorf("Name mismatch: got %v", svc.patchCalled.Name)
	}

	// Verify response body
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != wantName {
		t.Errorf("response name: want %q, got %v", wantName, resp["name"])
	}
	if resp["status"] != wantStatus {
		t.Errorf("response status: want %q, got %v", wantStatus, resp["status"])
	}
}

func TestKeyPatchBothFields(t *testing.T) {
	svc := &fakeKeyServicePatch{
		patchResult: userkey.PatchResult{APIKeyID: 5, Name: "n", Status: "revoked"},
	}
	r := buildPatchRouter(svc)

	body, _ := json.Marshal(map[string]string{"name": "n", "status": "revoked"})
	req := httptest.NewRequest(http.MethodPatch, "/5", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.patchCalled.Name == nil || *svc.patchCalled.Name != "n" {
		t.Errorf("Name not set correctly")
	}
	if svc.patchCalled.Status == nil || *svc.patchCalled.Status != "revoked" {
		t.Errorf("Status not set correctly")
	}
}

// expires_at tri-state decode (sub2api-style, CLAUDE.md #16). The handler must turn
// the wire *string into the service's value+clear split exactly.

// SET: a non-empty RFC3339 string -> service receives the parsed deadline, ClearExpiry=false.
// MUTATION: handler drops expires_at / mis-parses -> ExpiresAt nil -> RED.
func TestKeyPatchExpiresAtSet(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"2027-01-02T03:04:05Z"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled == nil {
		t.Fatal("Patch not called")
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be false for a set")
	}
	if svc.patchCalled.ExpiresAt == nil {
		t.Fatalf("ExpiresAt must be non-nil for a set")
	}
	want := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if !svc.patchCalled.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt=%v want %v", svc.patchCalled.ExpiresAt, want)
	}
}

// CLEAR: an empty string -> service receives ClearExpiry=true, ExpiresAt=nil.
// MUTATION: handler treats "" as a set or a parse error -> ClearExpiry false / 400 -> RED.
func TestKeyPatchExpiresAtClear(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if !svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be true for empty string")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("ExpiresAt must be nil for clear, got %v", svc.patchCalled.ExpiresAt)
	}
}

// UNCHANGED: expires_at omitted -> ClearExpiry=false AND ExpiresAt=nil (no accidental clear).
// MUTATION: handler defaults clear=true or fabricates a deadline on omit -> RED. This guards
// the exact footgun the design avoids (omitted must NOT touch the deadline).
func TestKeyPatchExpiresAtUnchanged(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "x", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("ClearExpiry must be false when expires_at omitted (no accidental clear)")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("ExpiresAt must be nil when expires_at omitted, got %v", svc.patchCalled.ExpiresAt)
	}
}

// INVALID: a non-empty, non-RFC3339 string -> 400 invalid_expires_at, service NOT called.
// MUTATION: handler forwards the bad string to the service / returns 200 -> RED.
func TestKeyPatchExpiresAtInvalid(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"nonsense"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_expires_at") {
		t.Errorf("body=%s want invalid_expires_at code", w.Body.String())
	}
	if svc.patchCalled != nil {
		t.Errorf("service must NOT be called on parse failure (no partial write)")
	}
}

// RESPONSE: the PatchResult deadline is echoed back as expires_at.
// MUTATION: handler drops ExpiresAt from patchResponse -> body lacks the timestamp -> RED.
func TestKeyPatchExpiresAtResponse(t *testing.T) {
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active", ExpiresAt: &exp}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":"2027-01-02T03:04:05Z"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "2027-01-02T03:04:05Z") {
		t.Errorf("response body=%s want expires_at echoed", w.Body.String())
	}
}

// CLEAR RESPONSE: a cleared (never-expiring) key must OMIT expires_at from the body.
// The omit relies on a nil *time.Time + `json:"...,omitempty"` (handlers.go patchResponse).
// MUTATION: drop ,omitempty (ships "expires_at":null) or switch the field to value time.Time
// (ships "0001-01-01T00:00:00Z", which the frontend renders as a real past deadline) -> body
// contains expires_at -> RED. The existing SET test pins the present-case; this pins the omit-case.
func TestKeyPatchExpiresAtClearResponseOmits(t *testing.T) {
	// patchResult.ExpiresAt nil = the post-clear (never-expiring) state.
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "expires_at") {
		t.Errorf("cleared/never-expiring key must omit expires_at, got %s", w.Body.String())
	}
}

// NULL == ABSENT: explicit JSON null must behave identically to omitting expires_at (unchanged).
// Because patchRequest.ExpiresAt is *string, null decodes to a nil pointer, byte-identical to
// absent. MUTATION: a future switch to json.RawMessage / custom unmarshaler that treats JSON null
// as clear -> ClearExpiry true -> RED. Pins the null==absent contract the handler/OpenAPI promise.
func TestKeyPatchExpiresAtNullIsUnchanged(t *testing.T) {
	svc := &fakeKeyServicePatch{patchResult: userkey.PatchResult{APIKeyID: 7, Name: "n", Status: "active"}}
	r := buildPatchRouter(svc)
	req := httptest.NewRequest(http.MethodPatch, "/7", strings.NewReader(`{"expires_at":null}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", w.Code, w.Body.String())
	}
	if svc.patchCalled.ClearExpiry {
		t.Errorf("JSON null must mean unchanged, not clear")
	}
	if svc.patchCalled.ExpiresAt != nil {
		t.Errorf("JSON null must leave ExpiresAt nil, got %v", svc.patchCalled.ExpiresAt)
	}
}
