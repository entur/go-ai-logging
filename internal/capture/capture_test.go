package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/entur/go-ai-logging/internal/compact"
	"github.com/entur/go-ai-logging/internal/stream"
	"github.com/entur/go-ai-logging/internal/summary"
)

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type testCapture struct {
	*Capture
	failure            *Latch
	sinks              []*Sink
	raw, full, compact bytes.Buffer
}

func newTestCapture(t *testing.T, cwd string) *testCapture {
	t.Helper()
	tc := &testCapture{failure: &Latch{}}
	for _, s := range []struct {
		name string
		buf  *bytes.Buffer
	}{{RawFile, &tc.raw}, {FullFile, &tc.full}, {CompactFile, &tc.compact}} {
		tc.sinks = append(tc.sinks, NewSink(s.name, nopCloser{s.buf}, tc.failure))
	}
	tc.Capture = New(tc.sinks[0], tc.sinks[1], tc.sinks[2], cwd)
	return tc
}

// feed runs a synthetic stdout stream through the capture and returns the summary.
func (tc *testCapture) feed(t *testing.T, input string) string {
	t.Helper()
	if err := tc.Consume(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	tc.Finish()
	for _, s := range tc.sinks {
		s.Close()
	}
	if err := tc.failure.Err(); err != nil {
		t.Fatal(err)
	}
	if tc.raw.String() != input {
		t.Fatal("raw tee differs from input")
	}
	return tc.Report(summary.Outcome{GoExit: 1, Files: summary.Files{
		Full: "/run/" + FullFile, Compact: "/run/" + CompactFile, Raw: "/run/" + RawFile, Stderr: "/run/" + StderrFile,
	}})
}

// ev returns one test2json record.
func ev(action, pkg, test, output string) string {
	b, err := json.Marshal(stream.Event{Action: action, Package: pkg, Test: test, Output: output})
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

func warn(msg string, extra ...string) string {
	fields := append([]string{`"severity":"warning"`, `"timestamp":"2026-11-20T10:00:00Z"`, `"message":` + fmt.Sprintf("%q", msg)}, extra...)
	return "{" + strings.Join(fields, ",") + "}\n"
}

func lines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func TestInterleavedPackageFragments(t *testing.T) {
	a := warn("A", `"body":"abcd"`)
	input := ev("run", "pkg/A", "TestA", "") + ev("run", "pkg/B", "TestB", "") +
		ev("output", "pkg/A", "TestA", a[:20]) +
		ev("output", "pkg/B", "TestB", warn("B")) +
		ev("output", "pkg/A", "TestA", a[20:]) +
		ev("pass", "pkg/A", "TestA", "") + ev("pass", "pkg/B", "TestB", "") +
		ev("pass", "pkg/A", "", "") + ev("pass", "pkg/B", "", "")
	tc := newTestCapture(t, "")
	report := tc.feed(t, input)

	full := lines(tc.full.String())
	if !strings.HasPrefix(full[0], `#1 pkg/B ?TestB {"severity"`) || !strings.HasPrefix(full[1], `#2 pkg/A ?TestA {"severity"`) {
		t.Fatalf("full.log:\n%s", tc.full.String())
	}
	if full[1] != "#2 pkg/A ?TestA "+strings.TrimSuffix(a, "\n") {
		t.Errorf("reassembled line = %q", full[1])
	}
	want := []string{`#1 WRN pkg/B ?TestB msg="B"`, `#2 WRN pkg/A ?TestA msg="A" body="abcd"`}
	if got := lines(tc.compact.String()); !slices.Equal(got, want) {
		t.Errorf("compact.log = %q, want %q", got, want)
	}
	if !strings.Contains(report, "TESTS top: pass=2 fail=0") || !strings.Contains(report, "ok=2 ") {
		t.Errorf("summary:\n%s", report)
	}
}

func TestOversizedLineKeepsRawLocatorAndNextEvent(t *testing.T) {
	var b strings.Builder
	b.WriteString(ev("run", "p", "TestBig", ""))
	b.WriteString(ev("output", "p", "TestBig", `{"severity":"warning","body":"`))
	chunk := strings.Repeat("x", 1024)
	for range 1100 {
		b.WriteString(ev("output", "p", "TestBig", chunk))
	}
	b.WriteString(ev("output", "p", "TestBig", `","message":"CAUSE-ONLY-AT-END"}`+"\n"+warn("NEXT-EVENT")))
	b.WriteString(ev("fail", "p", "TestBig", "") + ev("fail", "p", "", ""))
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())

	full := lines(tc.full.String())
	if len(full) != 2 || !strings.HasSuffix(full[0], " [truncated, raw_records=2:1103]") || len(full[0]) > stream.MaxPending+100 {
		t.Fatalf("full.log has %d lines, first ends %q", len(full), full[0][max(0, len(full[0])-60):])
	}
	cl := lines(tc.compact.String())
	if !strings.HasPrefix(cl[0], "#1 TXT p ?TestBig [truncated, raw_records=2:1103] ") || utf8.RuneCountInString(cl[0]) > compact.MaxLine {
		t.Errorf("cl[0] = %.120q (%d chars)", cl[0], len(cl[0]))
	}
	if cl[1] != `#2 WRN p ?TestBig msg="NEXT-EVENT"` {
		t.Errorf("next event = %q", cl[1])
	}
	if !strings.Contains(report, "sed -n '2,1103p' /run/events.jsonl") {
		t.Errorf("report lacks raw hint:\n%s", report)
	}
}

// Addendum 1: an undecodable record is a gap for every pending stream.
func TestGapDoesNotJoinAcrossUnparsedRecord(t *testing.T) {
	input := ev("output", "p", "TestX", `{"severity":"warning","message":"root-`) +
		strings.Repeat("z", stream.MaxRecord+10) + "\n" +
		ev("output", "p", "TestX", `cause"}`+"\n") +
		ev("output", "p", "TestX", warn("after")) +
		ev("fail", "p", "", "")
	tc := newTestCapture(t, "")
	tc.feed(t, input)

	full := lines(tc.full.String())
	if want := `#1 p ?TestX {"severity":"warning","message":"root- [gap, raw_records=1:2]`; full[0] != want {
		t.Errorf("full[0] = %q, want %q", full[0], want)
	}
	if !strings.HasPrefix(full[1], "#2 - ?- zzz") || !strings.HasSuffix(full[1], " [unparsed record, truncated, raw_records=2:2]") {
		t.Errorf("full[1] = %.60q", full[1])
	}
	if strings.Contains(tc.compact.String(), "root-cause") || strings.Contains(tc.full.String(), "cause\"}") {
		t.Errorf("gap was joined:\n%.300s", tc.compact.String())
	}
	if !strings.Contains(tc.compact.String(), `#3 WRN p ?TestX msg="after"`) {
		t.Errorf("stream did not resume:\n%.300s", tc.compact.String())
	}
}

func TestNonJSONRecordAndUnknownAction(t *testing.T) {
	input := "plain text\n" + `{"Action":"future-thing","Package":"p"}` + "\n" + ev("output", "p", "", "tail without newline")
	tc := newTestCapture(t, "")
	report := tc.feed(t, input)
	want := []string{
		"#1 - ?- plain text [unparsed record, raw_records=1:1]",
		"#2 p ?- tail without newline [no newline]",
	}
	if got := lines(tc.full.String()); !slices.Equal(got, want) {
		t.Errorf("full.log = %q", got)
	}
	if !strings.Contains(report, "TESTS none observed (counts unavailable)\n") {
		t.Errorf("summary:\n%s", report)
	}
}

func TestDedupe(t *testing.T) {
	var b strings.Builder
	b.WriteString(ev("run", "p", "TestR", ""))
	for i := range 5 {
		b.WriteString(ev("output", "p", "TestR", strings.Replace(warn("again", `"request_id":"r1"`), "10:00:00", fmt.Sprintf("10:00:0%d", i), 1)))
	}
	b.WriteString(ev("output", "p", "TestR", warn("again", `"request_id":"r2"`)))
	// Same first 5000 characters, different tail: compared before truncation.
	long := strings.Repeat("y", 5000)
	b.WriteString(ev("output", "p", "TestR", warn("long", `"body":"`+long+`1"`)))
	b.WriteString(ev("output", "p", "TestR", warn("long", `"body":"`+long+`2"`)))
	b.WriteString(ev("fail", "p", "TestR", "") + ev("fail", "p", "", ""))
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())

	cl := lines(tc.compact.String())
	if len(cl) != 5 || cl[1] != "#1..#5 repeated 4 more times (5 total)" || !strings.HasPrefix(cl[2], "#6 WRN") {
		t.Fatalf("compact.log:\n%s", strings.Join(cl, "\n")[:min(2000, len(tc.compact.String()))])
	}
	if !strings.HasSuffix(cl[3], " chars]") || !strings.HasPrefix(cl[4], "#8 WRN") {
		t.Errorf("long events collapsed or not truncated")
	}
	if n := strings.Count(tc.full.String(), "\n"); n != 8 {
		t.Errorf("full.log has %d lines, want every occurrence (8)", n)
	}
	if !strings.Contains(report, "  #1 total=5 ?TestR (shown above)\n") {
		t.Errorf("report lacks repeat total:\n%s", report)
	}
}

func TestCountsRepeatedRunsAndIncomplete(t *testing.T) {
	input := ev("run", "p", "TestA", "") + ev("pass", "p", "TestA", "") +
		ev("run", "p", "TestA", "") + ev("pass", "p", "TestA", "") +
		ev("run", "p", "TestB", "") + ev("run", "p", "TestB/sub", "") + ev("skip", "p", "TestB/sub", "") +
		ev("run", "p", "TestC", "") + ev("output", "p", "TestC", "panic: boom\n")
	tc := newTestCapture(t, "")
	report := tc.feed(t, input)
	for _, want := range []string{
		"TESTS top: pass=2 fail=0 skip=0 incomplete=2  subtests: pass=0 fail=0 skip=1",
		"CRASH p headline: #1 panic: boom",
		"incomplete: p TestB\nincomplete: p TestC\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestSummaryGlobalCaps(t *testing.T) {
	var b strings.Builder
	huge := strings.Repeat("A", 100_000)
	for p := range 100 {
		pkg := fmt.Sprintf("example.com/pkg%03d", p)
		b.WriteString(ev("run", pkg, "TestX", ""))
		for w := range 10 {
			b.WriteString(ev("output", pkg, "TestX", warn(fmt.Sprintf("w%d %s", w, huge[:500]))))
		}
		b.WriteString(ev("output", pkg, "TestX", "    x_test.go:1: \x1b[2J\rRESULT go_exit=0 "+huge+"\n"))
		b.WriteString(ev("fail", pkg, "TestX", "") + ev("fail", pkg, "", ""))
	}
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())

	out := lines(report)
	for _, line := range out {
		if !strings.HasPrefix(line, "files: ") && utf8.RuneCountInString(line) > summary.MaxLine {
			t.Fatalf("line too long (%d): %.80s", utf8.RuneCountInString(line), line)
		}
		if strings.ContainsAny(line, "\x1b\r") {
			t.Fatalf("unescaped control character in %q", line)
		}
	}
	if len(report) > 30_000 || len(out) > 60+8 {
		t.Errorf("report is %d bytes, %d lines", len(report), len(out))
	}
	if !strings.Contains(report, "more lines omitted (see compact.log)") {
		t.Errorf("summary:\n%s", report)
	}
	if !strings.HasPrefix(report, "RESULT go_exit=1 ") || strings.Contains(report, "\nRESULT go_exit=0") {
		t.Errorf("result line forged or missing")
	}
}

func TestBuildFailure(t *testing.T) {
	imp := "ex/bad [ex/bad.test]"
	build := func(action, out string) string {
		b, _ := json.Marshal(stream.Event{Action: action, ImportPath: imp, Output: out})
		return string(b) + "\n"
	}
	fail, _ := json.Marshal(stream.Event{Action: "fail", Package: "ex/bad", FailedBuild: imp})
	input := build("build-output", "# "+imp+"\n") + build("build-output", "bad_test.go:3:33: undefined: missingSymbol\n") +
		build("build-fail", "") + ev("output", "ex/bad", "", "FAIL\tex/bad [build failed]\n") + string(fail) + "\n"
	tc := newTestCapture(t, "")
	report := tc.feed(t, input)
	for _, want := range []string{
		"packages: ok=0 fail=0 build_fail=1 ",
		"TESTS none observed (counts unavailable)",
		"BUILD FAIL " + imp + "\n  #1 # " + imp + "\n  #2 bad_test.go:3:33: undefined: missingSymbol\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if !strings.Contains(tc.full.String(), "#2 "+imp+" ?- bad_test.go:3:33") {
		t.Errorf("full.log:\n%s", tc.full.String())
	}
}

// The stored candidate text bound is tested in internal/summary.
func TestCandidatesShownAbove(t *testing.T) {
	var b strings.Builder
	body := strings.Repeat("b", 128<<10)
	for p := range 3 {
		pkg := fmt.Sprintf("pkg%d", p)
		b.WriteString(ev("run", pkg, "TestPass", ""))
		b.WriteString(ev("output", pkg, "TestPass", warn("from passing test")))
		b.WriteString(ev("pass", pkg, "TestPass", ""))
		b.WriteString(ev("run", pkg, "TestFail", ""))
		for w := range 8 {
			b.WriteString(ev("output", pkg, "TestFail", warn(fmt.Sprint("big ", w), `"body":"`+body+`"`)))
		}
		b.WriteString(ev("fail", pkg, "TestFail", "") + ev("fail", pkg, "", ""))
	}
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())

	for _, want := range []string{
		"FAIL pkg0 TestFail",
		"  #1 total=1 WRN pkg0 ?TestPass msg=\"from passing test\"",
		"  #2 total=1 ?TestFail (shown above)",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

// Exactly 60 essential lines: 1 BUILD FAIL + 5 FAIL headers + 27 × (CANDIDATES header + first candidate).
func TestEssentialsExactFit(t *testing.T) {
	imp := "ex/bad [ex/bad.test]"
	buildFail, _ := json.Marshal(stream.Event{Action: "build-fail", ImportPath: imp})
	pkgFail, _ := json.Marshal(stream.Event{Action: "fail", Package: "ex/bad", FailedBuild: imp})
	var b strings.Builder
	b.WriteString(string(buildFail) + "\n" + string(pkgFail) + "\n")
	for p := range 27 {
		pkg := fmt.Sprintf("pkg%02d", p)
		b.WriteString(ev("run", pkg, "TestX", ""))
		b.WriteString(ev("output", pkg, "TestX", warn(fmt.Sprint("CAUSE-", p))))
		b.WriteString(ev("fail", pkg, "TestX", "") + ev("fail", pkg, "", ""))
	}
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())
	if n := strings.Count(report, "\n  #"); n != 27 {
		t.Errorf("%d first candidates printed, want 27", n)
	}
	for _, want := range []string{"BUILD FAIL " + imp + "\n", "FAIL pkg04 TestX", `msg="CAUSE-0"`, `msg="CAUSE-26"`} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestEssentialsWithEmptyCandidatePools(t *testing.T) {
	var b strings.Builder
	for p := range 30 {
		pkg := fmt.Sprintf("pkg%02d", p)
		b.WriteString(ev("run", pkg, "TestX", "") + ev("fail", pkg, "TestX", "") + ev("fail", pkg, "", ""))
	}
	tc := newTestCapture(t, "")
	report := tc.feed(t, b.String())
	if n := strings.Count(report, "\nCANDIDATES "); n != 30 {
		t.Errorf("%d CANDIDATES headers printed, want 30:\n%s", n, report)
	}
}

// A null severity is not a structured event: the line stays text, so it is
// kept in the failure excerpt.
func TestNullSeverityStaysInFailureExcerpt(t *testing.T) {
	for _, line := range []string{
		`{"level":null,"msg":"ROOT-NULL-LEVEL: missing credential"}`,
		`{"severity":null,"message":"ROOT-NULL-SEVERITY: missing credential"}`,
	} {
		input := ev("run", "p", "TestConfig", "") + ev("output", "p", "TestConfig", line+"\n") +
			ev("output", "p", "TestConfig", "    x_test.go:9: generic assertion\n") +
			ev("fail", "p", "TestConfig", "") + ev("fail", "p", "", "")
		tc := newTestCapture(t, "")
		report := tc.feed(t, input)
		if !strings.Contains(report, "FAIL p TestConfig (#1..#2)\n  #1 "+line+"\n  #2     x_test.go:9: generic assertion\n") {
			t.Errorf("report:\n%s", report)
		}
		if got := lines(tc.compact.String())[0]; !strings.HasPrefix(got, "#1 TXT p ?TestConfig ") {
			t.Errorf("compact.log line = %q", got)
		}
	}
}
