// Package text holds small UTF-8 helpers shared across packages.
package text

import "unicode/utf8"

// ClipRunes returns s truncated to at most n runes. If truncated, a
// Unicode ellipsis is appended. n <= 0 returns "".
func ClipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
