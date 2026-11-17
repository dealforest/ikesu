package usage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	fiveHourWindow = 5 * 60 * 60
	sevenDayWindow = 7 * 24 * 60 * 60
	// claudeMaxCacheAge is how old a Claude cache may be before it is ignored.
	claudeMaxCacheAge = 24 * time.Hour
)

// ModelLimit is a per-model weekly limit from Claude Code's get_usage cache.
type ModelLimit struct {
	DisplayName string
	Bucket      Bucket
	ObservedAt  time.Time
}

// Claude is Claude Code's rate-limit state.
type Claude struct {
	FiveHour, SevenDay                 *Bucket
	ModelLimits                        []ModelLimit
	FiveHourObservedAt, SevenDayObservedAt time.Time
	ObservedAt                         time.Time
}

// ParseClaudeRateLimits reads the statusline JSON ({"rate_limits": {...}}) or
// a bare rate_limits object.
func ParseClaudeRateLimits(data []byte, observedAt time.Time) (*Claude, bool) {
	m, ok := decodeObject(data)
	if !ok {
		return nil, false
	}
	return claudeFromObject(m, observedAt)
}

func claudeFromObject(m map[string]any, observedAt time.Time) (*Claude, bool) {
	limits, ok := m["rate_limits"].(map[string]any)
	if !ok {
		limits = m
	}
	five := claudeBucket(firstValue(limits, "five_hour", "fiveHour", "5h", "short_term", "shortTerm"), "5h", fiveHourWindow)
	seven := claudeBucket(firstValue(limits, "seven_day", "sevenDay", "weekly", "7d", "week"), "W", sevenDayWindow)
	if five == nil && seven == nil {
		return nil, false
	}
	c := &Claude{FiveHour: five, SevenDay: seven, ObservedAt: observedAt}
	if five != nil {
		c.FiveHourObservedAt = observedAt
	}
	if seven != nil {
		c.SevenDayObservedAt = observedAt
	}
	return c, true
}

func claudeBucket(v any, label string, window float64) *Bucket {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	used, ok := claudeUsed(m)
	if !ok {
		return nil
	}
	return &Bucket{Label: label, UsedPercent: clamp(used), WindowSeconds: window, ResetAt: resetAt(firstValue(m, "resets_at", "reset_at"))}
}

func claudeUsed(m map[string]any) (float64, bool) {
	for _, k := range []string{"used_percentage", "used_percent", "percent", "percentage"} {
		if v, ok := number(m[k]); ok {
			return v, true
		}
	}
	if v, ok := number(m["utilization"]); ok {
		// Some payloads report a 0...1 fraction, others a 0...100 percentage.
		if v <= 1 {
			return v * 100, true
		}
		return v, true
	}
	return 0, false
}

func resetAt(v any) float64 {
	if f, ok := number(v); ok {
		return f
	}
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return float64(t.Unix())
			}
		}
	}
	return 0
}

// ParseClaudeModelLimits reads the get_usage cache written by
// claude-model-limits-fetch.mjs. It is ignored when older than a day.
func ParseClaudeModelLimits(data []byte, now time.Time) (*Claude, bool) {
	m, ok := decodeObject(data)
	if !ok {
		return nil, false
	}
	fetched, ok := number(m["fetched_at"])
	if !ok {
		return nil, false
	}
	observed := time.Unix(int64(fetched), 0)
	if age := now.Sub(observed); age < 0 || age > claudeMaxCacheAge {
		return nil, false
	}
	base, _ := claudeFromObject(m, observed)
	var models []ModelLimit
	items, _ := m["model_limits"].([]any)
	for _, item := range items {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := im["display_name"].(string)
		used, ok := number(im["used_percentage"])
		if name == "" || !ok {
			continue
		}
		reset, _ := number(im["resets_at"])
		models = append(models, ModelLimit{DisplayName: name, Bucket: Bucket{Label: "W", UsedPercent: clamp(used), WindowSeconds: sevenDayWindow, ResetAt: reset}, ObservedAt: observed})
	}
	if base == nil && len(models) == 0 {
		return nil, false
	}
	if base == nil {
		base = &Claude{ObservedAt: observed}
	}
	base.ModelLimits = models
	return base, true
}

// MergeClaude prefers the statusline values and takes the model limits from
// the get_usage cache.
func MergeClaude(statusline, models *Claude) *Claude {
	if statusline == nil && models == nil {
		return nil
	}
	out := &Claude{}
	if statusline != nil && statusline.FiveHour != nil {
		out.FiveHour, out.FiveHourObservedAt = statusline.FiveHour, statusline.FiveHourObservedAt
	} else if models != nil {
		out.FiveHour, out.FiveHourObservedAt = models.FiveHour, models.FiveHourObservedAt
	}
	if statusline != nil && statusline.SevenDay != nil {
		out.SevenDay, out.SevenDayObservedAt = statusline.SevenDay, statusline.SevenDayObservedAt
	} else if models != nil {
		out.SevenDay, out.SevenDayObservedAt = models.SevenDay, models.SevenDayObservedAt
	}
	if models != nil {
		out.ModelLimits = models.ModelLimits
	}
	switch {
	case statusline != nil:
		out.ObservedAt = statusline.ObservedAt
	case models != nil:
		out.ObservedAt = models.ObservedAt
	}
	return out
}

// ClaudeHome is $CLAUDE_CONFIG_DIR or ~/.claude.
func ClaudeHome(getenv func(string) string, home string) string {
	if dir := strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
		return dir
	}
	return filepath.Join(home, ".claude")
}

// ReadClaude reads both Claude caches under claudeHome.
func ReadClaude(claudeHome string, now time.Time) *Claude {
	var statusline *Claude
	path := filepath.Join(claudeHome, "cache", "statusline-rate-limits.json")
	if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) <= claudeMaxCacheAge {
		if data, err := os.ReadFile(path); err == nil {
			statusline, _ = ParseClaudeRateLimits(data, info.ModTime())
		}
	}
	var models *Claude
	if data, err := os.ReadFile(filepath.Join(claudeHome, "cache", "claude-model-limits.json")); err == nil {
		models, _ = ParseClaudeModelLimits(data, now)
	}
	return MergeClaude(statusline, models)
}
