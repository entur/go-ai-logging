package framework

import (
	"fmt"
	"os"
	"testing"
)

// Application text that merely looks like a test2json frame marker.
func TestConfig(t *testing.T) {
	fmt.Fprintln(os.Stderr, "=== CONFIG: missing service credential")
	t.Error("generic assertion")
}
