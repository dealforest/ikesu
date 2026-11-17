package usage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// Codex is Codex's rate-limit state.
type Codex struct {
	ShortTerm, Weekly, Monthly *Bucket
	ObservedAt                 time.Time
	Source                     string
}

// ParseCodex reads the usage API response or a codex.rate_limits log entry.
func ParseCodex(data []byte, observedAt time.Time, source string) (*Codex, bool) {
	m, ok := decodeObject(data)
	if !ok {
		return nil, false
	}
	var rate map[string]any
	for _, k := range []string{"rate_limit", "limits", "rate_limits"} {
		if v, ok := m[k].(map[string]any); ok {
			rate = v
			break
		}
	}
	buckets := codexBuckets(rate)
	switch extra := m["additional_rate_limits"].(type) {
	case []any:
		for _, item := range extra {
			if im, ok := item.(map[string]any); ok {
				r, _ := im["rate_limit"].(map[string]any)
				buckets = append(buckets, codexBuckets(r)...)
			}
		}
	case map[string]any:
		for _, item := range extra {
			if r, ok := item.(map[string]any); ok {
				buckets = append(buckets, codexBuckets(r)...)
			}
		}
	}
	c := &Codex{
		ShortTerm:  bestBucket(buckets, 0, 1, "short"),
		Weekly:     bestBucket(buckets, 5, 10, "weekly"),
		Monthly:    bestBucket(buckets, 20, 45, "monthly"),
		ObservedAt: observedAt,
		Source:     source,
	}
	if c.ShortTerm == nil && c.Weekly == nil && c.Monthly == nil {
		return nil, false
	}
	return c, true
}

func codexBuckets(rate map[string]any) []Bucket {
	if rate == nil {
		return nil
	}
	var out []Bucket
	for _, p := range [][2]string{{"primary", "primary_window"}, {"secondary", "secondary_window"}, {"additional", ""}} {
		v := rate[p[0]]
		if v == nil && p[1] != "" {
			v = rate[p[1]]
		}
		if b, ok := codexBucket(v, p[0]); ok {
			out = append(out, b)
		}
	}
	return out
}

func codexBucket(v any, fallback string) (Bucket, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Bucket{}, false
	}
	used, ok := number(m["used_percent"])
	if !ok {
		return Bucket{}, false
	}
	window, ok := number(m["limit_window_seconds"])
	if !ok {
		if minutes, ok2 := number(m["window_minutes"]); ok2 {
			window = minutes * 60
		}
	}
	reset, _ := number(m["reset_at"])
	label := fallback
	if window > 0 {
		label = windowLabel(window)
	}
	return Bucket{Label: label, UsedPercent: clamp(used), WindowSeconds: window, ResetAt: reset}, true
}

func bestBucket(buckets []Bucket, minDays, maxDays float64, fallback string) *Bucket {
	lo, hi := minDays*86400, maxDays*86400
	for _, b := range buckets {
		if b.WindowSeconds > 0 && b.WindowSeconds >= lo && b.WindowSeconds <= hi {
			b := b
			return &b
		}
	}
	for _, b := range buckets {
		if strings.Contains(strings.ToLower(b.Label), fallback) {
			b := b
			return &b
		}
	}
	return nil
}

func windowLabel(seconds float64) string {
	if days := int(math.Round(seconds / 86400)); days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	if hours := int(math.Round(seconds / 3600)); hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(math.Round(seconds/60)))
}

// CodexUsageURL is the endpoint Codex Desktop reads its limits from.
const CodexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// ReadCodex asks the usage API with the token in codexHome/auth.json and
// falls back to the newest codex.rate_limits entry in the local log (only
// when it is under 30 minutes old).
func ReadCodex(ctx context.Context, client *http.Client, codexHome string, now time.Time) *Codex {
	if c := readCodexLive(ctx, client, codexHome, now); c != nil {
		return c
	}
	return readCodexLog(codexHome, now)
}

func readCodexLive(ctx context.Context, client *http.Client, codexHome string, now time.Time) *Codex {
	token := codexAccessToken(codexHome)
	if token == "" || client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CodexUsageURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("originator", "Codex Desktop")
	if id := codexAccountID(token); id != "" {
		req.Header.Set("chatgpt-account-id", id)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	c, _ := ParseCodex(data, now, "live")
	return c
}

func codexAccessToken(codexHome string) string {
	data, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		return ""
	}
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return ""
	}
	return auth.Tokens.AccessToken
}

func codexAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

func readCodexLog(codexHome string, now time.Time) *Codex {
	path := filepath.Join(codexHome, "logs_2.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	conn, err := sqlite3.OpenFlags("file:"+path+"?mode=ro", sqlite3.OPEN_READONLY|sqlite3.OPEN_URI)
	if err != nil {
		return nil
	}
	defer conn.Close()
	st, _, err := conn.Prepare(`SELECT ts, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%"type":"codex.rate_limits"%' ORDER BY ts DESC, ts_nanos DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil
	}
	defer st.Close()
	if !st.Step() {
		return nil
	}
	observed := time.Unix(0, int64(st.ColumnFloat(0)*1e9))
	if now.Sub(observed) > 30*time.Minute {
		return nil
	}
	body := extractRateLimitJSON(st.ColumnText(1))
	if body == "" {
		return nil
	}
	c, _ := ParseCodex([]byte(body), observed, "log")
	return c
}

// extractRateLimitJSON cuts the first {"type":"codex.rate_limits"...} object
// out of a log body.
func extractRateLimitJSON(body string) string {
	start := strings.Index(body, `{"type":"codex.rate_limits"`)
	if start < 0 {
		return ""
	}
	depth, inString, escaping := 0, false, false
	for i := start; i < len(body); i++ {
		c := body[i]
		switch {
		case inString && escaping:
			escaping = false
		case inString && c == '\\':
			escaping = true
		case inString && c == '"':
			inString = false
		case inString:
		case c == '"':
			inString = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return body[start : i+1]
			}
		}
	}
	return ""
}

// CodexHome is $CODEX_HOME or ~/.codex.
func CodexHome(getenv func(string) string, home string) string {
	if dir := strings.TrimSpace(getenv("CODEX_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}
