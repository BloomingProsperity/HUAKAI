package proxyadmin

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
)

func TestCreateEncryptsAuthSecretBeforeDBWrite(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	q := &mockProxyQuerier{}
	plaintext := "proxy-secret:with@reserved?chars"

	_, err := New(q, keys).Create(ctx, CreateInput{
		TenantID:     7,
		Name:         "residential-a",
		Protocol:     "http",
		Host:         "proxy.example.com",
		Port:         3128,
		AuthUsername: strPtr("proxy-user"),
		AuthSecret:   &plaintext,
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if q.createCalls != 1 {
		t.Fatalf("CreateProxy calls=%d want 1", q.createCalls)
	}
	if q.createArg.AuthSecret == nil {
		t.Fatal("CreateProxy AuthSecret nil; want encrypted envelope")
	}
	stored := *q.createArg.AuthSecret
	if stored == plaintext {
		t.Fatal("stored auth_secret equals plaintext; write path did not encrypt")
	}
	if !strings.HasPrefix(stored, proxysecret.EnvelopePrefix) {
		t.Fatalf("stored auth_secret prefix=%q want %q", stored[:min(len(stored), len(proxysecret.EnvelopePrefix))], proxysecret.EnvelopePrefix)
	}
	roundTrip, err := proxysecret.Decode(ctx, keys, 7, stored)
	if err != nil {
		t.Fatalf("Decode stored auth_secret: %v", err)
	}
	if roundTrip != plaintext {
		t.Fatalf("decoded auth_secret=%q want original", roundTrip)
	}
}

func TestUpdateEncryptsAuthSecretBeforeDBWrite(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	q := &mockProxyQuerier{}
	plaintext := "updated-proxy-secret"

	_, err := New(q, keys).Update(ctx, UpdateInput{
		TenantID:     9,
		ID:           55,
		Name:         "residential-b",
		Protocol:     "socks5",
		Host:         "proxy2.example.com",
		Port:         1080,
		AuthUsername: strPtr("proxy-user"),
		AuthSecret:   &plaintext,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if q.updateCalls != 1 {
		t.Fatalf("UpdateProxy calls=%d want 1", q.updateCalls)
	}
	if q.updateArg.AuthSecret == nil {
		t.Fatal("UpdateProxy AuthSecret nil; want encrypted envelope")
	}
	stored := *q.updateArg.AuthSecret
	if stored == plaintext {
		t.Fatal("stored auth_secret equals plaintext; update path did not encrypt")
	}
	roundTrip, err := proxysecret.Decode(ctx, keys, 9, stored)
	if err != nil {
		t.Fatalf("Decode stored auth_secret: %v", err)
	}
	if roundTrip != plaintext {
		t.Fatalf("decoded auth_secret=%q want original", roundTrip)
	}
}

type mockProxyQuerier struct {
	createCalls int
	createArg   admindb.CreateProxyParams
	updateCalls int
	updateArg   admindb.UpdateProxyParams
}

func (m *mockProxyQuerier) CreateProxy(_ context.Context, arg admindb.CreateProxyParams) (admindb.CreateProxyRow, error) {
	m.createCalls++
	m.createArg = arg
	return admindb.CreateProxyRow{
		ID: arg.TenantID + 100, TenantID: arg.TenantID, Name: arg.Name, Protocol: arg.Protocol,
		Host: arg.Host, Port: arg.Port, AuthUsername: arg.AuthUsername, AuthSecret: arg.AuthSecret,
		Status: arg.Status,
	}, nil
}

func (m *mockProxyQuerier) UpdateProxy(_ context.Context, arg admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error) {
	m.updateCalls++
	m.updateArg = arg
	return admindb.UpdateProxyRow{
		ID: arg.ID, TenantID: arg.TenantID, Name: arg.Name, Protocol: arg.Protocol,
		Host: arg.Host, Port: arg.Port, AuthUsername: arg.AuthUsername, AuthSecret: arg.AuthSecret,
		Status: "active",
	}, nil
}

func testKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("proxy-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}

func strPtr(v string) *string { return &v }
