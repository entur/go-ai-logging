package timeout

import (
	"testing"
	"time"
)

func TestOrdinary(t *testing.T) {
	t.Error("ordinary failure")
}

func TestSlow(t *testing.T) {
	t.Run("inner", func(t *testing.T) { time.Sleep(time.Minute) })
}
