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
