package runtime

import (
	"strings"
	"testing"
)

func TestPaneWebLinkAtUsesScrollbackAndHardLineBoundaries(t *testing.T) {
	s := NewSupervisor()
	defer s.KillAll()
	const width, height = 30, 4
	head := "https://first.test/"
	continuation := strings.Repeat("abc", 12)
	second := "https://second.test/path"
	p, err := s.SpawnWith(1, t.TempDir(), width, height, nil, []string{"/bin/sh", "-c",
		`printf '%s\r\n%s\r\n%s\r\nfour\r\nfive\r\nsix\r\nREADY' "$1" "$2" "$3"; exec /bin/cat`,
		"test", head + continuation, second, "hard_break_text",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitReadMarker(t, p, "READY")

	off := p.Snapshot().ScrollbackLen
	if off == 0 {
		t.Fatal("fixture did not create scrollback")
	}
	if got := p.WebLinkAt(1, 1, off, height, false); got != head+continuation {
		t.Fatalf("scrolled continuation link = %q", got)
	}
	if got := p.WebLinkAt(1, 2, off, height, false); got != second {
		t.Fatalf("link followed by a hard line break = %q", got)
	}
	if got := p.WebLinkAt(1, 3, off, height, false); got != "" {
		t.Fatalf("hard line break continuation = %q", got)
	}
}
