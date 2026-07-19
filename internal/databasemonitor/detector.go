// Package databasemonitor observes Jellyfin's already-captured console stream
// for high-confidence SQLite corruption messages. It never opens the live
// database file.
package databasemonitor

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"time"
)

const maxBufferedLine = 64 * 1024
const lineOverlap = 256

type Evidence struct {
	DetectedAt time.Time
	Message    string
}

type Detector struct {
	mu       sync.Mutex
	partial  []byte
	evidence Evidence
}

type ConsoleWriter struct {
	output   io.Writer
	detector *Detector
}

func NewConsoleWriter(output io.Writer, detector *Detector) *ConsoleWriter {
	return &ConsoleWriter{output: output, detector: detector}
}

func (w *ConsoleWriter) Write(p []byte) (int, error) {
	return io.MultiWriter(w.output, w.detector).Write(p)
}

func (w *ConsoleWriter) Flush() { w.detector.Flush() }

func (d *Detector) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partial = append(d.partial, p...)
	for {
		newline := bytes.IndexByte(d.partial, '\n')
		if newline < 0 {
			if len(d.partial) > maxBufferedLine {
				d.observeLine(d.partial[:maxBufferedLine])
				d.partial = append(d.partial[:0], d.partial[maxBufferedLine-lineOverlap:]...)
				continue
			}
			break
		}
		d.observeLine(d.partial[:newline])
		d.partial = append(d.partial[:0], d.partial[newline+1:]...)
	}
	return len(p), nil
}

// Flush observes the final unterminated console line. Process capture calls it
// after the child console reaches EOF so crash-final corruption messages are
// not discarded merely because they lack a newline.
func (d *Detector) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.partial) == 0 {
		return
	}
	d.observeLine(d.partial)
	d.partial = nil
}

func (d *Detector) observeLine(raw []byte) {
	line := strings.TrimSpace(stripTerminalEscapes(string(raw)))
	lower := strings.ToLower(line)
	if !isCorruptionSignature(lower) {
		return
	}
	if len(line) > 2048 {
		line = line[:2048]
	}
	d.evidence = Evidence{DetectedAt: time.Now(), Message: line}
}

func stripTerminalEscapes(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for i := 0; i < len(value); {
		if value[i] != 0x1b {
			out.WriteByte(value[i])
			i++
			continue
		}
		i++
		if i >= len(value) {
			break
		}
		switch value[i] {
		case '[': // CSI: ESC [ parameters/intermediates final-byte
			i++
			for i < len(value) {
				b := value[i]
				i++
				if b >= 0x40 && b <= 0x7e {
					break
				}
			}
		case ']', 'P', '^', '_': // OSC/DCS/PM/APC: BEL or ST terminated
			i++
			for i < len(value) {
				if value[i] == 0x07 {
					i++
					break
				}
				if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // two-byte ESC sequence
			i++
		}
	}
	return out.String()
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
