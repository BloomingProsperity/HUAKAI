package registry

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeBindingBodyParamGate(t *testing.T) {
	strips, err := decodeBindingBodyParamStrips(`["service_tier","stream_options.include_obfuscation"]`)
	if err != nil {
		t.Fatalf("decode strips: %v", err)
	}
	wantStrips := []string{"service_tier", "stream_options.include_obfuscation"}
	if !reflect.DeepEqual(strips, wantStrips) {
		t.Fatalf("strips=%v want %v", strips, wantStrips)
	}

	override, err := decodeBindingParamOverride(`{"temperature":0}`)
	if err != nil {
		t.Fatalf("decode override: %v", err)
	}
	if string(override["temperature"]) != "0" {
		asJSON, _ := json.Marshal(override)
		t.Fatalf("override=%s want temperature=0", asJSON)
	}

	emptyStrips, err := decodeBindingBodyParamStrips(`[]`)
	if err != nil {
		t.Fatalf("decode empty strips: %v", err)
	}
	if emptyStrips != nil {
		t.Fatalf("empty strips=%v want nil", emptyStrips)
	}
	emptyOverride, err := decodeBindingParamOverride(`{}`)
	if err != nil {
		t.Fatalf("decode empty override: %v", err)
	}
	if emptyOverride != nil {
		t.Fatalf("empty override=%v want nil", emptyOverride)
	}
}

func TestBindingForAttemptRequiresUnambiguousIdentity(t *testing.T) {
	resolved := Resolved{BindingMetadata: []BindingMetadata{
		{BindingID: 11, PoolGroupID: 101, StatusCodeMapping: map[int]int{403: 429}},
		{BindingID: 12, PoolGroupID: 101, StatusCodeMapping: map[int]int{403: 503}},
		{BindingID: 13, PoolGroupID: 202, StatusCodeMapping: map[int]int{403: 422}},
	}}

	if got, ok := resolved.BindingForAttempt(12, 101); !ok || got.BindingID != 12 {
		t.Fatalf("binding_id 精确命中=%+v/%t want 12/true", got, ok)
	}
	if _, ok := resolved.BindingForAttempt(99, 101); ok {
		t.Fatal("显式 binding_id 未命中时不得回退到同池或唯一候选")
	}
	if _, ok := resolved.BindingForAttempt(0, 101); ok {
		t.Fatal("同池存在多个 binding 时不得随便取第一条")
	}
	if got, ok := resolved.BindingForAttempt(0, 202); !ok || got.BindingID != 13 {
		t.Fatalf("唯一池命中=%+v/%t want 13/true", got, ok)
	}

	legacy := Resolved{BindingMetadata: []BindingMetadata{{BindingID: 21, PoolGroupID: 301}}}
	if _, ok := legacy.BindingForAttempt(0, 999); ok {
		t.Fatal("显式 pool_group_id 未命中时不得套用唯一但错误的 binding")
	}
	if got, ok := legacy.BindingForAttempt(0, 0); !ok || got.BindingID != 21 {
		t.Fatalf("完全无身份的旧计划可在唯一候选回退=%+v/%t want 21/true", got, ok)
	}
}
