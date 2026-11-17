//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sysctl で読んだ開始時刻が、ps の lstart（秒単位）と同じになる。
func TestProcStartMatchesPS(t *testing.T) {
	pid := os.Getpid()
	got, ok := ProcStart(pid)
	if !ok {
		t.Fatal("ProcStart failed for this process")
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("ProcStart = %v, ps = %v", got, want)
	}
	if _, ok := ProcStart(1 << 30); ok {
		t.Fatal("missing pid reported a start time")
	}
}
