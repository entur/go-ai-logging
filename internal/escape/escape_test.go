package escape

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShellJoin(t *testing.T) {
	got := ShellJoin([]string{"test", "-run=^TestA$", "it's", "./..."})
	if want := `test '-run=^TestA$' 'it'\''s' ./...`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestClip(t *testing.T) {
	const limit = 400
	s := strings.Repeat("é", 500)
	got := Clip(s, limit)
	if utf8.RuneCountInString(got) > limit || !strings.HasSuffix(got, fmt.Sprintf(" …[truncated, %d chars]", 500-limit+clipReserve)) {
		t.Errorf("clip = %q", got)
	}
	if got := Text("a\tb\xffc\u2028"); got != `a\tb\xffc\u2028` {
		t.Errorf("escapeText = %q", got)
	}
}
