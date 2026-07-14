package logging

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// RotatingWriter adds duration-based rotation to lumberjack's size and
// retention policies. File naming, rollover, cleanup, and concurrent writes
// remain owned by lumberjack.
type RotatingWriter struct {
	mu           sync.Mutex
	logger       *lumberjack.Logger
	interval     time.Duration
	lastRotation time.Time
	closed       bool
}

func New(path string, maxBytes int64, interval, preserve time.Duration) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	maxSizeMB := int((maxBytes + 1024*1024 - 1) / (1024 * 1024))
	if maxSizeMB < 1 {
		maxSizeMB = 1
	}
	maxAgeDays := int((preserve + 24*time.Hour - 1) / (24 * time.Hour))
	r := &RotatingWriter{
		logger: &lumberjack.Logger{
			Filename:  path,
			MaxSize:   maxSizeMB,
			MaxAge:    maxAgeDays,
			LocalTime: false,
			Compress:  false,
		},
		interval:     interval,
		lastRotation: time.Now(),
	}
	// Preserve the previous logger's fail-fast behavior: a daemon must learn
	// that its configured log destination is unusable before starting Jellyfin.
	if _, err := r.logger.Write(nil); err != nil {
		_ = r.logger.Close()
		return nil, err
	}
	return r, nil
}

func (r *RotatingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.interval > 0 && time.Since(r.lastRotation) >= r.interval {
		if err := r.logger.Rotate(); err != nil {
			return 0, err
		}
		r.lastRotation = time.Now()
	}
	return r.logger.Write(p)
}

func (r *RotatingWriter) Rotate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return os.ErrClosed
	}
	if err := r.logger.Rotate(); err != nil {
		return err
	}
	r.lastRotation = time.Now()
	return nil
}

func (r *RotatingWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.logger.Close()
}
