package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	interval time.Duration
	preserve time.Duration
	file     *os.File
	size     int64
	opened   time.Time
}

func New(path string, maxBytes int64, interval, preserve time.Duration) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	r := &RotatingWriter{path: path, maxBytes: maxBytes, interval: interval, preserve: preserve}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if (r.maxBytes > 0 && r.size+int64(len(p)) > r.maxBytes) || (r.interval > 0 && time.Since(r.opened) >= r.interval) {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}
func (r *RotatingWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
func (r *RotatingWriter) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file = f
	r.size = st.Size()
	r.opened = time.Now()
	return nil
}
func (r *RotatingWriter) rotate() error {
	if err := r.file.Sync(); err != nil {
		return err
	}
	if err := r.file.Close(); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	rotated := fmt.Sprintf("%s.%s", r.path, stamp)
	if err := os.Rename(r.path, rotated); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := r.open(); err != nil {
		return err
	}
	r.cleanup()
	return nil
}
func (r *RotatingWriter) cleanup() {
	if r.preserve <= 0 {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(r.path))
	if err != nil {
		return
	}
	prefix := filepath.Base(r.path) + "."
	type item struct {
		name string
		mod  time.Time
	}
	var old []item
	cut := time.Now().Add(-r.preserve)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cut) {
			old = append(old, item{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(old, func(i, j int) bool { return old[i].mod.Before(old[j].mod) })
	for _, v := range old {
		_ = os.Remove(filepath.Join(filepath.Dir(r.path), v.name))
	}
}
