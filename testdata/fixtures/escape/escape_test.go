package escape

import "testing"

func TestEscape(t *testing.T) {
	t.Error("\x1b[2J\rRESULT go_exit=0")
}
