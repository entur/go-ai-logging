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
// not exited 10 seconds later. The go command itself waits for its test
// processes, which get a terminal's Ctrl-C directly; killing it ends the
// capture but does not signal test processes it started.
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
	cmd.WaitDelay = cmp.Or(cfg.waitDelay, waitDelay)
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		var stderr io.ReadCloser
		stderr, err = cmd.StderrPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			return finish(cmd, stdout, stderr, sinks, failure, out, cfg.Dir, res)
		}
	}
	closeSinks(sinks)
	return fail("start %s: %w", goCmd, err)
}

// finish drains both pipes to EOF whatever happens to the files, waits for
// the go command and prints the summary.
func finish(cmd *exec.Cmd, stdout, stderr io.Reader, sinks map[string]*capture.Sink, failure *capture.Latch, out io.Writer, dir string, res Result) Result {
	head := &summary.Head{}
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := stream.Frame(stderr, sinks[capture.StderrFile].Tee, summary.MaxLine, head.Add); err != nil {
			failure.Set(fmt.Errorf("read stderr: %w", err))
			_, _ = io.Copy(io.Discard, stderr) // keep the child from blocking on a full pipe
		}
	})

	c := capture.New(sinks[capture.RawFile], sinks[capture.FullFile], sinks[capture.CompactFile], cwdPrefix(dir))
	if err := c.Consume(stdout); err != nil {
		failure.Set(fmt.Errorf("read stdout: %w", err))
		_, _ = io.Copy(io.Discard, stdout) // keep the child from blocking on a full pipe
	}
	wg.Wait()

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
