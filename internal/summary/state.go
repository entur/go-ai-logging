// Package summary keeps the bounded per-test and per-package state of a run
// and renders the summary printed after `go test` exits.
package summary

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/entur/go-ai-logging/internal/compact"
	"github.com/entur/go-ai-logging/internal/escape"
	"github.com/entur/go-ai-logging/internal/stream"
)

const (
	// MaxLine bounds every diagnostic line of the summary, in runes.
	MaxLine = 400
	// maxEntry leaves room for the indent and labels around a line body.
	maxEntry = MaxLine - 20

	maxBlockLines = 8
	maxCandidates = 8
	maxFailBlocks = 5
	maxTailLines  = 20
	maxBuildLines = 30
)

type tally struct{ pass, fail, skip, incomplete int }

type testBlock struct {
	pkg, name   string
	first, last int
	lines       []string // the last maxBlockLines non-log lines
	dropped     int      // earlier non-log lines not kept
	events      []ref
}

// ref is a bounded summary entry for one logical line.
type ref struct {
	id   int
	text string
}

type candidate struct {
	ref
	total int
	hint  string
}

type pkgState struct {
	name       string
	active     map[string]*testBlock
	incomplete []string
	failed     int
	crashed    bool // package failed without a failed test
	cached     bool
	tail       []string
	headline   string
	lastLine   string
	cands      []*candidate
	candIndex  map[[32]byte]*candidate
	otherWarns int
}

// State accumulates what the summary needs; everything it keeps is bounded
// per package and per test, and the full text stays in the run files.
type State struct {
	sawEvent, sawNamed bool
	pkgs               map[string]*pkgState

	top, sub                          tally
	ok, fail, buildFail, noTests, hit int
	failing                           []*pkgState
	failBlocks                        []*testBlock
	moreFailed                        int
	builds                            []string
	buildLines                        map[string][]string
	buildLineCount                    int
}

// New returns an empty State.
func New() *State {
	return &State{pkgs: map[string]*pkgState{}, buildLines: map[string][]string{}}
}

// Line records logical line id as rendered for compact.log.
func (s *State) Line(id int, l stream.Line, r compact.Rendered) {
	text := string(l.Text)
	if l.Build {
		if s.buildLineCount < maxBuildLines {
			s.buildLineCount++
			s.buildLines[l.Package] = append(s.buildLines[l.Package], entry(id, escape.Text(text)))
		}
		return
	}

	p := s.pkg(l.Package)
	b := p.active[l.Test]
	if b != nil {
		b.first = cmp.Or(b.first, id)
		b.last = id
	}

	if r.Event == nil {
		if r.Framework {
			p.cached = p.cached || strings.HasPrefix(text, "ok  ") && strings.Contains(text, "(cached)")
			return
		}
		e := entry(id, escape.Text(strings.TrimPrefix(l.Mark+" ", " ")+text))
		if b != nil {
			b.lines = append(b.lines, e)
			if len(b.lines) > maxBlockLines {
				b.lines = b.lines[1:]
				b.dropped++
			}
		}
		p.tail = append(p.tail, e)
		if len(p.tail) > maxTailLines {
			p.tail = p.tail[1:]
		}
		if p.headline == "" && isHeadline(text) {
			p.headline = e
		}
		if !strings.HasPrefix(text, "exit status ") {
			p.lastLine = e
		}
		return
	}

	e := entry(id, r.Body)
	p.lastLine = e
	if !r.Event.Severe() {
		return
	}
	if b != nil && !r.Repeated && len(b.events) < maxBlockLines {
		b.events = append(b.events, ref{id: id, text: e})
	}
	key := r.Event.Key()
	switch cd := p.candIndex[key]; {
	case cd != nil:
		cd.total++
	case len(p.cands) < maxCandidates:
		// Identity is the full event; only a bounded display text is kept.
		cd = &candidate{ref: ref{id: id, text: escape.Clip(r.Body, maxEntry)}, total: 1, hint: compact.Hint(l)}
		p.cands = append(p.cands, cd)
		p.candIndex[key] = cd
	default:
		p.otherWarns++
	}
}

// Event records one test2json event.
func (s *State) Event(ev *stream.Event) {
	s.sawEvent = true
	switch ev.Action {
	case "build-fail":
		s.addBuild(ev.ImportPath)
	case "run":
		if ev.Test != "" {
			s.sawNamed = true
			s.pkg(ev.Package).active[ev.Test] = &testBlock{pkg: ev.Package, name: ev.Test}
		}
	case "pass", "fail", "skip":
		if ev.Test != "" {
			s.testDone(ev)
		} else if ev.Package != "" {
			s.pkgDone(ev)
		}
	default: // start, pause, cont, bench, attr, ... and future actions: kept in the raw file only
	}
}

func (s *State) testDone(ev *stream.Event) {
	s.sawNamed = true
	p := s.pkg(ev.Package)
	b := p.active[ev.Test]
	delete(p.active, ev.Test)

	t := &s.top
	if strings.Contains(ev.Test, "/") {
		t = &s.sub
	}
	switch ev.Action {
	case "pass":
		t.pass++
	case "skip":
		t.skip++
	default:
		t.fail++
		p.failed++
		if len(s.failBlocks) == maxFailBlocks {
			s.moreFailed++
			return
		}
		if b == nil {
			b = &testBlock{pkg: ev.Package, name: ev.Test}
		}
		s.failBlocks = append(s.failBlocks, b)
	}
}

func (s *State) pkgDone(ev *stream.Event) {
	p := s.pkg(ev.Package)
	delete(s.pkgs, ev.Package)
	s.closeTests(p)
	if p.cached {
		s.hit++
	}

	switch {
	case ev.FailedBuild != "":
		s.buildFail++
		s.addBuild(ev.FailedBuild)
		return
	case ev.Action == "pass":
		s.ok++
	case ev.Action == "skip":
		s.noTests++
	default:
		s.fail++
		p.crashed = p.failed == 0
	}
	if p.failed > 0 || p.crashed || len(p.incomplete) > 0 {
		s.failing = append(s.failing, p)
	}
}

// closeTests turns tests that started but never finished into incomplete ones.
func (s *State) closeTests(p *pkgState) {
	for _, name := range slices.Sorted(maps.Keys(p.active)) {
		p.incomplete = append(p.incomplete, name)
		if !strings.Contains(name, "/") {
			s.top.incomplete++
		}
	}
	clear(p.active)
}

// Finish closes the packages that never reported a terminal event.
func (s *State) Finish() {
	for _, name := range slices.Sorted(maps.Keys(s.pkgs)) {
		if p := s.pkgs[name]; len(p.active) > 0 {
			s.closeTests(p)
			s.failing = append(s.failing, p)
		}
	}
}

func (s *State) pkg(name string) *pkgState {
	p := s.pkgs[name]
	if p == nil {
		p = &pkgState{name: name, active: map[string]*testBlock{}, candIndex: map[[32]byte]*candidate{}}
		s.pkgs[name] = p
	}
	return p
}

func (s *State) addBuild(path string) {
	if !slices.Contains(s.builds, path) {
		s.builds = append(s.builds, path)
	}
}

func isHeadline(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "panic:") || strings.HasPrefix(trimmed, "fatal error:") || strings.Contains(trimmed, "test timed out")
}

// entry is a summary line body: the logical line ID first, so clipping never removes it.
func entry(id int, text string) string {
	return escape.Clip(fmt.Sprintf("#%d %s", id, text), maxEntry)
}
