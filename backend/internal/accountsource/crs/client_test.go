package crs

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type resolverStub struct {
	addresses []net.IPAddr
}

func (r resolverStub) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

type doerStub struct {
	requests []*http.Request
}

func (d *doerStub) Do(request *http.Request) (*http.Response, error) {
	d.requests = append(d.requests, request)
	if len(d.requests) == 1 {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"access_token":"login-secret"}}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"accounts":[{"name":"alpha","provider_code":"openai","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"upstream-secret"},"external_account_id":"acct-1"}]}`))}, nil
}

func TestFetchUsesAllowlistAndReturnsCanonicalCandidate(t *testing.T) {
	doer := &doerStub{}
	client := &Client{http: doer, resolver: resolverStub{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}, allowed: normalizedHosts([]string{"relay.example.com"})}
	items, contextMap, err := client.Fetch(context.Background(), FetchInput{BaseURL: "https://relay.example.com", Username: "admin", Password: "password"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 || items[0].Candidate.Vendor != "openai" || items[0].Candidate.AuthMode != "api_key" {
		t.Fatalf("items=%+v", items)
	}
	if got := doer.requests[1].Header.Get("Authorization"); got != "Bearer login-secret" {
		t.Fatalf("authorization=%q", got)
	}
	if contextMap["source_host"] != "relay.example.com" {
		t.Fatalf("context=%v", contextMap)
	}
}

func TestFetchRejectsPrivateResolutionBeforeHTTP(t *testing.T) {
	doer := &doerStub{}
	client := &Client{http: doer, resolver: resolverStub{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}, allowed: normalizedHosts([]string{"relay.example.com"})}
	_, _, err := client.Fetch(context.Background(), FetchInput{BaseURL: "https://relay.example.com", Username: "admin", Password: "password"})
	if err != ErrEndpointBlocked {
		t.Fatalf("err=%v want endpoint blocked", err)
	}
	if len(doer.requests) != 0 {
		t.Fatal("解析时 SSRF 拒绝后不得发 HTTP 请求")
	}
}

func TestFetchRejectsHostOutsideDeploymentAllowlist(t *testing.T) {
	client := &Client{http: &doerStub{}, resolver: resolverStub{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}, allowed: normalizedHosts([]string{"approved.example.com"})}
	_, _, err := client.Fetch(context.Background(), FetchInput{BaseURL: "https://relay.example.com", Username: "admin", Password: "password"})
	if err != ErrDisabled {
		t.Fatalf("err=%v want disabled", err)
	}
}
