package weblink

import (
	"net/url"
	"strings"
	"unicode"
)

func Valid(raw string) bool {
	if strings.ContainsAny(raw, "\\<>\"\x60") || strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != ""
}
