// Package databasemonitor observes Jellyfin's already-captured console stream
// for high-confidence SQLite corruption messages. It never opens the live
// database file.
package databasemonitor

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxBufferedLine = 64 * 1024

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type Evidence struct {
	DetectedAt time.Time
	Message    string
}

type Detector struct {
	mu       sync.Mutex
	partial  []byte
	evidence Evidence
}

func (d *Detector) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partial = append(d.partial, p...)
	for {
		newline := bytes.IndexByte(d.partial, '\n')
		if newline < 0 {
			if len(d.partial) > maxBufferedLine {
				d.observeLine(d.partial[:maxBufferedLine])
				d.partial = append(d.partial[:0], d.partial[maxBufferedLine:]...)
			}
			break
		}
		d.observeLine(d.partial[:newline])
		d.partial = append(d.partial[:0], d.partial[newline+1:]...)
	}
	return len(p), nil
}

func (d *Detector) observeLine(raw []byte) {
	line := strings.TrimSpace(ansiEscape.ReplaceAllString(string(raw), ""))
	lower := strings.ToLower(line)
	if !isCorruptionSignature(lower) {
		return
	}
	if len(line) > 2048 {
		line = line[:2048]
	}
	d.evidence = Evidence{DetectedAt: time.Now(), Message: line}
}

func isCorruptionSignature(line string) bool {
	return strings.Contains(line, "database disk image is malformed") ||
		strings.Contains(line, "database corruption at line") ||
		strings.Contains(line, "malformed database schema") ||
		strings.Contains(line, "sqlite error 11:") ||
		strings.Contains(line, "sqlite error 26:") && strings.Contains(line, "file is not a database")
}

func (d *Detector) Candidate(window time.Duration) (Evidence, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.evidence.DetectedAt.IsZero() || window <= 0 || time.Since(d.evidence.DetectedAt) > window {
		return Evidence{}, false
	}
	return d.evidence, true
}

func (d *Detector) Reset() {
	d.mu.Lock()
	d.partial = nil
	d.evidence = Evidence{}
	d.mu.Unlock()
}
