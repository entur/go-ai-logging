package subtests

import "testing"

func TestParent(t *testing.T) {
	t.Run("ok", func(t *testing.T) {})
	t.Run("bad", func(t *testing.T) { t.Error("sub failure") })
	t.Run("skipped", func(t *testing.T) { t.Skip("not today") })
}

func TestSkipped(t *testing.T) {
	t.Skip("top-level skip")
}
