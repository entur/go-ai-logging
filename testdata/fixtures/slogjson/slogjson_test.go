package slogjson

import (
	"log/slog"
	"os"
	"testing"
)

// A process-wide logger: test2json attributes its lines to the running test.
func TestSlogGlobal(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true}))
	logger.Warn("upstream rejected credential S", "request_id", "req-s", "attempt", 2)
	t.Error("generic assertion S")
}

// A per-test logger on t.Output(): its lines belong to this test exactly.
func TestSlogOutput(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(t.Output(), nil))
	logger.Error("exact attribution", "tenant", 17)
	t.Error("generic assertion T")
}
