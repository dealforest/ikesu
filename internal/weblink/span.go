package weblink

// Span identifies a link on one physical row. End is exclusive.
type Span struct {
	Row, Start, End int
	URL             string
}

// At resolves the first span covering the cell. Explicit terminal links can
// be listed before links parsed from text to take precedence.
func At(spans []Span, x, y int) string {
	for _, span := range spans {
		if span.Row == y && x >= span.Start && x < span.End {
			return span.URL
		}
	}
	return ""
}

// AppendSpan joins adjacent cells with the same target.
func AppendSpan(spans []Span, span Span) []Span {
	if n := len(spans); n > 0 {
		last := &spans[n-1]
		if last.Row == span.Row && last.End == span.Start && last.URL == span.URL {
			last.End = span.End
			return spans
		}
	}
	return append(spans, span)
}
