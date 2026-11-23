//go:build windows

package rex

import (
	"os"
	"strings"
	"testing"
)

// TestUpgradeStopReleasesJournal checks that a run that stops for an
// upgrade closes the journal, so that the server taking its place compacts
// it as it starts: on Windows a rename over a file another process has
// open fails, and the server that hands over lives until the new one
// serves.
func TestUpgradeStopReleasesJournal(t *testing.T) {
	s, _ := testServer(t)
	s.journal = openJournal()
	t.Cleanup(func() { s.journal.close() })
	s.journal.started()
	// The journal of a long run, past its limit.
	f, err := os.OpenFile(journalPath(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"at":"2026-01-01T00:00:00Z","event":"layout","rev":1,"layout":{}}` + "\n"
	var b strings.Builder
	for b.Len() <= journalLimit {
		b.WriteString(line)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	s.stopForUpgrade(`C:\a-build\GoRex.exe`)
	// The server that takes its place compacts the journal as it starts.
	next := openJournal()
	t.Cleanup(func() { next.close() })
	if st, err := os.Stat(journalPath()); err != nil || st.Size() > journalLimit {
		t.Fatalf("the journal past its limit was not compacted: %v, %v", st, err)
	}
	entries := readJournal()
	if len(entries) == 0 || entries[len(entries)-1].Event != eventStop {
		t.Errorf("the compacted journal does not say how the run ended: %+v", entries)
	}
}
