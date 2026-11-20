package flags

import (
	"flag"
	"fmt"
	"testing"
)

// A test-binary flag with the wrapper's own flag name; values after -args must reach it.
var out = flag.String("ailog.out", "", "test binary flag")

func TestFlag(t *testing.T) {
	fmt.Printf("child_out=%s\n", *out)
}

func TestCount(t *testing.T) {}
