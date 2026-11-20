// Package escape makes text safe and bounded for a terminal or a shell: it
// escapes control characters, clips long lines and quotes shell words.
package escape

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// clipReserve is the room kept for the " …[truncated, N chars]" suffix.
const clipReserve = 32

// Text escapes control characters, other non-printable runes and invalid
// UTF-8 the way strconv.Quote does, leaving everything else as it is.
func Text(s string) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, needsEscape) {
		return s
	}
	var b strings.Builder
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 || needsEscape(r) {
			q := strconv.Quote(s[:size])
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteString(s[:size])
		}
		s = s[size:]
	}
	return b.String()
}

func needsEscape(r rune) bool {
	return !unicode.IsPrint(r)
}

// Clip limits s to limit runes, saying how many were dropped. limit must be
// larger than the suffix.
func Clip(s string, limit int) string {
	n := utf8.RuneCountInString(s)
	if n <= limit {
		return s
	}
	keep := limit - clipReserve
	cut := 0
	for range keep {
		_, size := utf8.DecodeRuneInString(s[cut:])
		cut += size
	}
	return s[:cut] + fmt.Sprintf(" …[truncated, %d chars]", n-keep)
}

// Line escapes and clips one line of terminal output.
func Line(s string, limit int) string {
	return Clip(Text(s), limit)
}

// ShellJoin returns args as one POSIX shell command line.
func ShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// ShellQuote returns arg as one POSIX shell word, single-quoted unless it is
// plainly safe.
func ShellQuote(arg string) string {
	if arg != "" && !strings.ContainsFunc(arg, needsShellQuote) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func needsShellQuote(r rune) bool {
	safe := 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("-_./=:,+@%", r)
	return !safe
}
