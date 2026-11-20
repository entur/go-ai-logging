package excerpt

import "testing"

// The assertion comes after more setup lines than the failure excerpt keeps.
func TestFailureLast(t *testing.T) {
	for i := range 10 {
		t.Logf("setup step %d", i)
	}
	t.Error("ACTUAL-ASSERTION: tenant 17 missing credential")
}
