# AGENTS.md

Follow [entur/ai](https://github.com/entur/ai) ([CONVENTIONS.md](https://github.com/entur/ai/blob/main/CONVENTIONS.md), [guides/reference/go.md](https://github.com/entur/ai/blob/main/guides/reference/go.md)); the rules below take precedence.

- Module `github.com/entur/go-ai-logging`, root `package ailogging`. v0.x: the API may change before 1.0.
- Go floor 1.26: build, vet and test with `GOTOOLCHAIN=go1.26.0` as well as the current toolchain.
- The root module uses the standard library only: no `require` in `go.mod`. Dev tools live in `tools/`, never in the root `go.mod`.
- No mutable package-level state, no `init`. Constant tables are `const` or returned from functions; sentinel errors (`var ErrX = errors.New(...)`) are the exception.
- The agent-facing output format is `doc/contract.md`; change it in the same pull request as the code.
- Layout: the root package is the small public API (`Config`, `ParseArgs`, `Run`, `Result`, `ExitFailure`); everything else is in `internal/` (`stream` framing and reassembly, `compact` rendering, `summary` state and report, `capture` run files, `escape`); `cmd/ailog/main.go` only parses, runs and exits.
- Test fixtures live in the separate module `testdata/fixtures` with no `require`; tests never use the network.
- Paths in `CODEOWNERS` are never auto-merged.
- Workflows: every `uses:` pinned to a full SHA with a `# vX.Y.Z` comment; `permissions: {}` at the top, job-level grants, `persist-credentials: false`, no caches, no secrets. Run `actionlint` and `zizmor` after changes.
- Public repository: nothing internal to Entur in files, commits or PRs.
- Signed Conventional Commits; run the checks in CONTRIBUTING.md first.

`CLAUDE.md` is a symlink to this file; edit `AGENTS.md`.
