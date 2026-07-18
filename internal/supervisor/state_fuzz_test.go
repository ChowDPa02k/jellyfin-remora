package supervisor

import (
	"strconv"
	"strings"
	"testing"
)

func FuzzPersistedStateParsing(f *testing.F) {
	f.Add([]byte("0\n0\n1\n1\n"))
	f.Add([]byte("1\n2\n0\nfuture\ntrailing\n"))
	f.Add([]byte{0, 0xff, '\n'})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		got := parsePersistedState(data)
		fields := strings.Fields(string(data))
		wantManual, wantDatabase := false, false
		if len(fields) >= 3 {
			value, _ := strconv.Atoi(fields[2])
			wantManual = value == 1
		}
		if len(fields) >= 4 {
			value, _ := strconv.Atoi(fields[3])
			wantDatabase = value == 1
		}
		if got.ManualStop != wantManual || got.DatabaseDamaged != wantDatabase {
			t.Fatalf("state=%+v, want manual=%t database=%t", got, wantManual, wantDatabase)
		}
	})
}
