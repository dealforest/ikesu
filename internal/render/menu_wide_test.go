package render

import "testing"

func TestMenuSizeCountsWideCharactersAsTwoCells(t *testing.T) {
	m := &Menu{Title: "Reopen closed space", Items: []MenuItem{{Label: "大事なspace · 2 panes · claude · just now"}}}
	w, _ := MenuSize(m)
	if want := 6 + 35 + 4; w != want { // 大事な takes 6 cells, the rest 35, plus padding and border
		t.Fatalf("menu width %d, want %d", w, want)
	}
}
