package tui

import (
	"bytes"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/runtime"
)

// スマホのアプリの画面（native_menus）は、横長でも右のエージェントの一覧を出さない。PC の画面は今までどおり出す。
func TestPhoneClientStartsWithoutRightSidebar(t *testing.T) {
	for _, native := range []bool{false, true} {
		a := NewApp(model.New(t.TempDir(), 160, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
		var c config.Config
		c.UI.NativeMenus = native
		a.setSidebarConfig(c, true)
		if got := a.st.RightSidebarArea().W; (got == 0) != native {
			t.Fatalf("native=%v right sidebar width=%d", native, got)
		}
	}
}
