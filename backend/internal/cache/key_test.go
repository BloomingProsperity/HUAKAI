package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

func TestCanonicalRequestBody_StripsTopLevelVolatileFieldsAndSortsObjects(t *testing.T) {
	a := []byte(`{"stream":false,"metadata":{"trace":"x"},"messages":[{"content":"hi","role":"user"}],"model":"gpt","timestamp":"now"}`)
	b := []byte(`{
		"timestamp": "later",
		"model": "gpt",
		"messages": [{"role":"user","content":"hi"}],
		"metadata": {"trace":"y"},
		"stream": true
	}`)
	ca, err := CanonicalRequestBody(a)
	if err != nil {
		t.Fatalf("canonical a: %v", err)
	}
	cb, err := CanonicalRequestBody(b)
	if err != nil {
		t.Fatalf("canonical b: %v", err)
	}
	if !bytes.Equal(ca, cb) {
		t.Fatalf("canonical bodies differ:\na=%s\nb=%s", ca, cb)
	}
	want := `{"messages":[{"content":"hi","role":"user"}],"model":"gpt"}`
	if string(ca) != want {
		t.Fatalf("canonical=%s want %s", ca, want)
	}
}

func TestBuildKeyIncludesTenantVendorAndModel(t *testing.T) {
	body := []byte(`{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`)
	base, _, err := BuildKey(KeyInput{TenantID: 7, Vendor: "openai", Model: "gpt-4o", Body: body})
	if err != nil {
		t.Fatalf("base key: %v", err)
	}
	cases := []KeyInput{
		{TenantID: 8, Vendor: "openai", Model: "gpt-4o", Body: body},
		{TenantID: 7, Vendor: "anthropic", Model: "gpt-4o", Body: body},
		{TenantID: 7, Vendor: "openai", Model: "gpt-4o-mini", Body: body},
	}
	for _, tc := range cases {
		got, _, err := BuildKey(tc)
		if err != nil {
			t.Fatalf("variant key: %v", err)
		}
		if got == base {
			t.Fatalf("key did not change for %+v", tc)
		}
	}
}

func TestBuildKeyIncludesEndpointFamilyAndV2Version(t *testing.T) {
	body := []byte(`{"model":"gpt","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	chat, _, err := BuildKey(KeyInput{
		TenantID:       7,
		Vendor:         "openai",
		Model:          "gpt-4o",
		EndpointFamily: "chat",
		Body:           body,
	})
	if err != nil {
		t.Fatalf("chat key: %v", err)
	}
	messages, _, err := BuildKey(KeyInput{
		TenantID:       7,
		Vendor:         "openai",
		Model:          "gpt-4o",
		EndpointFamily: "messages",
		Body:           body,
	})
	if err != nil {
		t.Fatalf("messages key: %v", err)
	}
	if chat == messages {
		t.Fatalf("GW-01: identical tenant/vendor/model/body across endpoint families reused L2 key %q", chat)
	}
	if !strings.HasPrefix(chat, "l2:v2:") || !strings.HasPrefix(messages, "l2:v2:") {
		t.Fatalf("keys must carry l2:v2 version: chat=%q messages=%q", chat, messages)
	}
	chatAgain, _, err := BuildKey(KeyInput{
		TenantID:       7,
		Vendor:         "openai",
		Model:          "gpt-4o",
		EndpointFamily: "chat",
		Body:           body,
	})
	if err != nil {
		t.Fatalf("chat repeat key: %v", err)
	}
	if chatAgain != chat {
		t.Fatalf("same endpoint family key changed: first=%q second=%q", chat, chatAgain)
	}
}

func TestBuildKeyV2CannotCollideWithLegacyV1Key(t *testing.T) {
	body := []byte(`{"model":"gpt","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	input := KeyInput{
		TenantID:       7,
		Vendor:         "openai",
		Model:          "gpt-4o",
		EndpointFamily: "chat",
		Body:           body,
	}
	current, _, err := BuildKey(input)
	if err != nil {
		t.Fatalf("current key: %v", err)
	}
	legacy := legacyV1Key(t, input)
	if legacy == current {
		t.Fatalf("GW-01: v1 key collided with v2 key: %q", current)
	}
	if !strings.HasPrefix(legacy, "l2:v1:") || !strings.HasPrefix(current, "l2:v2:") {
		t.Fatalf("unexpected key versions: legacy=%q current=%q", legacy, current)
	}
}

func legacyV1Key(t *testing.T, in KeyInput) string {
	t.Helper()
	canonical, err := CanonicalRequestBody(in.Body)
	if err != nil {
		t.Fatalf("legacy canonical body: %v", err)
	}
	preimage := bytes.NewBuffer(nil)
	preimage.WriteString("l2:v1")
	preimage.WriteByte(0)
	preimage.WriteString(strconv.FormatInt(in.TenantID, 10))
	preimage.WriteByte(0)
	preimage.WriteString(in.Vendor)
	preimage.WriteByte(0)
	preimage.WriteString(in.Model)
	preimage.WriteByte(0)
	preimage.Write(canonical)
	sum := sha256.Sum256(preimage.Bytes())
	return "l2:v1:" + hex.EncodeToString(sum[:])
}
