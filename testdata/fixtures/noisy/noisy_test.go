package noisy

import (
	"fmt"
	"strings"
	"testing"
)

// Enough output to block the child on a full pipe if the runner stopped reading.
func TestNoisy(t *testing.T) {
	line := strings.Repeat("n", 1000)
	for range 4000 {
		fmt.Println(line)
	}
}
