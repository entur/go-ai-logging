package stream

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

const readBufSize = 64 << 10

// Frame passes every byte of r to tee and each newline-terminated record (or
// the final unterminated one), without its newline and bounded to limit
// bytes, to emit; cut reports that the record was longer than limit.
// The record slice is reused after emit returns.
func Frame(r io.Reader, tee func([]byte), limit int, emit func(rec []byte, cut bool)) error {
	br := bufio.NewReaderSize(r, readBufSize)
	rec := make([]byte, 0, min(limit, readBufSize))
	pending, cut := false, false
	for {
		chunk, err := br.ReadSlice('\n')
		tee(chunk)
		pending = pending || len(chunk) > 0
		chunk = bytes.TrimSuffix(chunk, []byte("\n"))
		if room := limit - len(rec); len(chunk) > room {
			cut = true
			chunk = chunk[:room]
		}
		rec = append(rec, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if pending {
			emit(rec, cut)
		}
		rec, pending, cut = rec[:0], false, false
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
