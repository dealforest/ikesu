// Package agyview recognizes the observed terminal rows below an agy Ask footer.
package agyview

import (
	"regexp"
	"strings"
)

// The status row starts with "esc to cancel"; the right side names the model and
// effort, sometimes after the mode ("auto · "), and may add parts such as "1 task(s) · /tasks".
var status = regexp.MustCompile(`^esc to cancel(?:\s{2,}[^·]+(?: · [^·]+)*)?$`)

// Quota rows under the status row. Observed forms (agy 1.5.x):
//   - one row "Gemini 5h 3% (2h39m) · wk 6% (6d16h) │ Claude/GPT 5h 0% (<1m) · wk 50% (1d22h)",
//     with or without " │ Argon wk 0% (...)" at the end;
//   - the same parts wrapped onto separate rows in a narrow pane, the last one possibly cut off;
//   - "quota: --" before the first figures arrive.
//
// Each part must follow the observed figures ("5h 3% (2h39m) · wk 6% (6d16h)", "wk 0% (6d)"),
// cut anywhere from the right but never before the first window: ordinary output such as
// "Gemini finished" is not a quota row.
var (
	quotaWindow  = regexp.MustCompile(`^[0-9]+h$`)
	quotaPercent = regexp.MustCompile(`^[0-9]+%$`)
	quotaParen   = regexp.MustCompile(`^\([^()]*\)$`)
	// The last token may be cut by the pane edge.
	quotaPercentCut = regexp.MustCompile(`^[0-9]+%?$`)
	quotaParenCut   = regexp.MustCompile(`^\([^()]*\)?$`)
)

type quotaToken struct {
	full, cut func(string) bool
}

func literal(word string) quotaToken {
	return quotaToken{func(s string) bool { return s == word }, func(s string) bool { return s != "" && strings.HasPrefix(word, s) }}
}

var (
	percentToken = quotaToken{quotaPercent.MatchString, quotaPercentCut.MatchString}
	parenToken   = quotaToken{quotaParen.MatchString, quotaParenCut.MatchString}
	// "Gemini 5h 3% (2h39m) · wk 6% (6d16h)"; the name and window must be whole.
	modelQuota = []quotaToken{{quotaWindow.MatchString, quotaWindow.MatchString}, percentToken, parenToken, literal("·"), literal("wk"), percentToken, parenToken}
	// "Argon wk 0% (6d)"
	argonQuota = []quotaToken{literal("wk"), percentToken, parenToken}
)

func quotaPart(part string) bool {
	fields := strings.Fields(part)
	if len(fields) < 2 {
		return false
	}
	var tokens []quotaToken
	switch fields[0] {
	case "Gemini", "Claude/GPT":
		tokens = modelQuota
	case "Argon":
		tokens = argonQuota
	default:
		return false
	}
	rest := fields[1:]
	if len(rest) > len(tokens) {
		return false
	}
	for i, field := range rest {
		if i == len(rest)-1 && !tokens[i].cut(field) || i < len(rest)-1 && !tokens[i].full(field) {
			return false
		}
	}
	return true
}

const maxQuotaRows = 3

func quotaRow(line string) bool {
	if line == "quota: --" {
		return true
	}
	for _, part := range strings.Split(line, "│") {
		if !quotaPart(strings.TrimSpace(part)) {
			return false
		}
	}
	return true
}

// Tail accepts only the observed cancel/status line and the optional quota rows.
func Tail(lines []string) bool {
	seenStatus, quotaRows := false, 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !seenStatus && status.MatchString(line) {
			seenStatus = true
			continue
		}
		if seenStatus && quotaRows < maxQuotaRows && quotaRow(line) {
			quotaRows++
			continue
		}
		return false
	}
	return seenStatus
}

var questionHeader = regexp.MustCompile(`^\s*Question ([1-9][0-9]*)/([1-9][0-9]*):`)

// ShortFooter is the footer of a lone question: no Next and no Submit. It is
// valid only under "Question 1/1:"; in a longer set the last one shows Submit.
func ShortFooter(line string) bool {
	return strings.Contains(line, "↑/↓ Navigate") && !strings.Contains(line, "→ Next") && !strings.Contains(line, "Submit") &&
		!strings.Contains(line, "Toggle") && (strings.Contains(line, "enter Select") || strings.Contains(line, "enter Write-in")) &&
		strings.Contains(line, "esc Skip")
}

func HasCurrentFooter(lines []string) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "↑/↓ Navigate") && strings.Contains(lines[i], "enter ") {
			if ShortFooter(lines[i]) && !singleQuestionAbove(lines[:i]) {
				return false
			}
			return Tail(lines[i+1:])
		}
	}
	return false
}

// singleQuestionAbove reports whether the nearest question header above the footer is "Question 1/1:".
func singleQuestionAbove(lines []string) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := questionHeader.FindStringSubmatch(lines[i]); m != nil {
			return m[1] == "1" && m[2] == "1"
		}
	}
	return false
}
