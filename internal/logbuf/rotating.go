package logbuf

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingFile is a size-based rotating log writer (an io.Writer). When the
// active file would exceed MaxBytes, it is renamed to "<path>.1" (shifting any
// older backups up by one, dropping the oldest beyond MaxBackups) and a fresh
// file is opened. It is safe for concurrent writes.
type RotatingFile struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	f          *os.File
	size       int64
}

// NewRotatingFile opens (creating the directory if needed) a rotating log file
// at path, rolling over at maxBytes and keeping maxBackups rotated files.
func NewRotatingFile(path string, maxBytes int64, maxBackups int) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &RotatingFile{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		f:          f,
		size:       fi.Size(),
	}, nil
}

// Write appends p, rotating first if it would push the file past maxBytes.
func (w *RotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the active file, shifts the backups (path.N → path.N+1, dropping
// the oldest), renames the active file to path.1, and opens a fresh active file.
func (w *RotatingFile) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	// Drop the oldest, then cascade: .(N-1)→.N, …, .1→.2.
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.maxBackups))
	for i := w.maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if w.maxBackups > 0 {
		_ = os.Rename(w.path, w.path+".1")
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.size = 0
	return nil
}

// Close closes the underlying file.
func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
