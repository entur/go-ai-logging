package ailogging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/entur/go-ai-logging/internal/capture"
	"github.com/entur/go-ai-logging/internal/escape"
)

type fixtureRun struct {
	code            int
	summary, runDir string
	full, compact   string
}

// runFixture runs ailog in-process against the fixture module with a real go test child.
func runFixture(t *testing.T, create func(dir, name string) (io.WriteCloser, error), args ...string) fixtureRun {
	t.Helper()
	integration(t)
	return runFixtureIn(t, t.TempDir(), create, args...)
}

func integration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test runs go test")
	}
	t.Parallel()
}

// runFixtureIn uses parent as -ailog.out; parent must be empty.
func runFixtureIn(t *testing.T, parent string, create func(dir, name string) (io.WriteCloser, error), args ...string) fixtureRun {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseArgs(append([]string{"-ailog.out=" + parent}, args...))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg.Stdout, cfg.Dir, cfg.create = &out, dir, create
	res := Run(t.Context(), cfg)

	r := fixtureRun{code: res.ExitCode, summary: out.String()}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "ailog-") {
		t.Fatalf("run dir not found under %q (%v):\n%s", parent, err, r.summary)
	}
	r.runDir = filepath.Join(parent, entries[0].Name())
	if res.RunDir != r.runDir {
		t.Errorf("Result.RunDir = %q, want %q", res.RunDir, r.runDir)
	}
	root, err := os.OpenRoot(r.runDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	read := func(name string) string {
		b, err := root.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	r.full, r.compact = read(capture.FullFile), read(capture.CompactFile)
	return r
}

func (r fixtureRun) expect(t *testing.T, code int, wants ...string) {
	t.Helper()
	if r.code != code {
		t.Errorf("exit = %d, want %d", r.code, code)
	}
	for _, want := range wants {
		if !strings.Contains(r.summary, want) {
			t.Errorf("summary lacks %q", want)
		}
	}
	if t.Failed() {
		t.Logf("summary:\n%s", r.summary)
	}
}

func TestIntegrationAllPass(t *testing.T) {
	r := runFixture(t, createFile, "./pass")
	r.expect(t, 0,
		"cmd: go test -json -count=1 ./pass\n",
		"RESULT go_exit=0 packages: ok=1 fail=0 build_fail=0 no_tests=0 cached=0\n",
		"TESTS top: pass=2 fail=0 skip=0 incomplete=0  subtests: pass=2 fail=0 skip=0\n")
	if strings.Contains(r.summary, "FAIL") || strings.Contains(r.summary, "CANDIDATES") {
		t.Errorf("diagnostics for a passing run:\n%s", r.summary)
	}
	if !strings.Contains(r.compact, `?TestPass at=pass/pass_test.go:10 msg="warning in a passing test" request_id="req-1"`) {
		t.Errorf("compact.log:\n%s", r.compact)
	}
	if info, err := os.Stat(r.runDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("run dir mode: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(r.runDir, capture.FullFile)); err != nil || info.Mode().Perm() != filePerm {
		t.Errorf("full.log mode: %v %v", info, err)
	}
}

func TestIntegrationWarningOnlyFailures(t *testing.T) {
	r := runFixture(t, createFile, "./warnfail")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=2 ",
		"warnfail_test.go:12: generic assertion A",
		`?TestWarnA at=warnfail/warnfail_test.go:11 msg="upstream rejected credential A" request_id="req-a"`,
		"FAIL example.com/ailogfixtures/warnfail TestWarnB",
		`msg="upstream rejected credential B" error="token expired" `+
			`stack="example.com/ailogfixtures/warnfail.TestWarnB warnfail/warnfail_test.go:16 > testing.tRunner `)
}

func TestIntegrationSlogJSON(t *testing.T) {
	r := runFixture(t, createFile, "./slogjson")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=2 ",
		`?TestSlogGlobal at=slogjson/slogjson_test.go:12 msg="upstream rejected credential S" attempt=2 request_id="req-s"`,
		`ERR example.com/ailogfixtures/slogjson ?TestSlogOutput msg="exact attribution" tenant=17`,
		"slogjson_test.go:13: generic assertion S")
}

func TestIntegrationParallelMisattributionKeptInCandidates(t *testing.T) {
	r := runFixture(t, createFile, "./parallel")
	r.expect(t, 1,
		"CANDIDATES example.com/ailogfixtures/parallel (test hints are uncertain; includes passing tests)",
		`?TestParallel/B at=parallel/parallel_test.go:16 msg="GLOBAL actual=A"`)
}

func TestIntegrationBuildFailure(t *testing.T) {
	r := runFixture(t, createFile, "./buildfail", "./pass")
	r.expect(t, 1,
		"packages: ok=1 fail=0 build_fail=1 ",
		"BUILD FAIL example.com/ailogfixtures/buildfail [example.com/ailogfixtures/buildfail.test]",
		"undefined: missingSymbol")
}

func TestIntegrationLoggingFatalIsIncomplete(t *testing.T) {
	r := runFixture(t, createFile, "./fatal")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=0 skip=0 incomplete=1 ",
		`CRASH example.com/ailogfixtures/fatal headline: #2 FTL example.com/ailogfixtures/fatal ?TestFatal at=fatal/fatal_test.go:10 msg="fatal cause"`,
		"incomplete: example.com/ailogfixtures/fatal TestFatal")
}

func TestIntegrationStdlibFatalAfterFailure(t *testing.T) {
	r := runFixture(t, createFile, "./stdfatal")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=1 skip=0 incomplete=1 ",
		"FAIL example.com/ailogfixtures/stdfatal TestOrdinary",
		"CRASH example.com/ailogfixtures/stdfatal headline: ",
		"fatal cause: x",
		"incomplete: example.com/ailogfixtures/stdfatal TestStdFatal")
}

func TestIntegrationTimeoutAfterFailure(t *testing.T) {
	r := runFixture(t, createFile, "-timeout=200ms", "./timeout")
	r.expect(t, 1,
		"FAIL example.com/ailogfixtures/timeout TestOrdinary",
		"panic: test timed out after 200ms",
		"incomplete: example.com/ailogfixtures/timeout TestSlow\nincomplete: example.com/ailogfixtures/timeout TestSlow/inner\n")
}

func TestIntegrationSubtestCounts(t *testing.T) {
	r := runFixture(t, createFile, "./subtests")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=1 skip=1 incomplete=0  subtests: pass=1 fail=1 skip=1\n",
		"FAIL example.com/ailogfixtures/subtests TestParent/bad",
		"subtests_test.go:7: sub failure")
}

func TestIntegrationOversizedLine(t *testing.T) {
	r := runFixture(t, createFile, "./huge")
	r.expect(t, 1, "incomplete lines: 1 (truncated or gap); raw records: sed -n '", "huge_test.go:14: huge failure")
	if !strings.Contains(r.full, "[truncated, raw_records=") || strings.Contains(r.full, "CAUSE-ONLY-AT-END") {
		t.Error("full.log lacks the truncation locator")
	}
	if !strings.Contains(r.compact, `?TestHuge at=huge/huge_test.go:13 msg="NEXT-EVENT"`) {
		t.Errorf("next event lost:\n%.500s", r.compact)
	}
}

func TestIntegrationCountOverride(t *testing.T) {
	r := runFixture(t, createFile, "-count=2", "-run=^TestCount$", "./flags")
	r.expect(t, 0, "TESTS top: pass=2 fail=0 ")
}

func TestIntegrationJSONDisabled(t *testing.T) {
	r := runFixture(t, createFile, "-json=false", "-run=^TestCount$", "./flags")
	r.expect(t, 0, "TESTS none observed (counts unavailable: no test2json events)")
	if !strings.Contains(r.full, "ok  \texample.com/ailogfixtures/flags") {
		t.Errorf("full.log:\n%s", r.full)
	}
}

func TestIntegrationArgsPassedThrough(t *testing.T) {
	r := runFixture(t, createFile, "-run=^TestFlag$", "./flags", "-args", "-ailog.out=child-value")
	r.expect(t, 0, "cmd: go test -json -count=1 '-run=^TestFlag$' ./flags -args -ailog.out=child-value\n")
	if !strings.Contains(r.full, "child_out=child-value") {
		t.Errorf("full.log:\n%s", r.full)
	}
}

func TestIntegrationEscapesSummary(t *testing.T) {
	r := runFixture(t, createFile, "./escape")
	r.expect(t, 1, `escape_test.go:6: \x1b[2J\rRESULT go_exit=0`)
	if strings.ContainsAny(r.summary, "\x1b\r") {
		t.Error("summary contains raw control characters")
	}
	if !strings.Contains(r.full, "\x1b[2J\rRESULT") {
		t.Error("full.log is not verbatim")
	}
}

type failingFile struct {
	io.WriteCloser
	left int
}

func (f *failingFile) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errors.New("disk full")
	}
	f.left -= len(p)
	return f.WriteCloser.Write(p)
}

func TestIntegrationWriteFailureDrainsAndExits2(t *testing.T) {
	create := func(dir, name string) (io.WriteCloser, error) {
		f, err := createFile(dir, name)
		if err != nil || name != capture.RawFile {
			return f, err
		}
		return &failingFile{WriteCloser: f, left: 100_000}, nil
	}
	// noisy writes about 4 MB; the child would block on a full pipe if ailog stopped reading.
	r := runFixture(t, create, "./noisy")
	r.expect(t, ExitFailure,
		"RESULT go_exit=0 packages: ok=1 ",
		"CAPTURE INCOMPLETE: write events.jsonl: disk full")
}

func TestIntegrationJSONDisabledFailureShowsOutputTail(t *testing.T) {
	r := runFixture(t, createFile, "-json=false", "./escape")
	r.expect(t, 1,
		"TESTS none observed (counts unavailable: no test2json events)",
		"OUTPUT TAIL (no test2json events)",
		`escape_test.go:6: \x1b[2J\rRESULT go_exit=0`)
}

func TestIntegrationFrameworkLookalikeIsKept(t *testing.T) {
	r := runFixture(t, createFile, "./framework")
	r.expect(t, 1, "=== CONFIG: missing service credential")
	if !strings.Contains(r.compact, `"=== CONFIG: missing service credential"`) {
		t.Errorf("compact.log:\n%s", r.compact)
	}
}

func TestIntegrationFailureExcerptKeepsTail(t *testing.T) {
	r := runFixture(t, createFile, "./excerpt")
	r.expect(t, 1, "  (+3 earlier lines, see full.log)", "ACTUAL-ASSERTION: tenant 17 missing credential")
}

func TestIntegrationBusyFailuresThenCrash(t *testing.T) {
	r := runFixture(t, createFile, "./budget")
	r.expect(t, 1,
		"TESTS top: pass=0 fail=1 skip=0 incomplete=1  subtests: pass=0 fail=5 skip=0",
		"CRASH example.com/ailogfixtures/budget headline: ",
		"fatal cause: SPEC-CRASH-MUST-SHOW",
		"incomplete: example.com/ailogfixtures/budget TestCrashLast",
		"more lines omitted (see compact.log)")
	if n := strings.Count(r.summary, "\n"); n > 60+12 {
		t.Errorf("summary has %d lines", n)
	}
}

func TestIntegrationParentWithSpaces(t *testing.T) {
	integration(t)
	parent := filepath.Join(t.TempDir(), "logs with spaces")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	r := runFixtureIn(t, parent, createFile, "./pass")
	r.expect(t, 0)
	var grep string
	for line := range strings.Lines(r.summary) {
		if cmd, ok := strings.CutPrefix(line, "drill down: "); ok {
			grep = strings.ReplaceAll(strings.TrimSpace(cmd), "<id>", "1")
		}
	}
	out, err := exec.CommandContext(t.Context(), "sh", "-c", grep).Output()
	if err != nil || !strings.Contains(string(out), "1:#1 ") {
		t.Errorf("%s: %v\n%s", grep, err, out)
	}
}

func TestIntegrationParentWithControlCharacters(t *testing.T) {
	integration(t)
	parent := filepath.Join(t.TempDir(), "evil\x1b[2J\r\nRESULT go_exit=0")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Skip("file system refuses the name:", err)
	}
	r := runFixtureIn(t, parent, createFile, "./pass")
	r.expect(t, 0, `evil\x1b[2J\r\nRESULT go_exit=0/ailog-`)
	if strings.ContainsAny(r.summary, "\x1b\r") || strings.Count(r.summary, "\nRESULT ") != 1 {
		t.Errorf("unescaped path in summary:\n%q", r.summary)
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args   []string
		parent string
		rest   []string
	}{
		{[]string{"./..."}, "", []string{"./..."}},
		{[]string{"-ailog.out=/o", "-run", "X", "./..."}, "/o", []string{"-run", "X", "./..."}},
		{[]string{"-v", "-ailog.out", "/o", "./p"}, "/o", []string{"-v", "./p"}},
		{[]string{"./p", "-args", "-ailog.out=x", "-ailog.out", "y"}, "", []string{"./p", "-args", "-ailog.out=x", "-ailog.out", "y"}},
	}
	for _, tt := range tests {
		cfg, err := ParseArgs(tt.args)
		if err != nil || cfg.OutParent != tt.parent || !slices.Equal(cfg.Args, tt.rest) {
			t.Errorf("ParseArgs(%q) = %q, %q, %v", tt.args, cfg.OutParent, cfg.Args, err)
		}
	}
	if _, err := ParseArgs([]string{"-ailog.out"}); err == nil {
		t.Error("missing value accepted")
	}
}

func TestStartupErrorIsEscaped(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "missing\x1b[2J\r\nRESULT go_exit=0\n", "x")
	var out bytes.Buffer
	res := Run(t.Context(), Config{Args: []string{"./..."}, OutParent: parent, Stdout: &out})
	if res.ExitCode != ExitFailure || res.GoExit != -1 || res.RunDir != "" || res.Err == nil ||
		!strings.Contains(out.String(), "AILOG ERROR: create run dir: ") {
		t.Errorf("%+v:\n%s", res, out.String())
	}
	if strings.ContainsAny(out.String(), "\x1b\r") || slices.Contains(strings.Split(out.String(), "\n"), "RESULT go_exit=0") {
		t.Errorf("unescaped error output: %q", out.String())
	}
}

// syncBuffer is a bytes.Buffer safe for one writer and concurrent readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The go command ignores an interrupt while its test process runs, so it is
// killed after the wait delay; Run still reports and does not claim success.
func TestIntegrationCancelEndsRun(t *testing.T) {
	integration(t)
	dir, err := filepath.Abs(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &syncBuffer{}
	cfg := Config{Args: []string{"-run=^TestSlow$", "./timeout"}, OutParent: t.TempDir(), Dir: dir, Stdout: out, waitDelay: 100 * time.Millisecond}
	done := make(chan Result)
	go func() { done <- Run(ctx, cfg) }()
	for !strings.Contains(out.String(), "cmd: ") {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(time.Second) // let go test start the test process
	cancel()
	select {
	case res := <-done:
		// Killed: -1. Should go stop by itself while still building, it fails instead.
		if res.ExitCode == 0 || res.GoExit == 0 || !strings.Contains(out.String(), "\nRESULT go_exit=") {
			t.Errorf("%+v:\n%s", res, out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// shellGo writes an executable sh script to use as Config.GoCommand.
func shellGo(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	p := filepath.Join(t.TempDir(), "fake-go")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not created", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// cancelWhenReady runs cfg, cancels once ready exists and returns the result
// with the time Run took after the cancellation.
func cancelWhenReady(t *testing.T, cfg Config, ready string) (Result, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Run(ctx, cfg) }()
	waitForFile(t, ready)
	start := time.Now()
	cancel()
	select {
	case res := <-done:
		return res, time.Since(start)
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancel")
		return Result{}, 0
	}
}

func expectCutOff(t *testing.T, res Result, took time.Duration, out fmt.Stringer) {
	t.Helper()
	if took > 100*time.Millisecond+killDrain+time.Second {
		t.Errorf("Run returned %s after cancel, wait delay 100ms", took)
	}
	if res.ExitCode != ExitFailure || res.Err == nil || !strings.Contains(res.Err.Error(), "cut off by cancellation") ||
		!strings.Contains(out.String(), "\nCAPTURE INCOMPLETE: cut off by cancellation: ") {
		t.Errorf("%+v:\n%s", res, out)
	}
}

// A descendant that outlives the wait delay keeps the pipes open; the
// capture is cut off instead of waiting for it.
func TestCancelCutsOffInheritedPipes(t *testing.T) {
	t.Parallel()
	ready := filepath.Join(t.TempDir(), "ready")
	goCmd := shellGo(t, "trap 'exit 0' INT\nsleep 5 &\necho ready > "+escape.ShellQuote(ready)+"\nwhile :; do sleep 0.1; done\n")
	out := &syncBuffer{}
	res, took := cancelWhenReady(t, Config{GoCommand: goCmd, OutParent: t.TempDir(), Stdout: out, waitDelay: 100 * time.Millisecond}, ready)
	expectCutOff(t, res, took, out)
}

// The same with a real go test behind a wrapper that runs go in the background.
func TestIntegrationCancelWrappedGo(t *testing.T) {
	integration(t)
	module := t.TempDir()
	ready := filepath.Join(module, "ready")
	src := fmt.Sprintf(`package hold

import (
	"os"
	"testing"
	"time"
)

func TestHold(t *testing.T) {
	if err := os.WriteFile(%q, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
}
`, ready)
	for name, content := range map[string]string{"go.mod": "module example.com/hold\n\ngo 1.26.0\n", "hold_test.go": src} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goCmd := shellGo(t, "go \"$@\" &\nwait\n")
	out := &syncBuffer{}
	cfg := Config{GoCommand: goCmd, Args: []string{"./..."}, Dir: module, OutParent: t.TempDir(), Stdout: out, waitDelay: 100 * time.Millisecond}
	res, took := cancelWhenReady(t, cfg, ready)
	expectCutOff(t, res, took, out)
}

// Without cancellation, output a descendant writes after the go command
// exited is still read to EOF.
func TestDescendantOutputAfterExitIsCaptured(t *testing.T) {
	t.Parallel()
	late := `{"Action":"output","Package":"p","Output":"late line\n"}`
	goCmd := shellGo(t, "(sleep 0.3; printf '%s\\n' "+escape.ShellQuote(late)+") &\nexit 0\n")
	var out bytes.Buffer
	res := Run(t.Context(), Config{GoCommand: goCmd, OutParent: t.TempDir(), Stdout: &out, waitDelay: 100 * time.Millisecond})
	if res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("%+v:\n%s", res, out.String())
	}
	full, err := os.ReadFile(filepath.Join(res.RunDir, capture.FullFile))
	if err != nil || string(full) != "#1 p ?- late line\n" {
		t.Errorf("full.log = %q, %v", full, err)
	}
}
