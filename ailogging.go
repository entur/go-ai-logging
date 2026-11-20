package ailogging

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/entur/go-ai-logging/internal/capture"
	"github.com/entur/go-ai-logging/internal/escape"
	"github.com/entur/go-ai-logging/internal/stream"
	"github.com/entur/go-ai-logging/internal/summary"
)

// ExitFailure is the exit code when the capture is incomplete or the go
// command could not run.
const ExitFailure = 2

const (
	outFlag  = "-ailog.out"
	filePerm = 0o600
	// waitDelay is how long the go command gets after ctx is done before it
	// is killed.
	waitDelay = 10 * time.Second
	// killDrain is how long the capture keeps reading after the go command
	// was killed, for the EOF that follows when no descendant holds a pipe.
	killDrain = 250 * time.Millisecond
)

// Config describes one run. The zero value runs `go test -json -count=1` in
// the current directory with a run directory under [os.TempDir] and the
// summary on [os.Stdout].
type Config struct {
	// Args are the go test arguments, forwarded verbatim and in order after
	// "test -json -count=1"; later flags such as -count=2 override.
	Args []string
	// OutParent is the directory in which the run directory is created.
	// Empty means os.TempDir.
	OutParent string
	// Dir is the working directory of the go command. Empty means the
	// current directory.
	Dir string
	// GoCommand is the go command, looked up in PATH. Empty means "go".
	GoCommand string
	// Stdout receives the summary. Nil means os.Stdout.
	Stdout io.Writer

	create    func(dir, name string) (io.WriteCloser, error) // tests replace it
	waitDelay time.Duration                                  // tests shorten it
}

// Result is the outcome of [Run].
type Result struct {
	// ExitCode is the code the process should exit with: GoExit, or
	// ExitFailure when the capture is incomplete or the go command could not
	// run.
	ExitCode int
	// GoExit is the go command's exit code; -1 if it did not start or was
	// stopped by a signal.
	GoExit int
	// RunDir is the run directory, or "" if it could not be created.
	RunDir string
	// Err is why the capture is incomplete; nil after a complete capture,
	// whatever the test outcome.
	Err error
}

// ParseArgs consumes -ailog.out=PARENT (or -ailog.out PARENT) before the
// first -args and keeps every other argument, in order, as Config.Args.
func ParseArgs(args []string) (Config, error) {
	var cfg Config
	rest := make([]string, 0, len(args))
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "-args":
			cfg.Args = append(append(rest, arg), args...)
			return cfg, nil
		case strings.HasPrefix(arg, outFlag+"="):
			cfg.OutParent = strings.TrimPrefix(arg, outFlag+"=")
		case arg == outFlag:
			if len(args) == 0 {
				return Config{}, fmt.Errorf("%s needs a directory", outFlag)
			}
			cfg.OutParent, args = args[0], args[1:]
		default:
			rest = append(rest, arg)
		}
	}
	cfg.Args = rest
	return cfg, nil
}

// Run creates a private run directory, runs `go test -json -count=1
// cfg.Args...`, writes the run files and prints the summary to cfg.Stdout.
// When ctx is done, the go command is sent an interrupt and killed if it has
// not exited 10 seconds later. A quarter of a second after that the capture
// stops reading, even if a process the go command started still holds its
// stdout or stderr: Result.Err then says the capture was cut off by cancellation and
// ExitCode is ExitFailure. Processes the go command started are not
// signalled; a terminal's Ctrl-C reaches them directly.
func Run(ctx context.Context, cfg Config) Result {
	out := cmp.Or[io.Writer](cfg.Stdout, os.Stdout)
	create := cfg.create
	if create == nil {
		create = createFile
	}
	res := Result{ExitCode: ExitFailure, GoExit: -1}
	fail := func(format string, args ...any) Result {
		res.Err = fmt.Errorf(format, args...)
		say(out, "AILOG ERROR: %v", res.Err)
		return res
	}

	runDir, err := os.MkdirTemp(cfg.OutParent, "ailog-")
	if err != nil {
		return fail("create run dir: %w", err)
	}
	res.RunDir = runDir
	goCmd := cmp.Or(cfg.GoCommand, "go")
	argv := append([]string{"test", "-json", "-count=1"}, cfg.Args...)
	say(out, "AILOG run=%s", runDir)
	say(out, "cmd: %s", escape.ShellJoin(append([]string{goCmd}, argv...)))

	failure := &capture.Latch{}
	sinks := map[string]*capture.Sink{}
	for _, name := range []string{capture.RawFile, capture.StderrFile, capture.FullFile, capture.CompactFile} {
		f, err := create(runDir, name)
		if err != nil {
			closeSinks(sinks)
			return fail("create %s: %w", name, err)
		}
		sinks[name] = capture.NewSink(name, f, failure)
	}

	cmd := exec.CommandContext(ctx, goCmd, argv...) //nolint:gosec // running the go command with the caller's test arguments is the purpose
	cmd.Dir = cfg.Dir
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	grace := cmp.Or(cfg.waitDelay, waitDelay)
	cmd.WaitDelay = grace
	p, err := startPiped(cmd)
	if err != nil {
		closeSinks(sinks)
		return fail("start %s: %w", goCmd, err)
	}
	cut, stopWatch := p.cutAfter(ctx, grace)
	readErr := func(name string, r io.Reader, err error) {
		if cut.Load() && (errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, os.ErrClosed)) {
			failure.Set(fmt.Errorf("cut off by cancellation: %s still open %v after ctx was done", name, grace+killDrain))
			return
		}
		failure.Set(fmt.Errorf("read %s: %w", name, err))
		_, _ = io.Copy(io.Discard, r) // keep the child from blocking on a full pipe
	}

	// Read both pipes to EOF whatever happens to the files; only then wait.
	head := &summary.Head{}
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := stream.Frame(p.stderr, sinks[capture.StderrFile].Tee, summary.MaxLine, head.Add); err != nil {
			readErr("stderr", p.stderr, err)
		}
	})
	c := capture.New(sinks[capture.RawFile], sinks[capture.FullFile], sinks[capture.CompactFile], cwdPrefix(cfg.Dir))
	if err := c.Consume(p.stdout); err != nil {
		readErr("stdout", p.stdout, err)
	}
	wg.Wait()
	stopWatch()
	p.close()
	return finish(cmd, c, head, sinks, failure, out, res)
}

// finish waits for the go command after its output is read and prints the
// summary.
func finish(cmd *exec.Cmd, c *capture.Capture, head *summary.Head, sinks map[string]*capture.Sink, failure *capture.Latch, out io.Writer, res Result) Result {
	res.GoExit = 0
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			failure.Set(fmt.Errorf("wait for go: %w", err))
		}
		res.GoExit = cmd.ProcessState.ExitCode()
	}
	c.Finish()
	closeSinks(sinks)

	res.Err = failure.Err()
	writeOut(out, c.Report(summary.Outcome{
		GoExit:     res.GoExit,
		CaptureErr: res.Err,
		Stderr:     head,
		Files: summary.Files{
			Full:    filepath.Join(res.RunDir, capture.FullFile),
			Compact: filepath.Join(res.RunDir, capture.CompactFile),
			Raw:     filepath.Join(res.RunDir, capture.RawFile),
			Stderr:  filepath.Join(res.RunDir, capture.StderrFile),
		},
	}))
	res.ExitCode = res.GoExit
	if res.Err != nil || res.GoExit < 0 {
		res.ExitCode = ExitFailure
	}
	return res
}

// childPipes are the parent's read ends of the go command's stdout and
// stderr. Its write ends are *os.File, so exec starts no copying goroutines,
// and the parent closes its copies after Start: EOF then means every process
// holding a write end has closed it.
type childPipes struct {
	stdout, stderr *os.File
}

func startPiped(cmd *exec.Cmd) (childPipes, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return childPipes{}, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeFiles(outR, outW)
		return childPipes{}, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	err = cmd.Start()
	closeFiles(outW, errW) // the child has its own copies
	if err != nil {
		closeFiles(outR, errR)
		return childPipes{}, err
	}
	return childPipes{stdout: outR, stderr: errR}, nil
}

// cutAfter ends both reads once grace (when the go command is killed) and
// killDrain have elapsed after ctx is done, even if a descendant of the go
// command still holds a write end. cut reports that the deadline was set;
// stop ends the watch and returns after it has.
func (p childPipes) cutAfter(ctx context.Context, grace time.Duration) (cut *atomic.Bool, stop func()) {
	cut = &atomic.Bool{}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-done:
			return
		}
		cut.Store(true)
		deadline := time.Now().Add(killDrain)
		for _, f := range []*os.File{p.stdout, p.stderr} {
			if f.SetReadDeadline(deadline) != nil {
				_ = f.Close() // pipes without deadline support: closing ends the read
			}
		}
	})
	return cut, func() {
		close(done)
		wg.Wait()
	}
}

func (p childPipes) close() {
	closeFiles(p.stdout, p.stderr)
}

// closeFiles closes pipe ends; their close errors carry no data.
func closeFiles(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// createFile creates one of the fixed file names in the new private run dir;
// O_EXCL refuses existing files and symlinks.
func createFile(dir, name string) (io.WriteCloser, error) {
	return os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm) //nolint:gosec // fixed names inside a directory just created
}

func closeSinks(sinks map[string]*capture.Sink) {
	for _, s := range sinks {
		s.Close()
	}
}

// cwdPrefix is the go command's absolute working directory with a trailing
// slash, or "" if it cannot be determined.
func cwdPrefix(dir string) string {
	abs, err := filepath.Abs(cmp.Or(dir, "."))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(filepath.ToSlash(abs), "/") + "/"
}

// say prints one line with control characters escaped, so paths and errors
// cannot forge output.
func say(w io.Writer, format string, args ...any) {
	writeOut(w, escape.Text(fmt.Sprintf(format, args...))+"\n")
}

// writeOut writes already escaped text.
func writeOut(w io.Writer, s string) {
	_, _ = io.WriteString(w, s) // nothing useful to do if our own stdout is gone
}
