package router

import "testing"

// A LoadCap set on PASRSelectorConfig must propagate to the selector's loadCap.
// Discriminating vs TestPASR_DefaultLoadCap (0 -> 0.95): 0.5 != 0.95, so if the
// constructor stops honoring the field (or the wiring drops it), the selector
// falls back to 0.95 and this assertion fails.
func TestPASR_LoadCapPropagation(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	sel, err := NewPASRSelector(PASRSelectorConfig{
		Accounts:     &fakeAccountSource{},
		Segments:     tbl,
		RingProvider: func() *AccountRing { return NewAccountRing(nil, 1) },
		LoadCap:      0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sel.loadCap != 0.5 {
		t.Errorf("LoadCap=0.5 must propagate to selector, got %v", sel.loadCap)
	}
}
