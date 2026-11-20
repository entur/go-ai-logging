package stream

import (
	"bytes"
	"strings"
	"testing"
)

type record struct {
	text string
	cut  bool
}

func frame(t *testing.T, input string, limit int) []record {
	t.Helper()
	var tee bytes.Buffer
	var got []record
	err := Frame(strings.NewReader(input), func(p []byte) { tee.Write(p) }, limit, func(rec []byte, cut bool) {
		got = append(got, record{string(rec), cut})
	})
	if err != nil {
		t.Fatal(err)
	}
	if tee.String() != input {
		t.Fatal("tee differs from input")
	}
	return got
}

func TestFrameBoundaries(t *testing.T) {
	long := strings.Repeat("x", 3*readBufSize)
	tests := []struct {
		name, input string
		limit       int
		want        []record
	}{
		{"empty input", "", 10, nil},
		{"empty lines", "\n\n", 10, []record{{"", false}, {"", false}}},
		{"unterminated final record", "a\nb", 10, []record{{"a", false}, {"b", false}}},
		{"exactly the limit", "abcd\n", 4, []record{{"abcd", false}}},
		{"one over the limit", "abcde\nnext\n", 4, []record{{"abcd", true}, {"next", false}}},
		{"longer than the read buffer", long + "\nnext\n", len(long), []record{{long, false}, {"next", false}}},
		{"cut across buffer refills", long + "\nnext", 10, []record{{long[:10], true}, {"next", false}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frame(t, tt.input, tt.limit)
			if len(got) != len(tt.want) {
				t.Fatalf("%d records, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("record %d = %.20q cut=%v, want %.20q cut=%v", i, got[i].text, got[i].cut, tt.want[i].text, tt.want[i].cut)
				}
			}
		})
	}
}
