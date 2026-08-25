package tools

import (
	"bytes"
	"sync"
)

// captureWriter bounds combined stdout and stderr from a child process. Once
// the limit is crossed it keeps only the bounded prefix and terminates the
// process, preventing an unbounded pipe/buffer allocation.
type captureWriter struct {
	mu     sync.Mutex
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	limit  int
	used   int
	over   bool
	kill   func()
	killed bool
}

func newCaptureWriter(stdout, stderr *bytes.Buffer, limit int, kill func()) *captureWriter {
	return &captureWriter{stdout: stdout, stderr: stderr, limit: limit, kill: kill}
}

func (w *captureWriter) stdoutWriter() *captureStream { return &captureStream{owner: w, dst: w.stdout} }
func (w *captureWriter) stderrWriter() *captureStream { return &captureStream{owner: w, dst: w.stderr} }
func (w *captureWriter) exceeded() bool               { w.mu.Lock(); defer w.mu.Unlock(); return w.over }

type captureStream struct {
	owner *captureWriter
	dst   *bytes.Buffer
}

func (s *captureStream) Write(p []byte) (int, error) {
	w := s.owner
	w.mu.Lock()
	remaining := w.limit - w.used
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
		}
		_, _ = s.dst.Write(p[:n])
		w.used += n
	}
	if len(p) > remaining {
		w.over = true
		if !w.killed {
			w.killed = true
			kill := w.kill
			w.mu.Unlock()
			if kill != nil {
				kill()
			}
			return len(p), nil
		}
	}
	w.mu.Unlock()
	return len(p), nil
}
