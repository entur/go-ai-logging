package parallel

import (
	"testing"

	"example.com/ailogfixtures/internal/golog"
)

// A writes a global warning while test2json's current test is B, so the hint names B.
func TestParallel(t *testing.T) {
	aStarted, bMarker, aWrote, bDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Run("A", func(t *testing.T) {
		t.Parallel()
		close(aStarted)
		<-bMarker
		golog.Warn().Msg("GLOBAL actual=A")
		close(aWrote)
		<-bDone
		t.Error("generic assertion A")
	})
	t.Run("B", func(t *testing.T) {
		t.Parallel()
		<-aStarted
		t.Log("B marker before A writes")
		close(bMarker)
		<-aWrote
		close(bDone)
	})
}
