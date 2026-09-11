package upstreamcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

type seedRates struct {
	Input         string `json:"input_micro_usd"`
	Output        string `json:"output_micro_usd"`
	CacheRead     string `json:"cache_read_micro_usd"`
	CacheCreation string `json:"cache_creation_micro_usd"`
	Cache1h       string `json:"cache_creation_1h_micro_usd"`
}

func TestOfficialFrontierPricingSeed_MatchesPublishedRates(t *testing.T) {
	t.Parallel()
	up := readMigration(t, "0235_official_frontier_pricing.up.sql")
	models := extractDollarJSON(t, up, "$models$")
	openai := extractDollarJSON(t, up, "$openai$")

	want := map[string]seedRates{
		"gpt-6-astra":      {Input: "10", Output: "50", CacheRead: "1", CacheCreation: "12.5"},
		"gpt-5.6":          {Input: "4", Output: "20", CacheRead: "0.4"},
		"gpt-5.6-sol":      {Input: "4", Output: "20", CacheRead: "0.4"},
		"gpt-5.6-terra":    {Input: "2", Output: "12", CacheRead: "0.2"},
		"gpt-5.6-luna":     {Input: "0.2", Output: "1.2", CacheRead: "0.02"},
		"claude-fable-5-1": {Input: "10", Output: "50", CacheRead: "0.25", CacheCreation: "12.5", Cache1h: "20"},
		"claude-opus-5":    {Input: "5", Output: "25", CacheRead: "0.5", CacheCreation: "6.25"},
		"gemini-3.6-flash": {Input: "0.75", Output: "3.75", CacheRead: "0.075"},
		"gemini-3.7-flash": {Input: "0.75", Output: "3.75", CacheRead: "0.075"},
		"gemini-3.8-flash": {Input: "0.75", Output: "3.75", CacheRead: "0.075"},
		"grok-4.5":         {Input: "2", Output: "6", CacheRead: "0.3"},
		"grok-4.6":         {Input: "2", Output: "6", CacheRead: "0.5"},
	}
	assertSeedMap(t, "models", models, want)
	assertSeedMap(t, "providers.openai.models", openai, map[string]seedRates{
		"gpt-6-astra":   want["gpt-6-astra"],
		"gpt-5.6":       want["gpt-5.6"],
		"gpt-5.6-sol":   want["gpt-5.6-sol"],
		"gpt-5.6-terra": want["gpt-5.6-terra"],
		"gpt-5.6-luna":  want["gpt-5.6-luna"],
	})

	down := readMigration(t, "0235_official_frontier_pricing.down.sql")
	if !strings.Contains(down, `"gpt-5.6-sol":{"input_micro_usd":"5"`) {
		t.Fatal("down 必须把 Sol 恢复到 0173 旧价,避免回滚后仍按促销价结算")
	}
	for _, key := range []string{"gpt-6-astra", "claude-fable-5-1", "claude-opus-5", "gemini-3.8-flash", "grok-4.6"} {
		if !strings.Contains(down, "- '"+key+"'") {
			t.Fatalf("down 必须删除新键 %s", key)
		}
	}
}

func assertSeedMap(t *testing.T, label string, raw map[string]seedRates, want map[string]seedRates) {
	t.Helper()
	for model, expect := range want {
		got, ok := raw[model]
		if !ok {
			t.Fatalf("%s 缺少 %s", label, model)
		}
		assertDec(t, label+" "+model+" input", got.Input, expect.Input)
		assertDec(t, label+" "+model+" output", got.Output, expect.Output)
		assertDec(t, label+" "+model+" cache_read", got.CacheRead, expect.CacheRead)
		if expect.CacheCreation != "" {
			assertDec(t, label+" "+model+" cache_creation", got.CacheCreation, expect.CacheCreation)
		}
		if expect.Cache1h != "" {
			assertDec(t, label+" "+model+" cache_1h", got.Cache1h, expect.Cache1h)
		}
	}
}

func assertDec(t *testing.T, name, got, want string) {
	t.Helper()
	if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(want)) {
		t.Fatalf("%s=%s want %s", name, got, want)
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func extractDollarJSON(t *testing.T, src, tag string) map[string]seedRates {
	t.Helper()
	start := strings.Index(src, tag)
	if start < 0 {
		t.Fatalf("missing %s", tag)
	}
	start += len(tag)
	end := strings.Index(src[start:], tag)
	if end < 0 {
		t.Fatalf("unclosed %s", tag)
	}
	var out map[string]seedRates
	if err := json.Unmarshal([]byte(src[start:start+end]), &out); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", tag, err, src[start:start+end])
	}
	return out
}
