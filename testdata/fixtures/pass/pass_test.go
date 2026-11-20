package pass

import (
	"testing"

	"example.com/ailogfixtures/internal/golog"
)

func TestPass(t *testing.T) {
	golog.Warn().Str("request_id", "req-1").Msg("warning in a passing test")
}

func TestPassSub(t *testing.T) {
	t.Run("one", func(t *testing.T) {})
	t.Run("two", func(t *testing.T) {})
}
