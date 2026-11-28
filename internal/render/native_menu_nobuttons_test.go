package render

import (
	"strings"
	"testing"
)

// A progress box reaches the phone as no_buttons; other dialogs keep the
// JSON they had.
func TestNativeMenuJSONCarriesNoButtons(t *testing.T) {
	got := NativeMenuJSON(nil, &Dialog{Title: "delete idle panes", Detail: "ペインを閉じています…", NoButtons: true})
	if !strings.Contains(got, `"no_buttons":true`) {
		t.Fatal(got)
	}
	if got := NativeMenuJSON(nil, &Dialog{Title: "t", Confirm: "ok", Cancel: "cancel"}); strings.Contains(got, "no_buttons") {
		t.Fatal(got)
	}
}
