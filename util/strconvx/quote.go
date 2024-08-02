package strconvx

import (
	"strconv"
	"unicode"
)

// ShouldQuote returns true if the string should be quoted.
func ShouldQuote(s string) bool {
	if len(s) == 0 {
		return true
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '\'' || r == '\\' {
			return true
		}
	}
	return false
}

// Quote is similar to strconv.Quote,
// but it only quotes the string if it contains spaces or special characters.
func Quote(s string) string {
	if !ShouldQuote(s) {
		return s
	}
	return strconv.Quote(s)
}
