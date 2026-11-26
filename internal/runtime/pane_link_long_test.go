package runtime

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// A program that prints one huge soft-wrapped line must not make every paint
// read the whole line under the pane lock.
func TestPaintLinkScanStaysNearTheViewportOnAHugeLogicalLine(t *testing.T) {
	const width, height, rows = 80, 24, 990
	s := NewSupervisor()
	defer s.KillAll()
	url := "https://long.test/" + strings.Repeat("u", 3*width)
	p, err := s.SpawnWith(1, t.TempDir(), width, height, nil, []string{"/bin/sh", "-c",
		`printf '%s' "$1"; head -c "$2" /dev/zero | tr '\0' x; printf '\r\nREADY'; exec /bin/cat`,
		"test", url, strconv.Itoa(rows * width),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitReadMarker(t, p, "READY")
	history := p.Snapshot().ScrollbackLen
	if history < rows-height-10 {
		t.Fatalf("fixture scrollback = %d", history)
	}
	read := func(*vt.Emulator, int) {}
	start := time.Now()
	for i := 0; i < 20; i++ {
		p.WithViewport(0, height, width, false, true, read)
	}
	elapsed := time.Since(start)
	t.Logf("20 paints: %v", elapsed)
	if elapsed > 25*time.Millisecond {
		t.Fatalf("20 paints with menu links took %v on a %d-row logical line", elapsed, rows)
	}
	// The URL at the start of the line is still found where it is visible.
	links := p.WithViewport(history, height, width, false, true, read)
	if len(links) == 0 || !strings.HasPrefix(links[0].URL, url) {
		t.Fatalf("links at the start of the line = %+v", links)
	}
}

func TestLinkScanBoundsCoverALongURLAndNoMore(t *testing.T) {
	for _, cols := range []int{1, 40, 80, 200} {
		lo, hi := linkScanBounds(100, 124, cols)
		extra := 100 - lo
		if hi-124 != extra {
			t.Fatalf("cols %d: asymmetric bounds %d %d", cols, lo, hi)
		}
		if extra*cols < maxLinkBytes {
			t.Fatalf("cols %d: %d rows cannot hold a %d-byte URL", cols, extra, maxLinkBytes)
		}
		if (extra-2)*cols >= maxLinkBytes {
			t.Fatalf("cols %d: %d rows read more than needed", cols, extra)
		}
	}
}
