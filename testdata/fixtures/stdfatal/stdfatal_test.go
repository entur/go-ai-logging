package stdfatal

import (
	"log"
	"testing"
)

func TestOrdinary(t *testing.T) {
	t.Error("ordinary failure")
}

func TestStdFatal(t *testing.T) {
	log.Fatal("fatal cause: x")
}
