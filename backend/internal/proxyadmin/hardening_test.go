package proxyadmin

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestProxyTypeIsSecretFree is a structural defense-in-depth assertion (review
// F1): the secret-free guarantee of the read surface ultimately rests on the
// Proxy type never carrying a credential field. If someone adds an AuthSecret
// (or any "*secret*"/"*password*"/"*credential*") field to Proxy, the encrypted
// proxy credential could be one careless mapper away from a response. This test
// fails the moment such a field appears, before any leak can ship.
// MUTATION: add `AuthSecret *string` to Proxy -> RED.
func TestProxyTypeIsSecretFree(t *testing.T) {
	rt := reflect.TypeOf(Proxy{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, banned := range []string{"secret", "password", "credential", "passwd", "token"} {
			if strings.Contains(name, banned) {
				t.Fatalf("Proxy must stay secret-free; field %q looks credential-bearing (contains %q)", rt.Field(i).Name, banned)
			}
		}
	}
}

// TestValidatePortUpperBound guards review F3: a proxy port must be in 1..65535.
// Before the fix only port<=0 was rejected, so 70000 (or any >65535) was accepted
// and would later fail at connect time or drift from the OpenAPI maximum:65535.
// MUTATION: drop the `|| port > 65535` clause in validateCommon -> the 70000
// cases below are accepted -> RED.
func TestValidatePortUpperBound(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	base := CreateInput{TenantID: 7, Name: "p", Protocol: "http", Host: "h"}

	// Above the 16-bit ceiling: rejected at validation, before any DB call.
	in := base
	in.Port = 70000
	if _, err := New(&mockProxyQuerier{}, keys).Create(ctx, in); err != ErrInvalidInput {
		t.Fatalf("port 70000 must be ErrInvalidInput, got %v", err)
	}
	upd := UpdateInput{TenantID: 7, ID: 3, Name: "p", Protocol: "http", Host: "h", Port: 70000}
	if _, err := New(&mockProxyQuerier{}, keys).Update(ctx, upd); err != ErrInvalidInput {
		t.Fatalf("update port 70000 must be ErrInvalidInput, got %v", err)
	}

	// The exact upper boundary 65535 stays valid (discriminating: proves the
	// guard is `> 65535`, not an off-by-one `>= 65535`).
	ok := base
	ok.Port = 65535
	if _, err := New(&mockProxyQuerier{}, keys).Create(ctx, ok); err != nil {
		t.Fatalf("port 65535 must be accepted, got %v", err)
	}
}
