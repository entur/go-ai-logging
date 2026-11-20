package huge

import (
	"strings"
	"testing"

	"example.com/ailogfixtures/internal/golog"
)

// The message follows a body larger than ailog's 1 MiB line cap.
func TestHuge(t *testing.T) {
	golog.Warn().Str("body", strings.Repeat("x", 1<<20+100)).Msg("CAUSE-ONLY-AT-END")
	golog.Warn().Msg("NEXT-EVENT")
	t.Error("huge failure")
}
