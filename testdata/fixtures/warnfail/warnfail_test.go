package warnfail

import (
	"errors"
	"testing"

	"example.com/ailogfixtures/internal/golog"
)

func TestWarnA(t *testing.T) {
	golog.Warn().Str("request_id", "req-a").Msg("upstream rejected credential A")
	t.Error("generic assertion A")
}

func TestWarnB(t *testing.T) {
	golog.Warn().Str("request_id", "req-b").Err(errors.New("token expired")).Msg("upstream rejected credential B")
	t.Error("generic assertion B")
}
