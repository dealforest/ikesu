// Package usage reads the Claude Code and Codex rate-limit state the same way
// AgentPal does (kazuph/AgentPal: ClaudeUsage.swift, CodexUsage.swift and the
// readers in AppDelegate.swift), for the sidebar's usage section.
package usage

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// Bucket is one rate-limit window.
type Bucket struct {
	Label         string
	UsedPercent   float64
	WindowSeconds float64 // 0 when unknown
	ResetAt       float64 // unix seconds (or milliseconds); 0 when unknown
}

// RemainingPercent is 100 - used, clamped to 0...100.
func (b Bucket) RemainingPercent() float64 { return clamp(100 - b.UsedPercent) }

// PaceRemainingPercent is the share of the window still ahead at observedAt:
// using the limit evenly would leave exactly this much. ok is false when the
// window or reset time is unknown.
func (b Bucket) PaceRemainingPercent(observedAt time.Time) (float64, bool) {
	if b.WindowSeconds <= 0 || b.ResetAt == 0 {
		return 0, false
	}
	left := normalizedReset(b.ResetAt) - float64(observedAt.UnixNano())/1e9
	return clamp(left / b.WindowSeconds * 100), true
}

// ResetTime is the reset time, or the zero time when unknown.
func (b Bucket) ResetTime() time.Time {
	if b.ResetAt == 0 {
		return time.Time{}
	}
	sec := normalizedReset(b.ResetAt)
	return time.Unix(int64(sec), 0)
}

func normalizedReset(v float64) float64 {
	if v > 10_000_000_000 {
		return v / 1000
	}
	return v
}

// PaceKind tells whether the remaining share is below (overuse) or above
// (surplus) the even pace.
type PaceKind int

const (
	Overuse PaceKind = iota
	Surplus
)

// PaceDifference is the span between remaining and pace on a 0...100 bar.
type PaceDifference struct {
	Kind         PaceKind
	Lower, Upper float64
}

// Difference returns the span between remaining and pace, or false when they
// are equal.
func Difference(remaining, pace float64) (PaceDifference, bool) {
	r, p := clamp(remaining), clamp(pace)
	if r == p {
		return PaceDifference{}, false
	}
	kind := Surplus
	if r < p {
		kind = Overuse
	}
	return PaceDifference{Kind: kind, Lower: math.Min(r, p), Upper: math.Max(r, p)}, true
}

func clamp(v float64) float64 { return math.Min(math.Max(v, 0), 100) }

// number accepts JSON numbers and numeric strings.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func firstValue(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func decodeObject(data []byte) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// Row is one line of the sidebar's usage section, in AgentPal's order:
// Claude Code weekly, Claude Code 5h, the first per-model weekly limit, Codex weekly.
type Row struct {
	Label      string
	Bucket     *Bucket // nil when unknown
	ObservedAt time.Time
	Codex      bool
}

// Rows builds the usage section's rows.
func Rows(c *Claude, x *Codex) []Row {
	rows := []Row{{Label: "CC W"}, {Label: "CC 5h"}}
	if c != nil {
		rows[0].Bucket, rows[0].ObservedAt = c.SevenDay, firstTime(c.SevenDayObservedAt, c.ObservedAt)
		rows[1].Bucket, rows[1].ObservedAt = c.FiveHour, firstTime(c.FiveHourObservedAt, c.ObservedAt)
		if len(c.ModelLimits) > 0 {
			m := c.ModelLimits[0]
			name := []rune(strings.ToUpper(m.DisplayName))
			if len(name) > 3 {
				name = name[:3]
			}
			b := m.Bucket
			rows = append(rows, Row{Label: string(name) + " W", Bucket: &b, ObservedAt: m.ObservedAt})
		}
	}
	cx := Row{Label: "CX W", Codex: true}
	if x != nil {
		cx.Bucket, cx.ObservedAt = x.Weekly, x.ObservedAt
	}
	return append(rows, cx)
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}
