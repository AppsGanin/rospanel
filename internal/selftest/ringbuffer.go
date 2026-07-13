package selftest

import "sync"

// ringBuffer is an io.Writer that keeps only the last n bytes written. Xray's
// stderr during a probe is short, but a misconfig can make it loop; capping the
// buffer means we always have the tail (where the real error is) without letting a
// runaway child grow memory without bound.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		r.data = r.data[len(r.data)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.data)
}
