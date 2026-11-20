package summary

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/entur/go-ai-logging/internal/escape"
	"github.com/entur/go-ai-logging/internal/stream"
)

const (
	maxDiagLines = 60
	// StderrLines is how many go.stderr lines the summary shows.
	StderrLines = 10
)

// Diagnostic line priorities: the line budget goes to essential lines first,
// then to details, each in print order; kept lines keep their section order.
const (
	tierEssential = iota // BUILD FAIL and FAIL headers, CANDIDATES headers with their first candidate, crash headlines, incomplete tests
	tierDetail           // everything else
)

// Files are the paths of the run files shown in the footer.
type Files struct {
	Full, Compact, Raw, Stderr string
}

// Outcome is what the summary reports besides the State.
type Outcome struct {
	GoExit     int
	CaptureErr error
	Stderr     *Head
	Loss       stream.Loss
	Files      Files
}

// Head keeps the first StderrLines lines of a stream and counts all of them.
type Head struct {
	Lines []string
	Total int
}

// Add records one line; it has the signature of a stream.Frame callback.
func (h *Head) Add(line []byte, _ bool) {
	if len(h.Lines) < StderrLines {
		h.Lines = append(h.Lines, string(line))
	}
	h.Total++
}

type diagLine struct {
	text  string
	tier  int
	shows int    // #id of the event this FAIL block line prints
	ref   int    // #id a candidate line repeats
	short string // candidate line used when ref was printed in a FAIL block
}

type diag struct {
	items []diagLine
}

// add escapes and clips one diagnostic line; the result may be amended before the next add.
func (d *diag) add(tier int, format string, args ...any) *diagLine {
	d.items = append(d.items, diagLine{text: escape.Line(fmt.Sprintf(format, args...), MaxLine), tier: tier})
	return &d.items[len(d.items)-1]
}

// lines allocates the budget by tier and returns the kept lines in their original order.
func (d *diag) lines() []string {
	keep := make([]bool, len(d.items))
	budget := maxDiagLines
	for tier := tierEssential; tier <= tierDetail; tier++ {
		for i, item := range d.items {
			if item.tier == tier && budget > 0 {
				keep[i] = true
				budget--
			}
		}
	}
	printed := map[int]bool{}
	for i, item := range d.items {
		if keep[i] && item.shows > 0 {
			printed[item.shows] = true
		}
	}

	var out []string
	omitted := 0
	for i, item := range d.items {
		switch {
		case !keep[i]:
			omitted++
		case item.ref > 0 && printed[item.ref]:
			out = append(out, item.short)
		default:
			out = append(out, item.text)
		}
	}
	if omitted > 0 {
		out = append(out, fmt.Sprintf("... %d more lines omitted (see compact.log)", omitted))
	}
	return out
}

// Report renders the summary printed after the child has exited.
func (s *State) Report(o Outcome) string {
	out := []string{fmt.Sprintf("RESULT go_exit=%d packages: ok=%d fail=%d build_fail=%d no_tests=%d cached=%d",
		o.GoExit, s.ok, s.fail, s.buildFail, s.noTests, s.hit)}
	if o.CaptureErr != nil {
		out = append(out, escape.Line("CAPTURE INCOMPLETE: "+o.CaptureErr.Error(), MaxLine))
	}
	switch {
	case !s.sawEvent:
		out = append(out, "TESTS none observed (counts unavailable: no test2json events)")
	case !s.sawNamed:
		out = append(out, "TESTS none observed (counts unavailable)")
	default:
		out = append(out, fmt.Sprintf("TESTS top: pass=%d fail=%d skip=%d incomplete=%d  subtests: pass=%d fail=%d skip=%d",
			s.top.pass, s.top.fail, s.top.skip, s.top.incomplete, s.sub.pass, s.sub.fail, s.sub.skip))
	}

	d := &diag{}
	s.diagnostics(d, o)
	out = append(out, d.lines()...)

	// Metadata lines are escaped but never clipped, so paths stay usable.
	for _, f := range []struct{ label, path string }{
		{"full", o.Files.Full}, {"compact", o.Files.Compact}, {"raw", o.Files.Raw}, {"stderr", o.Files.Stderr},
	} {
		out = append(out, f.label+": "+escape.Text(f.path))
	}
	out = append(out, "drill down: grep -n '^#<id> ' "+escape.Text(escape.ShellQuote(o.Files.Full)))
	if o.Loss.Count > 0 {
		out = append(out, fmt.Sprintf("incomplete lines: %d (truncated or gap); raw records: sed -n '%d,%dp' %s",
			o.Loss.Count, o.Loss.Start, o.Loss.End, escape.Text(escape.ShellQuote(o.Files.Raw))))
	}
	return strings.Join(out, "\n") + "\n"
}

func (s *State) diagnostics(d *diag, o Outcome) {
	if h := o.Stderr; h != nil && h.Total > 0 {
		d.add(tierDetail, "STDERR %d lines", h.Total)
		for _, line := range h.Lines {
			d.add(tierDetail, "  %s", line)
		}
	}
	if p := s.pkgs[stream.TextPackage]; !s.sawEvent && o.GoExit != 0 && p != nil {
		d.add(tierDetail, "OUTPUT TAIL (no test2json events)")
		for _, line := range p.tail {
			d.add(tierDetail, "  %s", line)
		}
	}

	for _, path := range s.builds {
		d.add(tierEssential, "BUILD FAIL %s", path)
		for _, line := range s.buildLines[path] {
			d.add(tierDetail, "  %s", line)
		}
	}

	for _, b := range s.failBlocks {
		d.add(tierEssential, "FAIL %s %s (#%d..#%d)", b.pkg, b.name, b.first, b.last)
		if b.dropped > 0 {
			d.add(tierDetail, "  (+%d earlier lines, see full.log)", b.dropped)
		}
		for _, line := range b.lines {
			d.add(tierDetail, "  %s", line)
		}
		for _, ev := range b.events {
			d.add(tierDetail, "  %s", ev.text).shows = ev.id
		}
	}
	if s.moreFailed > 0 {
		d.add(tierDetail, "... %d more failed tests not shown (grep -e '--- FAIL' full.log)", s.moreFailed)
	}

	for _, p := range s.failing {
		d.add(tierEssential, "CANDIDATES %s (test hints are uncertain; includes passing tests)", p.name)
		for i, cd := range p.cands {
			tier := tierDetail
			if i == 0 {
				tier = tierEssential
			}
			line := d.add(tier, "  #%d total=%d %s", cd.id, cd.total, cd.text)
			line.ref = cd.id
			line.short = escape.Line(fmt.Sprintf("  #%d total=%d ?%s (shown above)", cd.id, cd.total, cd.hint), MaxLine)
		}
		if len(p.cands) == 0 {
			d.add(tierDetail, "  (no WRN/ERR/FTL/PNC events)")
		}
		if p.otherWarns > 0 {
			d.add(tierDetail, "  ... %d more WRN/ERR/FTL/PNC events in compact.log", p.otherWarns)
		}
		if p.crashed || len(p.incomplete) > 0 {
			d.add(tierEssential, "CRASH %s headline: %s", p.name, cmp.Or(p.headline, p.lastLine, "(none)"))
			for _, line := range p.tail {
				d.add(tierDetail, "  %s", line)
			}
		}
	}

	for _, p := range s.failing {
		for _, name := range p.incomplete {
			d.add(tierEssential, "incomplete: %s %s", p.name, name)
		}
	}
}
