package capture

import (
	"bufio"
	"fmt"
	"io"
	"sync"
)

const writeBufSize = 64 << 10

// Latch keeps the first capture error; once set, every Sink discards.
type Latch struct {
	mu  sync.Mutex
	err error
}

// Set records err unless an earlier error is already recorded.
func (l *Latch) Set(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
	}
}

// Err returns the first recorded error.
func (l *Latch) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Sink is a buffered file writer that never fails its caller: errors are
// latched so that the pipes keep draining and the child can finish.
type Sink struct {
	name    string
	f       io.WriteCloser
	w       *bufio.Writer
	failure *Latch
}

// NewSink buffers writes to f, the run file name, reporting errors to failure.
func NewSink(name string, f io.WriteCloser, failure *Latch) *Sink {
	return &Sink{name: name, f: f, w: bufio.NewWriterSize(f, writeBufSize), failure: failure}
}

// Write always reports success; a failure is latched instead.
func (s *Sink) Write(p []byte) (int, error) {
	s.Tee(p)
	return len(p), nil
}

// Tee writes p unless a capture error has been latched.
func (s *Sink) Tee(p []byte) {
	if s.failure.Err() != nil {
		return
	}
	if _, err := s.w.Write(p); err != nil {
		s.failure.Set(fmt.Errorf("write %s: %w", s.name, err))
	}
}

// Close flushes and closes the file; both errors count as capture failures.
func (s *Sink) Close() {
	if err := s.w.Flush(); err != nil {
		s.failure.Set(fmt.Errorf("write %s: %w", s.name, err))
	}
	if err := s.f.Close(); err != nil {
		s.failure.Set(fmt.Errorf("close %s: %w", s.name, err))
	}
}
