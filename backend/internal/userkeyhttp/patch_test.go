package userkeyhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
