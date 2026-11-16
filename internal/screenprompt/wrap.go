package screenprompt

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kazuph/ikesu/internal/cellwidth"
)

func screenColumns(lines []string) int {
	width, ruleWidth := 0, 0
	for _, line := range lines {
		width = max(width, cellwidth.String(line))
		if solidRule(line) {
			ruleWidth = max(ruleWidth, cellwidth.String(line))
		}
	}
	if ruleWidth > 0 {
		return ruleWidth
	}
	return width
}

func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// Claude wraps prose at word boundaries, but splits CJK and oversized tokens
// at the screen edge. Keep the physical previous row when deciding that edge.
func wrappedSeparator(previous, next string, columns int) string {
	left, right := strings.TrimSpace(previous), strings.TrimSpace(next)
	if left == "" || right == "" {
		return " "
	}
	a, _ := utf8.DecodeLastRuneInString(left)
	b, _ := utf8.DecodeRuneInString(right)
	if (cjk(a) || a > unicode.MaxASCII && unicode.IsPunct(a)) && (cjk(b) || b > unicode.MaxASCII && unicode.IsPunct(b)) {
		return ""
	}
	fragment := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/\\_.-", r)
	}
	if !fragment(a) || !fragment(b) {
		return " "
	}
	tokenBreak := func(r rune) bool { return unicode.IsSpace(r) || cjk(r) || r > unicode.MaxASCII && unicode.IsPunct(r) }
	last := left
	if i := strings.LastIndexFunc(left, tokenBreak); i >= 0 {
		_, size := utf8.DecodeRuneInString(left[i:])
		last = left[i+size:]
	}
	first := right
	if i := strings.IndexFunc(first, tokenBreak); i >= 0 {
		first = first[:i]
	}
	base := last[strings.LastIndexAny(last, "/\\")+1:]
	pathPrefix := strings.ContainsAny(last, "/\\_-") && !strings.Contains(base, ".")
	pathSuffix := strings.ContainsAny(first, "/\\_-") || strings.Contains(strings.TrimRight(first, ".,;:!?"), ".")
	if pathPrefix && pathSuffix || strings.ContainsRune("/\\_-", a) || strings.ContainsRune("/\\_.-", b) {
		return ""
	}
	indent := cellwidth.String(previous) - cellwidth.String(strings.TrimLeftFunc(previous, unicode.IsSpace))
	if columns > indent && cellwidth.String(last+first) > columns-indent {
		return ""
	}
	if columns > 0 && cellwidth.String(previous) >= columns-1 && strings.ContainsFunc(previous, cjk) {
		return ""
	}
	return " "
}

func joinWrapped(lines []string, columns int) string {
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteString(wrappedSeparator(lines[i-1], line, columns))
		}
		out.WriteString(strings.TrimSpace(line))
	}
	return out.String()
}

// joinBoxRows joins the rows of Claude's left-bordered box (a question or a
// command). Claude wraps the box text itself, moving a whole word to the
// next row only when it does not fit; a row that ends while the next row's
// first word would still have fit ended at a line break in the text, which
// is kept as "\n" (bullets, a command on its own line).
func joinBoxRows(lines []string, columns int) string {
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			if hardRowBreak(lines[i-1], line, columns) {
				out.WriteString("\n")
			} else {
				out.WriteString(wrappedSeparator(lines[i-1], line, columns))
			}
		}
		out.WriteString(strings.TrimSpace(line))
	}
	return out.String()
}

var listMarker = regexp.MustCompile(`^(?:[・•●○■□◆◇▶▸→※★☆✓✔✗✘①②③④⑤⑥⑦⑧⑨⑩]|[-*+] |\d{1,3}[.)、] |[（(]\d{1,3}[)）])`)

func hardRowBreak(previous, next string, columns int) bool {
	if columns <= 0 {
		return false
	}
	left := strings.TrimRightFunc(previous, unicode.IsSpace)
	right := strings.TrimSpace(next)
	if left == "" || right == "" {
		return false
	}
	// A token split across rows (a long path or name) is a wrap, not a break.
	a, _ := utf8.DecodeLastRuneInString(left)
	b, _ := utf8.DecodeRuneInString(right)
	if a <= unicode.MaxASCII && b <= unicode.MaxASCII && wrappedSeparator(previous, next, columns) == "" {
		return false
	}
	// Claude wraps only at spaces, so a row starting with a list marker that
	// has no space before it in the text began on its own line.
	if listMarker.MatchString(right) {
		return true
	}
	word := right
	if i := strings.IndexByte(word, ' '); i >= 0 {
		word = word[:i]
	}
	return cellwidth.String(left)+1+cellwidth.String(word) <= columns
}
