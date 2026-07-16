package databasemonitor

import (
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
