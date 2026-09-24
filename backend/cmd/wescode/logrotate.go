package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultMaxBytes   = 10 * 1024 * 1024 // 10 MB per file
	defaultMaxBackups = 5                // keep 5 rotated files
)

// rotatingWriter is an io.Writer that rotates the underlying file when it
// exceeds maxBytes. It keeps up to maxBackups historical files named
// <base>.1, <base>.2, ... (higher number = older).
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

func newRotatingWriter(path string) (*rotatingWriter, error) {
	w := &rotatingWriter{
		path:       path,
		maxBytes:   defaultMaxBytes,
		maxBackups: defaultMaxBackups,
	}
	if err := w.openExisting(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) openExisting() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err = w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
	}

	// Shift existing backups: .4 → .5, .3 → .4, ... , .1 → .2, current → .1
	for i := w.maxBackups; i >= 1; i-- {
		src := w.backupName(i - 1)
		dst := w.backupName(i)
		if i == 1 {
			src = w.path
		}
		_ = os.Remove(dst)
		_ = os.Rename(src, dst)
	}

	// Open fresh file
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rotatingWriter) backupName(index int) string {
	ext := filepath.Ext(w.path)
	base := w.path[:len(w.path)-len(ext)]
	return fmt.Sprintf("%s.%d%s", base, index, ext)
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
