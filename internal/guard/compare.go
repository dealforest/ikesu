package guard

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type CompareOptions struct {
	GuardLog, AILog string
	Window          *int64
	Since           int64
}
type entry struct {
	epoch               int64
	line, verdict, rule string
}

func readEntries(path string, ai bool) ([]entry, int, error) {
	var files []*os.File
	if ai {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		files = []*os.File{f}
	} else {
		var err error
		files, err = openDecisionLogFiles(path)
		if err != nil {
			return nil, 0, err
		}
	}
	defer closeLogFiles(files)
	var rows []entry
	bad := 0
	_, err := walkLogLines(files, 0, func(raw []byte) bool {
		line := string(raw)
		if strings.TrimSpace(line) == "" {
			return false
		}
		var row entry
		var e error
		if ai {
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				bad++
				return false
			}
			var ts time.Time
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05-0700"} {
				ts, e = time.ParseInLocation(layout, parts[0], time.Local)
				if e == nil {
					break
				}
			}
			if e != nil || parts[2] == "" {
				bad++
				return false
			}
			row = entry{epoch: ts.Unix(), line: parts[2], verdict: strings.ToUpper(parts[1])}
		} else {
			var r Record
			if e = json.Unmarshal(raw, &r); e != nil || r.CommandLine == "" || r.Epoch == 0 || !has([]string{"allow", "ask", "block", "deny"}, r.Decision) {
				bad++
				return false
			}
			row = entry{r.Epoch, r.CommandLine, r.Decision, r.Rule}
		}
		rows = append(rows, row)
		return false
	})
	return rows, bad, err
}
func aiVerdict(s string) string {
	switch s {
	case "ALLOW":
		return "allow"
	case "REJECT", "DAY_ALLOW", "PROMPT_SHOWN":
		return "ask"
	}
	if strings.HasPrefix(s, "BLOCK") {
		return "block"
	}
	return strings.ToLower(s)
}

// Compare pairs each record once with the nearest equal command line.
// A nil window has no time limit, as observed in herdr 0.2.30.
func Compare(o CompareOptions, w io.Writer) (int, error) {
	g, bg, e := readEntries(o.GuardLog, false)
	if e != nil {
		return 1, e
	}
	a, ba, e := readEntries(o.AILog, true)
	if e != nil {
		return 1, e
	}
	used := make([]bool, len(a))
	counts := map[string]int{}
	for _, r := range g {
		if r.epoch < o.Since {
			continue
		}
		best := -1
		var diff int64
		for n, s := range a {
			if used[n] || s.epoch < o.Since || s.line != r.line {
				continue
			}
			d := s.epoch - r.epoch
			if d < 0 {
				d = -d
			}
			if best < 0 || d < diff {
				best = n
				diff = d
			}
		}
		if best < 0 {
			counts["ikesu-only"]++
			fmt.Fprintf(w, "  IKESU-ONLY  %s  (ikesu=%s:%s)\n", r.line, r.verdict, r.rule)
			continue
		}
		used[best] = true
		s := a[best]
		if o.Window != nil && diff > *o.Window {
			counts["out-of-window"]++
			fmt.Fprintf(w, "  OUT-OF-WINDOW  %s\n", r.line)
			continue
		}
		status := "AGREE"
		key := "agree"
		if r.verdict != aiVerdict(s.verdict) {
			status = "DIVERGE"
			key = "diverge"
		}
		counts[key]++
		fmt.Fprintf(w, "  %-8s %s  (ikesu=%s ai_guard=%s)\n", status, r.line, r.verdict, s.verdict)
	}
	for n, s := range a {
		if !used[n] && s.epoch >= o.Since {
			counts["ai-guard-only"]++
			fmt.Fprintf(w, "  AI-GUARD-ONLY  %s  (ai_guard=%s)\n", s.line, s.verdict)
		}
	}
	fmt.Fprintf(w, "\nsummary: agree=%d diverge=%d ikesu-only=%d ai-guard-only=%d out-of-window=%d\n", counts["agree"], counts["diverge"], counts["ikesu-only"], counts["ai-guard-only"], counts["out-of-window"])
	if bg+ba > 0 {
		fmt.Fprintf(w, "malformed: ikesu=%d ai-guard=%d\n", bg, ba)
	}
	if counts["diverge"] > 0 {
		return 1, nil
	}
	return 0, nil
}
func ReadLog(path string, lines *int, w io.Writer, jsonMode bool) error {
	files, err := openDecisionLogFiles(path)
	if err != nil {
		return err
	}
	defer closeLogFiles(files)
	var records []string
	start := 0
	_, err = walkLogLines(files, 0, func(raw []byte) bool {
		line := string(raw)
		if lines != nil && *lines == 0 {
			return false
		}
		if lines == nil || len(records) < *lines {
			records = append(records, line)
			return false
		}
		records[start] = line
		start = (start + 1) % len(records)
		return false
	})
	if err != nil {
		return err
	}
	for offset := range records {
		raw := records[(start+offset)%len(records)]
		if jsonMode {
			fmt.Fprintln(w, raw)
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			fmt.Fprintln(w, "malformed:", raw)
			continue
		}
		fmt.Fprintf(w, "%d\t%-5s\t%s\t%s", r.Epoch, r.Decision, r.Rule, r.CommandLine)
		if r.Pane != "" {
			fmt.Fprintf(w, " pane=%s", r.Pane)
		}
		if r.Agent != "" {
			fmt.Fprintf(w, " agent=%s", r.Agent)
		}
		if r.Via != "" {
			fmt.Fprintf(w, " via=%s", r.Via)
		}
		fmt.Fprintln(w)
	}
	return nil
}
func Status(c Config, root string, i Input) (any, error) {
	path := root + "/guard/decisions.jsonl"
	rows, _, e := readEntries(path, false)
	if e != nil {
		return nil, e
	}
	m, active, e := ReadMarker(i)
	if e != nil {
		return nil, e
	}
	var marker any
	if active {
		marker = map[string]string{"agent": m.Agent, "cwd": m.Cwd, "path": i.Env["IKESU_GUARD_MARKER"]}
	}
	return map[string]any{"ask_timeout_seconds": int64(c.Timeout() / time.Second), "enabled": c.Active(), "log_path": path, "log_records": len(rows), "marker": marker, "mode": c.EffectiveMode(), "shim_dir": root + "/guard/bin"}, nil
}
