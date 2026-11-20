# Output contract

What `ailog` (and `ailogging.Run`) shows to the agent or person reading it: the command line, exit codes, run files and the line formats of `full.log`, `compact.log` and the summary. "Must" is normative. The Go API is documented in the package; this page is what tooling that reads the output may rely on. It changes in the same pull request as the code; before 1.0 it may change in a minor release.

## Command line

```text
ailog [-ailog.out=PARENT | -ailog.out PARENT] [go test arguments...]
```

- `-ailog.out` is consumed only before the first `-args`; the last one wins. Everything else, including `-args` and all that follows it, is forwarded verbatim and in order.
- The child is `go test -json -count=1 ARGS...`, started without a shell, with `go` from `PATH` and the caller's environment and working directory. Later arguments override the defaults (`-count=2`, `-json=false`). `GOFLAGS` is not parsed and no flag is reserved.
- An interrupt or `SIGTERM` does not stop `ailog`: it sends an interrupt to `go`, keeps reading, and kills `go` 10 seconds later if it is still running. A terminal's Ctrl-C reaches the test processes directly; `ailog` does not signal them. Reading stops 0.25 seconds after the kill even if a process `go` started still holds stdout or stderr; the capture is then incomplete (`CAPTURE INCOMPLETE: cut off by cancellation: ...`).

## Exit codes

| Code | Meaning |
|---|---|
| `go test`'s code | The capture is complete. `0` passed, `1` failures; `go test` itself may also exit `2`. |
| `2` | `ailog` failed: a bad `-ailog.out`, the run directory or a file could not be created, `go` could not start or was killed by a signal (`go_exit=-1`), or the capture is incomplete (`CAPTURE INCOMPLETE`). |

Exit code `2` is therefore ambiguous on its own; an `AILOG ERROR:` or `CAPTURE INCOMPLETE:` line says that `ailog` failed.

## Run directory and files

Each run creates a new directory `ailog-*` under `PARENT` (default: the system temp directory) with mode `0700`. Its four files are created exclusively with mode `0600` and are not size-limited:

| File | Content |
|---|---|
| `events.jsonl` | The child's stdout, byte for byte. |
| `go.stderr` | The child's stderr, byte for byte. |
| `full.log` | Every logical output line, numbered. |
| `compact.log` | One line per logical line, structured log events rendered compactly, adjacent repeats collapsed. |

After a write, read or close error the capture is incomplete: `ailog` keeps draining both pipes so the child can finish, waits for it, prints `CAPTURE INCOMPLETE: <error>` and exits `2`. The files are then not complete and must not be read as if they were.

## Logical lines and IDs

`go test -json` splits output into fragments. `ailog` reassembles the `Output` of each package (or, for build output, each `ImportPath`) into logical lines ending at a newline. Each completed line gets the next ID `#N`, starting at 1. IDs follow completion order: within one package they are chronological, across packages they are not. The same `#N` names the same line in `full.log`, `compact.log` and the summary.

The *test hint* `?Test` is the `Test` field of the event that completed the line, or `-` for none. It is a hint only (see [Attribution](#attribution)).

Limits on reconstruction:

- A stdout record (one test2json event) longer than 1 MiB, or one that is not a test2json event, is not decoded. Its stream is unknown, so every pending line of every package ends there as a *gap*; each stream discards its continuation up to its next newline. The record itself becomes a line of package `-`.
- A logical line longer than 1 MiB keeps its first 1 MiB and is marked *truncated*; the rest, up to its newline, is discarded.
- A line still pending at its package's terminal event or at EOF is flushed with `[no newline]`.

Gap and truncated lines are never parsed as JSON.

## `full.log`

```text
#N PACKAGE ?HINT TEXT[MARKS]
```

- `PACKAGE` is the test2json `Package`, the `ImportPath` for build output, or `-` for a stdout record that is not test2json.
- `TEXT` is the line without its newline, otherwise byte for byte (control characters included).
- `MARKS`, in this order when combined:
  - ` [unparsed record, raw_records=R:R]` or ` [unparsed record, truncated, raw_records=R:R]` for an undecoded stdout record;
  - ` [gap, raw_records=S:E]` or ` [truncated, raw_records=S:E]`;
  - ` [no newline]`.

`raw_records=S:E` are 1-based line numbers in `events.jsonl`: `sed -n 'S,Ep' events.jsonl` prints the records the line came from.

## `compact.log`

Three kinds of lines, each at most 4096 characters (runes); a longer line keeps its start and ends with ` …[truncated, N chars]`.

**Event lines.** A logical line is an event if, after trimming whitespace, it is a JSON object with a string `severity` or `level` and a `message` or `msg` (the first of each pair wins):

```text
#N SEV PACKAGE ?HINT[ at=LOCATION] msg=VALUE[ error=VALUE][ stack=VALUE][ KEY=VALUE...]
```

- `SEV`, case-insensitively: `trace` `TRC`, `debug` `DBG`, `info` `INF`, `warning`/`warn` `WRN`, `error` `ERR`, `fatal` `FTL`, `panic` `PNC`; any other value uppercased (`notice` `NOTICE`, slog's `WARN+2` `WARN+2`).
- `timestamp` and `time` are dropped.
- `at=` is go-logging's `caller` string, or else slog's `source` object as `file:line` when it has both. The working directory of `go` plus `/` is stripped from the start of a path, at that separator only.
- `stack` in go-logging's shape `[][]{file,function,line}` renders the first 2 frames of the first 2 groups: `"fn file:line > fn file:line [+K frames] | ... [+G groups]"`. Any other shape is a plain value.
- The remaining fields follow in key order. A value is a Go-quoted string for a JSON string, otherwise minified JSON with numbers exactly as written and `null` as `null`. A key is quoted when it is empty, is not valid UTF-8 or contains a space, `"`, `=` or a non-printable character.

**Text lines.** Every other logical line:

```text
#N TXT PACKAGE ?HINT[MARKS] "GO-QUOTED TEXT"
```

Lines printed by the testing framework itself are left out of `compact.log` (they stay in `full.log`): lines starting, after leading spaces, with `=== RUN `, `=== PAUSE `, `=== CONT `, `=== NAME `, `--- PASS: `, `--- FAIL: ` or `--- SKIP: `; lines starting with `ok  \t`, `FAIL\t` or `?   \t`; and lines that are exactly `PASS` or `FAIL`. Anything else, such as `=== CONFIG: ...`, is application text.

**Repeat lines.** Consecutive events of one stream that are equal in every field except `timestamp`/`time` (compared on the complete JSON, before truncation) and have the same hint are written once, followed, when the run ends, by:

```text
#FIRST..#LAST repeated K more times (K+1 total)
```

## Summary (stdout)

Before the child starts:

```text
AILOG run=DIR
cmd: go test -json -count=1 ARGS...
```

The command line is POSIX-shell-quoted. A failure before or while starting the child prints `AILOG ERROR: <error>` and nothing else follows.

After the child exits, in this order:

1. `RESULT go_exit=N packages: ok=A fail=B build_fail=C no_tests=D cached=E`
2. `CAPTURE INCOMPLETE: <error>`, only if the capture is incomplete.
3. One of:
   - `TESTS top: pass=P fail=F skip=S incomplete=I  subtests: pass=P fail=F skip=S`
   - `TESTS none observed (counts unavailable)` when test2json events arrived but no named test ran;
   - `TESTS none observed (counts unavailable: no test2json events)`, for example with `-json=false`.
4. Diagnostics, in this section order:
   1. `STDERR N lines` and the first 10 lines of `go.stderr`, without IDs (stderr lines are not logical lines).
   2. `OUTPUT TAIL (no test2json events)` and the last 20 text lines, only without test2json events and with a non-zero `go_exit`.
   3. Per build failure: `BUILD FAIL IMPORTPATH` and its build output lines (30 at most over all builds).
   4. Per failed test, top-level or subtest, 5 at most: `FAIL PACKAGE TEST (#FIRST..#LAST)`; then `  (+K earlier lines, see full.log)` when lines were dropped, the last 8 text lines of the test (assertions, panics), and up to 8 `WRN`/`ERR`/`FTL`/`PNC` events with its hint. Then `... N more failed tests not shown (grep -e '--- FAIL' full.log)`.
   5. Per failing package (a failed test, an incomplete test, or a package failure without a failed test): `CANDIDATES PACKAGE (test hints are uncertain; includes passing tests)` and up to 8 distinct `WRN`/`ERR`/`FTL`/`PNC` events of the whole package, whatever their hint, as `  #N total=K COMPACT-EVENT`. An event already printed in a FAIL block is `  #N total=K ?HINT (shown above)`. Then `  (no WRN/ERR/FTL/PNC events)` or `  ... N more WRN/ERR/FTL/PNC events in compact.log` where they apply. A package with incomplete tests or a failure without a failed test adds `CRASH PACKAGE headline: LINE` and its last 20 text lines. The headline is the first line starting with `panic:` or `fatal error:` or containing `test timed out`, else the last non-framework line of the package (`exit status` lines skipped), else `(none)`.
   6. `incomplete: PACKAGE TEST` per test that started and never finished (`log.Fatal`, `os.Exit`, a panic, a timeout).
5. Footer:

   ```text
   full: PATH
   compact: PATH
   raw: PATH
   stderr: PATH
   drill down: grep -n '^#<id> ' QUOTED-FULL-PATH
   incomplete lines: N (truncated or gap); raw records: sed -n 'S,Ep' QUOTED-RAW-PATH
   ```

   The last line appears only if a line was truncated or had a gap; `S,E` locates the first one. The paths in the commands are POSIX-shell-quoted.

Diagnostic body lines start with the logical line ID, `#N`, so that an agent can look it up with the drill-down command. The exceptions are the STDERR excerpt, which comes from `go.stderr`, and the notices (`(+K earlier lines ...)`, `(no WRN/ERR/FTL/PNC events)`, `... N more ...`).

**Budget.** Diagnostics are at most 60 lines, followed by `... N more lines omitted (see compact.log)` when lines were left out. Essential lines are kept first, in section order: `BUILD FAIL` headers, `FAIL` headers, each `CANDIDATES` header with its first candidate, `CRASH` headlines and `incomplete:` lines. Other lines fill the rest of the budget in section order. Kept lines are printed in section order.

**Escaping.** Every summary line, including paths, errors and the command line, has control characters, other non-printable characters and invalid UTF-8 escaped as in a Go string literal (`\x1b`, `\r`, `\n`, ` `, `\xff`). Output of a test can therefore never forge a summary line. Diagnostic lines are clipped to 400 characters with ` …[truncated, N chars]`, the ID first. The `AILOG run=`, `cmd:` and footer lines are never clipped, so their paths stay usable.

## Counting

- Each terminal test event (`pass`, `fail`, `skip` with a `Test`) counts once; a test run twice (`-count=2`) counts twice. Names with `/` are subtests.
- `incomplete` counts top-level tests that started and had no terminal event when their package ended or at EOF. Incomplete subtests are listed but not counted.
- Packages: `ok` passed, `fail` failed, `build_fail` failed to build (`FailedBuild`), `no_tests` skipped (no test files), `cached` printed `(cached)` on its `ok` line.

## Attribution

Go attributes a line of output to whichever test test2json saw last in that package. Output from process-wide loggers, background goroutines and parallel tests can therefore carry the hint of another test, including a passing one. That is why CANDIDATES lists warnings from the whole failing package. For exact attribution, give the code under test a logger that writes to `t.Output()`; its lines carry the test's own name.
