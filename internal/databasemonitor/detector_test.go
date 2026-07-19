package databasemonitor

import (
	"strings"
	"testing"
	"time"
)

func TestDetectorRecognizesChunkedANSIWrappedCorruption(t *testing.T) {
	d := &Detector{}
	for _, chunk := range []string{"\x1b[31mMicrosoft.Data.Sqlite.SqliteException: SQLite Error ", "11: 'database disk image is malformed'.\x1b[0m\n"} {
		if _, err := d.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	evidence, ok := d.Candidate(time.Minute)
	if !ok || evidence.Message == "" {
		t.Fatalf("evidence=%+v ok=%t", evidence, ok)
	}
	d.Reset()
	if _, ok := d.Candidate(time.Minute); ok {
		t.Fatal("reset retained corruption evidence")
	}
}

func TestDetectorDoesNotClassifyOperationalSQLiteErrorsAsCorruption(t *testing.T) {
	for _, line := range []string{
		"SQLite Error 5: 'database is locked'.\n",
		"SQLite Error 8: 'attempt to write a readonly database'.\n",
		"SQLite Error 13: 'database or disk is full'.\n",
		"SQLite Error 19: 'UNIQUE constraint failed'.\n",
	} {
		d := &Detector{}
		_, _ = d.Write([]byte(line))
		if evidence, ok := d.Candidate(time.Minute); ok {
			t.Fatalf("%q produced corruption evidence %+v", line, evidence)
		}
	}
}

func TestDetectorFlushesUnterminatedCrashLine(t *testing.T) {
	d := &Detector{}
	_, _ = d.Write([]byte("SQLite Error 11: database disk image is malformed"))
	if _, ok := d.Candidate(time.Minute); ok {
		t.Fatal("unterminated line was observed before EOF")
	}
	d.Flush()
	if _, ok := d.Candidate(time.Minute); !ok {
		t.Fatal("unterminated crash-final line was not observed at EOF")
	}
}

func TestDetectorScansOversizedLineAcrossChunkBoundary(t *testing.T) {
	d := &Detector{}
	prefix := strings.Repeat("x", maxBufferedLine-10)
	_, _ = d.Write([]byte(prefix + "database disk image is malformed" + strings.Repeat("y", maxBufferedLine)))
	if _, ok := d.Candidate(time.Minute); !ok {
		t.Fatal("signature spanning the scan boundary was not observed")
	}
}

func TestDetectorStripsOSCAndDCSTerminalSequences(t *testing.T) {
	d := &Detector{}
	_, _ = d.Write([]byte("database disk\x1b]0;title\x07 image is\x1bPpayload\x1b\\ malformed\n"))
	if _, ok := d.Candidate(time.Minute); !ok {
		t.Fatal("terminal control strings defeated corruption matching")
	}
}

func TestResetBeforeRetainsNewEvidenceAndPartialLine(t *testing.T) {
	d := &Detector{}
	cutoff := time.Now()
	_, _ = d.Write([]byte("SQLite Error 11: database disk image is malformed\npartial "))
	d.ResetBefore(cutoff)
	if _, ok := d.Candidate(time.Minute); !ok {
		t.Fatal("new evidence was cleared")
	}
	_, _ = d.Write([]byte("database disk image is malformed\n"))
	if _, ok := d.Candidate(time.Minute); !ok {
		t.Fatal("partial console line was cleared")
	}
}
