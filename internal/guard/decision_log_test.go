package guard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDecisionLogRotation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "guard", "decisions.jsonl")
	before := time.Now().Unix()
	write := func(command string, limit int64) {
		t.Helper()
		if err := logWithLimit(root, Input{RawLine: command}, Marker{}, result("allow", "test", undeterminedPrefix+"test"), "line", limit); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	write("curl old-one", 0)
	limit := int64(len(read(path)))
	previousArchive := []byte("obsolete data\n")
	if err := os.WriteFile(path+".1", previousArchive, 0600); err != nil {
		t.Fatal(err)
	}
	write("curl old-two", limit)
	if !bytes.Equal(read(path+".1"), previousArchive) {
		t.Fatal("rotated when size equaled limit")
	}
	beforeRotation := read(path)
	write("curl current", limit)
	data := read(path)
	if !bytes.Equal(read(path+".1"), beforeRotation) || bytes.Count(data, []byte{'\n'}) != 1 || !bytes.Contains(data, []byte("curl current")) {
		t.Fatal("rotation did not replace archive and write the new record to current")
	}

	var output bytes.Buffer
	lines := 2
	if err := ReadLog(path, &lines, &output, true); err != nil {
		t.Fatal(err)
	}
	var shown []string
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		shown = append(shown, r.CommandLine)
	}
	if !slices.Equal(shown, []string{"curl old-two", "curl current"}) {
		t.Fatalf("recent log lines: %v", shown)
	}

	rng := rand.New(rand.NewSource(1))
	samples, readBytes, err := SampleMissed(path, before, 30, rng)
	var sampled []string
	for _, sample := range samples {
		sampled = append(sampled, sample.CmdLine)
	}
	slices.Sort(sampled)
	if err != nil || !slices.Equal(sampled, []string{"curl current", "curl old-one", "curl old-two"}) || readBytes != int64(len(beforeRotation)+len(data)) {
		t.Fatalf("audit across rotation: %v, read=%d, error=%v", sampled, readBytes, err)
	}
	samples, readBytes, err = SampleMissed(path, time.Now().Unix()+1, 30, rng)
	if err != nil || len(samples) != 0 || readBytes != int64(len(data)) {
		t.Fatalf("audit must stop before opening the archive's contents: %v, read=%d, error=%v", samples, readBytes, err)
	}
}

func TestDecisionLogConcurrentRotation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "guard", "decisions.jsonl")
	const limit = 16 << 10
	if err := logWithLimit(root, Input{RawLine: "before rotation"}, Marker{}, result("allow", "test", strings.Repeat("x", limit)), "line", limit); err != nil {
		t.Fatal(err)
	}
	const total = 24
	start := make(chan struct{})
	results := make(chan error, total)
	for n := range total {
		go func() {
			<-start
			line := fmt.Sprintf("writer %d", n)
			results <- logWithLimit(root, Input{RawLine: line}, Marker{}, result("allow", "test", ""), "line", limit)
		}()
	}
	close(start)
	for range total {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	records, bad, err := readEntries(path, false)
	if err != nil || bad != 0 || len(records) != total+1 || records[0].line != "before rotation" {
		t.Fatalf("read %d records after concurrent rotation (malformed=%d, err=%v)", len(records), bad, err)
	}
	seen := map[string]bool{}
	for _, r := range records {
		if seen[r.line] {
			t.Errorf("duplicate record %q", r.line)
		}
		seen[r.line] = true
	}
}
