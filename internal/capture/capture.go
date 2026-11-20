// Package capture writes the run files of one `go test -json` run and keeps
// the state for its summary: it numbers each reassembled logical line and
// fans it out to full.log, compact.log and the summary.
package capture

import (
	"fmt"
	"io"

	"github.com/entur/go-ai-logging/internal/compact"
	"github.com/entur/go-ai-logging/internal/stream"
	"github.com/entur/go-ai-logging/internal/summary"
)

// The run files, created in the run directory.
const (
	RawFile     = "events.jsonl" // the child's stdout, byte for byte
	StderrFile  = "go.stderr"    // the child's stderr, byte for byte
	FullFile    = "full.log"     // every logical line, numbered
	CompactFile = "compact.log"  // one compact line per logical line, repeats collapsed
)

// Capture consumes the child's stdout.
type Capture struct {
	raw    *Sink
	fanout *fanout
	re     *stream.Reassembler
}

// fanout is the stream.Handler that numbers and distributes logical lines.
type fanout struct {
	full    *Sink
	compact *compact.Writer
	state   *summary.State
	seq     int
}

// New writes to the raw, full and compact sinks; cwd ("/"-terminated or "")
// is stripped from paths in compact lines.
func New(raw, full, compactLog *Sink, cwd string) *Capture {
	l := &fanout{full: full, compact: compact.NewWriter(compactLog, cwd), state: summary.New()}
	return &Capture{raw: raw, fanout: l, re: stream.New(l)}
}

// Consume tees r into the raw file and processes it to EOF.
func (c *Capture) Consume(r io.Reader) error {
	return c.re.Consume(r, c.raw.Tee)
}

// Finish flushes everything still pending at EOF.
func (c *Capture) Finish() {
	c.re.Finish()
	c.fanout.compact.Flush()
	c.fanout.state.Finish()
}

// Report renders the summary; o.Loss is filled in from the stream.
func (c *Capture) Report(o summary.Outcome) string {
	o.Loss = c.re.Loss()
	return c.fanout.state.Report(o)
}

func (l *fanout) Line(line stream.Line) {
	l.seq++
	id := l.seq
	l.full.Tee(fmt.Appendf(nil, "#%d %s ?%s %s%s\n", id, line.Package, compact.Hint(line), line.Text, line.Mark))
	l.state.Line(id, line, l.compact.Line(id, line))
}

func (l *fanout) End(key string) {
	l.compact.End(key)
}

func (l *fanout) Event(ev *stream.Event) {
	l.state.Event(ev)
}
