package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeCachesMerge(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1794911400, 0)
	os.WriteFile(filepath.Join(cache, "statusline-rate-limits.json"), []byte(`{"rate_limits":{"seven_day":{"used_percentage":55,"resets_at":1795464000},"five_hour":{"utilization":0.25,"resets_at":"2026-11-17T12:00:00Z"}}}`), 0o600)
	os.Chtimes(filepath.Join(cache, "statusline-rate-limits.json"), now, now)
	os.WriteFile(filepath.Join(cache, "claude-model-limits.json"), []byte(`{"fetched_at":1794911393,"model_limits":[{"display_name":"Fable","used_percentage":9,"resets_at":1795463999}],"rate_limits":{"seven_day":{"used_percentage":40}}}`), 0o600)
	c := ReadClaude(home, now)
	if c == nil || c.SevenDay == nil || c.SevenDay.UsedPercent != 55 || c.FiveHour == nil || c.FiveHour.UsedPercent != 25 {
		t.Fatalf("claude = %+v", c)
	}
	if c.FiveHour.ResetTime().UTC().Format(time.RFC3339) != "2026-11-17T12:00:00Z" {
		t.Fatalf("five hour reset = %v", c.FiveHour.ResetTime())
	}
	rows := Rows(c, nil)
	if len(rows) != 4 || rows[2].Label != "FAB W" || rows[2].Bucket.RemainingPercent() != 91 || rows[3].Label != "CX W" || rows[3].Bucket != nil {
		t.Fatalf("rows = %+v", rows)
	}
	// 1日より古い get_usage の記録は使わない。
	if _, ok := ParseClaudeModelLimits([]byte(`{"fetched_at":1,"model_limits":[{"display_name":"X","used_percentage":1}]}`), now); ok {
		t.Fatal("stale model limits used")
	}
}

func TestCodexParsesAPIAndLog(t *testing.T) {
	api := []byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":1794920000},"secondary_window":{"used_percent":49,"limit_window_seconds":604800,"reset_at":1795400000}}}`)
	c, ok := ParseCodex(api, time.Unix(1794911400, 0), "live")
	if !ok || c.Weekly == nil || c.Weekly.UsedPercent != 49 || c.Weekly.Label != "7d" || c.ShortTerm == nil || c.ShortTerm.Label != "5h" {
		t.Fatalf("codex = %+v", c)
	}
	body := `noise {"type":"codex.rate_limits","rate_limits":{"secondary":{"used_percent":30,"window_minutes":10080,"reset_at":1}},"note":"a \"}\" b"} tail`
	got := extractRateLimitJSON(body)
	if _, ok := ParseCodex([]byte(got), time.Now(), "log"); !ok {
		t.Fatalf("log entry not parsed: %q", got)
	}
}

func TestPace(t *testing.T) {
	now := time.Unix(1000, 0)
	b := Bucket{UsedPercent: 70, WindowSeconds: 100, ResetAt: 1050}
	pace, ok := b.PaceRemainingPercent(now)
	if !ok || pace != 50 {
		t.Fatalf("pace = %v %v", pace, ok)
	}
	d, ok := Difference(b.RemainingPercent(), pace)
	if !ok || d.Kind != Overuse || d.Lower != 30 || d.Upper != 50 {
		t.Fatalf("difference = %+v", d)
	}
	if _, ok := Difference(40, 40); ok {
		t.Fatal("equal values reported a difference")
	}
}
