package summary

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/entur/go-ai-logging/internal/compact"
	"github.com/entur/go-ai-logging/internal/stream"
)

// Identity comes from the full event, but the stored display text is bounded.
func TestCandidateTextBounded(t *testing.T) {
	s := New()
	body := strings.Repeat("b", 128<<10)
	id := 0
	for p := range 3 {
		pkg := fmt.Sprintf("pkg%d", p)
		s.Event(&stream.Event{Action: "run", Package: pkg, Test: "TestFail"})
		for w := range 9 {
			id++
			text := fmt.Appendf(nil, `{"severity":"warning","message":"big %d","body":%q}`, w%8, body)
			l := stream.Line{Key: pkg, Package: pkg, Test: "TestFail", Text: text}
			ev := compact.Parse(text)
			s.Line(id, l, compact.Rendered{Event: ev, Body: ev.Render(pkg, "TestFail", "")})
		}
		s.Event(&stream.Event{Action: "fail", Package: pkg, Test: "TestFail"})
		s.Event(&stream.Event{Action: "fail", Package: pkg})
	}

	if len(s.failing) != 3 {
		t.Fatalf("%d failing packages", len(s.failing))
	}
	for _, p := range s.failing {
		if len(p.cands) != maxCandidates || p.cands[0].total != 2 {
			t.Errorf("%s: %d candidates, first total %d", p.name, len(p.cands), p.cands[0].total)
		}
		for _, cd := range p.cands {
			if n := utf8.RuneCountInString(cd.text); n > maxEntry {
				t.Fatalf("%s candidate #%d keeps %d chars", p.name, cd.id, n)
			}
		}
	}
}

func TestHeadKeepsFirstLinesAndCounts(t *testing.T) {
	h := &Head{}
	for i := range StderrLines + 5 {
		h.Add(fmt.Append(nil, "line ", i), false)
	}
	if h.Total != StderrLines+5 || len(h.Lines) != StderrLines || h.Lines[0] != "line 0" {
		t.Errorf("head = %d lines of %d, first %q", len(h.Lines), h.Total, h.Lines[0])
	}
}
