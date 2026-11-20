// Package agyview recognizes the observed terminal rows below an agy Ask footer.
package agyview

import (
	"regexp"
	"strings"
)

var status = regexp.MustCompile(`^esc to cancel(?:\s{2,}[^\s·]+ · [^·]+ · [^\s·]+)?$`)
var quota = regexp.MustCompile(`^Gemini [0-9]+h [0-9]+% \([^)]*\) · wk [0-9]+% \([^)]*\) │ Claude/GPT [0-9]+h [0-9]+% \([^)]*\) · wk [0-9]+% \([^)]*\) │ Argon wk [0-9]+% \([^)]*\)$`)

// Tail accepts only the observed cancel/status line and optional quota line.
func Tail(lines []string) bool {
	seenStatus, seenQuota := false, false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !seenStatus && status.MatchString(line) {
			seenStatus = true
			continue
		}
		if seenStatus && !seenQuota && quota.MatchString(line) {
			seenQuota = true
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
