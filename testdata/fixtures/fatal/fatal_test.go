package fatal

import (
	"testing"

	"example.com/ailogfixtures/internal/golog"
)

func TestFatal(t *testing.T) {
	golog.Fatal().Msg("fatal cause")
}
