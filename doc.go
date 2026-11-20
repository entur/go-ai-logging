// Package ailogging runs `go test -json` for AI coding agents: it keeps the
// complete output in a private run directory and prints a short, bounded
// summary that points into it.
//
// The command github.com/entur/go-ai-logging/cmd/ailog is the usual entry
// point; [ParseArgs] and [Run] are what it calls. The output format, run
// files and exit codes are specified in doc/contract.md.
package ailogging
