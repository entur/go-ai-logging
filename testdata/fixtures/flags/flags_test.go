package flags

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// A test-binary flag with the wrapper's own flag name; values after -args must reach it.
func TestMain(m *testing.M) {
	flag.String("ailog.out", "", "test binary flag")
	flag.Parse()
	os.Exit(m.Run())
}

func TestFlag(t *testing.T) {
	fmt.Printf("child_out=%s\n", flag.Lookup("ailog.out").Value.String())
}

func TestCount(t *testing.T) {}
