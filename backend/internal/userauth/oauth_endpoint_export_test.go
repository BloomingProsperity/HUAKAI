package userauth_test

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestValidateOAuthEndpointURLExported(t *testing.T) {
	if err := userauth.ValidateOAuthEndpointURL("token_url", "https://oauth2.example.test/token"); err != nil {
		t.Fatalf("public https OAuth endpoint should be accepted: %v", err)
	}
	if err := userauth.ValidateOAuthEndpointURL("token_url", "http://oauth2.example.test/token"); err == nil {
		t.Fatal("plain-http OAuth endpoint should be rejected")
	}
}
