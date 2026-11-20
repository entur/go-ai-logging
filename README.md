# go-ai-logging

`ailog` runs `go test` for AI coding agents. It keeps the complete test output on disk and prints a short, bounded summary: the result, the failures with their assertions, and the warnings and errors logged around them, each with a line ID that points into the files. The agent reads the summary instead of thousands of lines of test output and drills down only where it needs to. Standard library only; it reads what tests already write and changes nothing in your logging.

The output format, run files and exit codes are specified in [doc/contract.md](doc/contract.md). It is inspired by Entur's JVM logging library [cloud-logging](https://github.com/entur/cloud-logging).

**Status:** v0.x. The output and API may still change before 1.0.

## Install and run

```sh
go get -tool github.com/entur/go-ai-logging/cmd/ailog # Go 1.26 or later
go tool ailog ./...
```

`ailog [-ailog.out=PARENT] [go test args...]` runs `go test -json -count=1 ARGS...`; your own flags come later and win (`-count=2`, `-run`, `-race`). `-ailog.out` is read only before `-args`. The exit code is `go test`'s, or `2` if `ailog` itself failed.

```text
AILOG run=/tmp/ailog-4051631161
cmd: go test -json -count=1 ./warnfail
RESULT go_exit=1 packages: ok=0 fail=1 build_fail=0 no_tests=0 cached=0
TESTS top: pass=0 fail=2 skip=0 incomplete=0  subtests: pass=0 fail=0 skip=0
FAIL example.com/ailogfixtures/warnfail TestWarnA (#1..#4)
  #3     warnfail_test.go:12: generic assertion A
  #2 WRN example.com/ailogfixtures/warnfail ?TestWarnA at=warnfail/warnfail_test.go:11 msg="upstream rejected credential A" request_id="req-a"
...
drill down: grep -n '^#<id> ' /tmp/ailog-4051631161/full.log
```

## AGENTS.md / CLAUDE.md snippet

```text
Run Go tests with `go tool ailog ./...` (go test flags work as usual); read the whole summary, do not pipe it through head/tail.
`?TestName` on a log line is only a hint: output of global loggers cannot be attributed to a test reliably; check the CANDIDATES lines too.
Details: `grep -n '^#<id> ' <full.log>` for a line ID from the summary, or search the printed compact.log.
```

## Files

Every run gets a new private directory (`AILOG run=<dir>`, under `-ailog.out` or the system temp directory):

| File | Content |
|---|---|
| `events.jsonl` | `go test -json` stdout, byte for byte |
| `go.stderr` | `go test` stderr |
| `full.log` | every reassembled output line as `#N <package> ?<test hint> <line>` |
| `compact.log` | the same lines, JSON log events rendered as `#N WRN pkg ?Test at=file.go:12 msg="..." k=v`, adjacent repeats collapsed |

JSON log lines in the shapes of [go-logging](https://github.com/entur/go-logging) (zerolog: `severity`, `message`, `caller`, `error`, `stack`) and `log/slog`'s `JSONHandler` (`level`, `msg`, `source`) are recognised. The files are not size-limited; a noisy suite can use a lot of disk.

## Exact per-test output

Go reports output from process-wide loggers, background goroutines and parallel tests under whichever test printed last, so the `?Test` hint can name the wrong test, even a passing one. For exact attribution, give the code under test a logger that writes to `t.Output()`:

```go
logger := logging.New(logging.WithWriter(t.Output()))   // go-logging
logger := slog.New(slog.NewJSONHandler(t.Output(), nil)) // log/slog
```

## Use from Go

`ailogging.ParseArgs` and `ailogging.Run` are what the command calls; see [example_test.go](example_test.go).

Security reports: [SECURITY.md](SECURITY.md). Contributing: [CONTRIBUTING.md](CONTRIBUTING.md).

License: [EUPL-1.2](LICENSE.txt)
