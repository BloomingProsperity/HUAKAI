package cache

import (
	"bytes"
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
