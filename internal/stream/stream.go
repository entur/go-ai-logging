// Package stream decodes the stdout of `go test -json` and reassembles the
// Output fragments of each package into logical lines, marking lines it
// could not reconstruct completely.
package stream

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const (
	// MaxRecord bounds one stdout record (one test2json event).
	MaxRecord = 1 << 20
	// MaxPending bounds one reassembled logical line.
	MaxPending = 1 << 20
	// TextPackage is the pseudo package of stdout records that are not test2json.
	TextPackage = "-"

	buildKey = "build\x00" // stream key prefix for build output, keyed by ImportPath
)

// How a pending logical line ended.
const (
	endNewline = iota
	endFlush   // package terminal event or EOF without a newline
	endGap     // an undecodable stdout record interrupted the stream
)

// Event is a test2json record (go doc test2json). Unknown fields are ignored.
type Event struct {
	Action      string
	Package     string
	ImportPath  string
	Test        string
	Output      string
	FailedBuild string
}

// Line is one completed logical line.
type Line struct {
	Key     string // reassembly stream: the package, or a build key
	Package string // Package, ImportPath for build output, or TextPackage
	Test    string // Test of the event that completed the line; a hint only
	Text    []byte // without the newline; reused after Handler.Line returns
	Mark    string // full.log suffix, e.g. " [no newline]"
	Opaque  bool   // content is incomplete: never parse it as JSON
	Build   bool   // build output
}

// Handler receives the reassembled stream in order.
type Handler interface {
	// Line receives each completed logical line.
	Line(l Line)
	// End reports that the stream key was flushed and closed.
	End(key string)
	// Event receives each decoded event, after the lines and End it caused.
	Event(ev *Event)
}

// Loss locates logical lines that were truncated or interrupted.
type Loss struct {
	Count      int
	Start, End int // raw record range of the first one, 1-based
}

type pending struct {
	key, pkg string
	test     string // Test of the last event that contributed to the pending line
	buf      []byte
	open     bool // a logical line is pending (possibly empty)
	start    int  // raw record where the pending line began
	cut      bool // pending line exceeded MaxPending; the rest is discarded
	skip     bool // discard up to the next newline after a gap
}

// Reassembler turns stdout records into logical lines for a Handler.
type Reassembler struct {
	h       Handler
	raw     int // records read so far; the current record's 1-based number
	streams map[string]*pending
	loss    Loss
}

// New returns a Reassembler that reports to h.
func New(h Handler) *Reassembler {
	return &Reassembler{h: h, streams: map[string]*pending{}}
}

// Consume tees r to tee and processes it one newline-framed record at a time.
func (r *Reassembler) Consume(rd io.Reader, tee func([]byte)) error {
	return Frame(rd, tee, MaxRecord, r.Record)
}

// Record processes one stdout record; an oversized record is kept only as a
// truncated text line.
func (r *Reassembler) Record(rec []byte, oversized bool) {
	r.raw++
	l := Line{Key: TextPackage, Package: TextPackage, Text: rec, Opaque: oversized}
	var ev Event
	switch {
	case oversized:
		l.Mark = fmt.Sprintf(" [unparsed record, truncated, raw_records=%d:%d]", r.raw, r.raw)
		r.lost(r.raw)
	case json.Unmarshal(rec, &ev) == nil && ev.Action != "":
		r.handle(&ev)
		return
	default:
		l.Mark = fmt.Sprintf(" [unparsed record, raw_records=%d:%d]", r.raw, r.raw)
	}

	// The stream this record belonged to is unknown, so every pending line has a hole.
	for _, key := range slices.Sorted(maps.Keys(r.streams)) {
		if st := r.streams[key]; st.open {
			r.complete(st, endGap)
			st.skip = true
		}
	}
	r.h.Line(l)
}

// Finish flushes every stream still pending at EOF.
func (r *Reassembler) Finish() {
	for _, key := range slices.Sorted(maps.Keys(r.streams)) {
		r.flush(key)
	}
}

// Loss reports the truncated or interrupted lines so far.
func (r *Reassembler) Loss() Loss {
	return r.loss
}

func (r *Reassembler) handle(ev *Event) {
	switch ev.Action {
	case "build-output":
		r.output(buildKey+ev.ImportPath, ev.ImportPath, "", ev.Output)
	case "build-fail":
		r.flush(buildKey + ev.ImportPath)
	case "output":
		r.output(ev.Package, ev.Package, ev.Test, ev.Output)
	case "pass", "fail", "skip":
		if ev.Test == "" && ev.Package != "" {
			r.flush(ev.Package)
		}
	default:
	}
	r.h.Event(ev)
}

func (r *Reassembler) output(key, pkg, test, out string) {
	st := r.streams[key]
	if st == nil {
		st = &pending{key: key, pkg: pkg}
		r.streams[key] = st
	}
	for out != "" {
		piece, rest, newline := strings.Cut(out, "\n")
		out = rest
		if st.skip {
			st.skip = !newline
			continue
		}
		if !st.open {
			st.open, st.start = true, r.raw
		}
		st.test = test
		if room := MaxPending - len(st.buf); len(piece) > room {
			st.cut = true
			piece = piece[:room]
		}
		st.buf = append(st.buf, piece...)
		if newline {
			r.complete(st, endNewline)
		}
	}
}

func (r *Reassembler) complete(st *pending, end int) {
	var mark string
	switch {
	case end == endGap:
		mark = fmt.Sprintf(" [gap, raw_records=%d:%d]", st.start, r.raw)
		r.lost(st.start)
	case st.cut:
		mark = fmt.Sprintf(" [truncated, raw_records=%d:%d]", st.start, r.raw)
		r.lost(st.start)
	default:
	}
	if end == endFlush {
		mark += " [no newline]"
	}
	r.h.Line(Line{
		Key: st.key, Package: st.pkg, Test: st.test, Text: st.buf, Mark: mark,
		Opaque: st.cut || end == endGap, Build: strings.HasPrefix(st.key, buildKey),
	})
	st.buf, st.open, st.cut = st.buf[:0], false, false
	if cap(st.buf) > readBufSize {
		st.buf = nil
	}
}

func (r *Reassembler) flush(key string) {
	if st := r.streams[key]; st != nil && st.open {
		r.complete(st, endFlush)
	}
	delete(r.streams, key)
	r.h.End(key)
}

func (r *Reassembler) lost(start int) {
	r.loss.Count++
	if r.loss.Count == 1 {
		r.loss.Start, r.loss.End = start, r.raw
	}
}
