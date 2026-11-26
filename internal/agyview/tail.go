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
var quotaPart = regexp.MustCompile(`^(?:Gemini|Claude/GPT|Argon)(?: [0-9A-Za-z%()<>/.:·-]+)*$`)

const maxQuotaRows = 3

func quotaRow(line string) bool {
	if line == "quota: --" {
		return true
	}
	for _, part := range strings.Split(line, "│") {
		if !quotaPart.MatchString(strings.TrimSpace(part)) {
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

func HasCurrentFooter(lines []string) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "↑/↓ Navigate") && strings.Contains(lines[i], "enter ") {
			return Tail(lines[i+1:])
		}
	}
	return false
}
