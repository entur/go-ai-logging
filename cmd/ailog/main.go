// Command ailog runs `go test -json`, keeps the complete output in a private
// run directory and prints a bounded summary that points into it.
//
// Usage: ailog [-ailog.out=PARENT] [go test args...]
//
// See doc/contract.md in github.com/entur/go-ai-logging for the output.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	ailogging "github.com/entur/go-ai-logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := ailogging.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Printf("AILOG ERROR: %v\n", err)
		return ailogging.ExitFailure
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ailogging.Run(ctx, cfg).ExitCode
}
