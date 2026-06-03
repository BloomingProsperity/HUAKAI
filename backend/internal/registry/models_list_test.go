package registry

import (
	"strings"
	"testing"
)

func TestListModelsQueryProjectsCanonicalIDAndContextWindow(t *testing.T) {
	if got := strings.Count(listModelsQuery, "m.default_context_window AS context_window"); got != 2 {
		t.Fatalf("listModelsQuery context_window projections=%d want both tenant and global UNION arms", got)
	}
	if got := strings.Count(listModelsQuery, "m.canonical_id AS canonical_id"); got != 2 {
		t.Fatalf("listModelsQuery canonical_id projections=%d want both tenant and global UNION arms", got)
	}
	if want := "owned_by,\n    context_window,\n    canonical_id"; !strings.Contains(listModelsQuery, want) {
		t.Fatalf("ListModels final projection missing %q", want)
	}
}
