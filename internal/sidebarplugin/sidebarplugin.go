// Package sidebarplugin runs small scripts on a schedule and keeps the first
// line each one prints, for the sidebar's plugin section. Like SwiftBar, the
// interval is in the file name: tenki.1h.sh runs every hour, fx.10m.py every
// ten minutes (units s, m, h, d).
package sidebarplugin

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/kazuph/ikesu/internal/childenv"
)

const (
	// MinInterval keeps a mistyped name (tenki.1s.sh) from running constantly.
	MinInterval = 10 * time.Second
	// RunTimeout stops a script that hangs; its last line stays on screen.
	RunTimeout = 10 * time.Second
	// scanInterval is how often the folder is re-read for added or removed scripts.
	scanInterval = 10 * time.Second
	maxText      = 200
	maxLines     = 20
)

var namePattern = regexp.MustCompile(`^(.+)\.([0-9]+)([smhd])(\.[^.]+)?$`)

// ParseName returns the plugin's display name and interval from its file
// name, or false when the name carries no interval.
func ParseName(file string) (string, time.Duration, bool) {
	m := namePattern.FindStringSubmatch(file)
	if m == nil || strings.HasPrefix(file, ".") {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[3]]
	return m[1], max(time.Duration(n)*unit, MinInterval), true
}

// Item is one plugin's latest result.
type Item struct {
	File, Name string
	Lines      []string // lines before "---" of the last successful run; SGR colors kept
	Err        string   // why the last run failed, empty on success
	Ran        bool     // false until the first run finished
}

// Runner runs the scripts in Dir.
type Runner struct {
	Dir      string
	OnChange func()
	// Columns, when set, is passed to scripts as IKESU_SIDEBAR_COLUMNS so they can fit the sidebar.
	Columns func() int

	mu    sync.Mutex
	items map[string]*Item
	next  map[string]time.Time
	every map[string]time.Duration
}

// Dir is ~/.config/ikesu/plugins/sidebar next to the config file.
func Dir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "plugins", "sidebar")
}

// Items returns the current results sorted by file name.
func (r *Runner) Items() []Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Item, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// Run scans the folder and runs due scripts until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastScan := time.Time{}
	for {
		now := time.Now()
		if now.Sub(lastScan) >= scanInterval {
			r.scan(now)
			lastScan = now
		}
		for _, file := range r.due(now) {
			r.runOne(ctx, file)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (r *Runner) scan(now time.Time) {
	entries, _ := os.ReadDir(r.Dir)
	seen := map[string]bool{}
	r.mu.Lock()
	if r.items == nil {
		r.items, r.next, r.every = map[string]*Item{}, map[string]time.Time{}, map[string]time.Duration{}
	}
	changed := false
	for _, e := range entries {
		name, every, ok := ParseName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode()&0o111 == 0 {
			continue
		}
		seen[e.Name()] = true
		if _, ok := r.items[e.Name()]; !ok {
			r.items[e.Name()] = &Item{File: e.Name(), Name: name}
			r.next[e.Name()] = now
			changed = true
		}
		r.every[e.Name()] = every
	}
	for file := range r.items {
		if !seen[file] {
			delete(r.items, file)
			delete(r.next, file)
			delete(r.every, file)
			changed = true
		}
	}
	r.mu.Unlock()
	if changed && r.OnChange != nil {
		r.OnChange()
	}
}

func (r *Runner) due(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for file, at := range r.next {
		if !now.Before(at) {
			out = append(out, file)
			r.next[file] = now.Add(r.every[file])
		}
	}
	sort.Strings(out)
	return out
}

func (r *Runner) runOne(ctx context.Context, file string) {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	cmd := childenv.CommandContextGroup(ctx, filepath.Join(r.Dir, file))
	cmd.Dir = r.Dir
	cmd.WaitDelay = time.Second
	if r.Columns != nil {
		if n := r.Columns(); n > 0 {
			cmd.Env = append(cmd.Env, "IKESU_SIDEBAR_COLUMNS="+strconv.Itoa(n))
		}
	}
	out, err := cmd.Output()
	lines, errText := Lines(out), ""
	if err != nil {
		errText = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errText = "時間切れ（" + RunTimeout.String() + "）"
		}
	}
	r.mu.Lock()
	it := r.items[file]
	changed := false
	if it != nil {
		prevLines, prevErr, prevRan := it.Lines, it.Err, it.Ran
		if errText == "" {
			it.Lines = lines
		}
		it.Err, it.Ran = errText, true
		changed = !slices.Equal(prevLines, it.Lines) || prevErr != it.Err || prevRan != it.Ran
	}
	r.mu.Unlock()
	if changed && r.OnChange != nil {
		r.OnChange()
	}
}

// Lines are the output lines before "---" (SwiftBar treats the rest as a
// menu), without SwiftBar's " | key=value" parameters. SGR color sequences
// are kept; other escape sequences and control characters are removed.
// Leading and trailing empty lines are dropped.
func Lines(out []byte) []string {
	s := bufio.NewScanner(bytes.NewReader(out))
	s.Buffer(make([]byte, 0, 4096), 1<<20)
	var lines []string
	for s.Scan() {
		raw := strings.TrimRight(s.Text(), " \t\r")
		if strings.TrimSpace(ansi.Strip(raw)) == "---" {
			break
		}
		line := keepSGR(raw)
		if i := strings.Index(line, " | "); i >= 0 {
			line = strings.TrimRight(line[:i], " ")
		}
		if ansi.StringWidth(line) > maxText {
			line = ansi.Truncate(line, maxText, "")
		}
		if len(lines) == 0 && strings.TrimSpace(ansi.Strip(line)) == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) == maxLines {
			break
		}
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var sgr = regexp.MustCompile(`^\x1b\[[0-9;]*m`)

// keepSGR removes control characters and escape sequences except SGR (colors).
func keepSGR(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		c := line[i]
		if c == 0x1b {
			if m := sgr.FindString(line[i:]); m != "" {
				b.WriteString(m)
				i += len(m)
				continue
			}
			// skip any other escape: ESC [ ... final byte, or ESC + one byte
			j := i + 1
			if j < len(line) && line[j] == ']' {
				// OSC: up to BEL or ESC \
				for j < len(line) && line[j] != 0x07 && !(line[j] == 0x1b && j+1 < len(line) && line[j+1] == '\\') {
					j++
				}
				if j < len(line) && line[j] == 0x1b {
					j++
				}
				i = j + 1
				continue
			}
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
					j++
				}
			}
			i = j + 1
			continue
		}
		if c < 0x20 || c == 0x7f {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
