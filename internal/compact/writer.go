package compact

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/entur/go-ai-logging/internal/escape"
	"github.com/entur/go-ai-logging/internal/stream"
)

// MaxLine bounds one compact.log line, in runes.
const MaxLine = 4096

// Rendered is the compact view of one logical line.
type Rendered struct {
	Event     *Event // nil for a text line
	Body      string // the compact line without the leading "#N "
	Framework bool   // a line printed by the testing package or go test itself
	Repeated  bool   // the same event as the previous one of its stream and hint
}

type repeat struct {
	key                [32]byte
	hint               string
	first, last, extra int
}

// Writer writes compact.log, collapsing adjacent repeats per stream and hint.
type Writer struct {
	out     io.Writer
	cwd     string
	repeats map[string]*repeat
}

// NewWriter writes to out; cwd ("/"-terminated or "") is stripped from paths.
func NewWriter(out io.Writer, cwd string) *Writer {
	return &Writer{out: out, cwd: cwd, repeats: map[string]*repeat{}}
}

// Hint is the "?Test" hint of a line: its Test, or "-".
func Hint(l stream.Line) string {
	return cmp.Or(l.Test, "-")
}

// Line renders logical line id and writes it unless it repeats the previous
// event of its stream or is framework output.
func (w *Writer) Line(id int, l stream.Line) Rendered {
	hint := Hint(l)
	var r Rendered
	if !l.Opaque {
		r.Event = Parse(l.Text)
	}
	if r.Event != nil {
		r.Body = r.Event.Render(l.Package, hint, w.cwd)
	} else {
		text := string(l.Text)
		r.Framework = IsFramework(text)
		r.Body = fmt.Sprintf("TXT %s ?%s%s %s", l.Package, hint, l.Mark, strconv.Quote(text))
	}

	if rep := w.repeats[l.Key]; r.Event != nil && rep != nil && rep.key == r.Event.key && rep.hint == hint {
		rep.last, r.Repeated = id, true
		rep.extra++
		return r
	}
	w.End(l.Key)
	if !r.Framework {
		w.put(escape.Clip(fmt.Sprintf("#%d %s", id, r.Body), MaxLine) + "\n")
	}
	if r.Event != nil {
		w.repeats[l.Key] = &repeat{key: r.Event.key, hint: hint, first: id, last: id}
	}
	return r
}

// End writes the pending repeat line of a stream, if any.
func (w *Writer) End(key string) {
	if rep := w.repeats[key]; rep != nil && rep.extra > 0 {
		w.put(fmt.Sprintf("#%d..#%d repeated %d more times (%d total)\n", rep.first, rep.last, rep.extra, rep.extra+1))
	}
	delete(w.repeats, key)
}

// Flush writes every pending repeat line, in stream key order.
func (w *Writer) Flush() {
	for _, key := range slices.Sorted(maps.Keys(w.repeats)) {
		w.End(key)
	}
}

func (w *Writer) put(s string) {
	_, _ = io.WriteString(w.out, s) // the capture sink latches write errors itself
}

// IsFramework reports lines printed by the testing package or go test itself.
// Anything else, including look-alikes such as "=== CONFIG", is application text.
func IsFramework(text string) bool {
	trimmed := strings.TrimLeft(text, " ")
	switch {
	case strings.HasPrefix(trimmed, "=== RUN "), strings.HasPrefix(trimmed, "=== PAUSE "),
		strings.HasPrefix(trimmed, "=== CONT "), strings.HasPrefix(trimmed, "=== NAME "),
		strings.HasPrefix(trimmed, "--- PASS: "), strings.HasPrefix(trimmed, "--- FAIL: "),
		strings.HasPrefix(trimmed, "--- SKIP: "):
		return true
	case strings.HasPrefix(text, "ok  \t"), strings.HasPrefix(text, "FAIL\t"), strings.HasPrefix(text, "?   \t"):
		return true
	default:
		return text == "PASS" || text == "FAIL"
	}
}
