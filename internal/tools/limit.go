package tools

import (
	"strings"
	"unicode/utf8"
)

// completeRecords drops the partial last record of output that was truncated.
// Records end with sep.
func completeRecords(out string, truncated bool, sep byte) string {
	if !truncated {
		return out
	}
	if i := strings.LastIndexByte(out, sep); i >= 0 {
		return out[:i]
	}
	return ""
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of s.
func trimPartialRune(s string) string {
	for i := 1; i < utf8.UTFMax && i <= len(s); i++ {
		if utf8.RuneStart(s[len(s)-i]) {
			if !utf8.FullRuneInString(s[len(s)-i:]) {
				return s[:len(s)-i]
			}
			break
		}
	}
	return s
}
