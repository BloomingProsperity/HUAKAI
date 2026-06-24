package codebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHCSFCloneAvoidsJSONRoundTrip(t *testing.T) {
	path := filepath.Join("..", "gateway", "upstream_dispatcher_hcsf.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read HCSF dispatcher: %v", err)
	}
	src := string(raw)
	start := strings.Index(src, "func cloneHCSF(")
	if start < 0 {
		t.Fatal("cloneHCSF function missing")
	}
	end := strings.Index(src[start:], "func clearHCSFNonWireFields(")
	if end < 0 {
		t.Fatal("clearHCSFNonWireFields must remain adjacent to cloneHCSF")
	}
	body := src[start : start+end]
	for _, forbidden := range []string{"json.Marshal", "json.Unmarshal"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("cloneHCSF must not use JSON round-trip clone; found %s in:\n%s", forbidden, body)
		}
	}
	for _, required := range []string{
		"cloneReflectValue(reflect.ValueOf(*env))",
		"clearHCSFNonWireFields(&out)",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("cloneHCSF missing required clone step %q in:\n%s", required, body)
		}
	}
}
