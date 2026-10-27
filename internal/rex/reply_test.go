//go:build darwin || linux || windows

package rex

import "testing"

// TestDropReplies checks what of a client's input is a terminal's answer
// to a query, which only the authority's reach the program: whole
// sequences go, any other byte stays.
func TestDropReplies(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b]11;rgb:2828/2828/2828\x1b\\\x1b[2;1R": "", // what gh's query got
		"\x1b]11;rgb:0/0/0\x07":                     "",
		"\x1b[?62;22c":                              "",
		"\x1b[0n":                                   "",
		"\x1b[?2026;2$y":                            "",
		"\x1b[?1u":                                  "",
		"\x1bP>|ghostty\x1b\\":                      "",
		"":                                          "",
		"ls\r":                                      "ls\r",
		"\x1b[A":                                    "\x1b[A",
		"\x1b[I":                                    "\x1b[I",        // focus in
		"\x1b[97;5u":                                "\x1b[97;5u",    // kitty key
		"\x1b[<0;10;5M":                             "\x1b[<0;10;5M", // mouse
		"\x1b]":                                     "\x1b]",         // Alt+]
		"\x1b]11;rgb:0/0/0\x07l":                    "l",
		"\x1b]5522;type=read:status=DATA\x1b\\":     "\x1b]5522;type=read:status=DATA\x1b\\", // a paste
		"\x1b[1;5R":                                 "",                                      // Ctrl+F3, alike a cursor position
	} {
		if got := string(dropReplies([]byte(in))); got != want {
			t.Errorf("dropReplies(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRepliesFromOneWindow checks that, with two windows on a session,
// both of whose terminals answer a query, the program gets one answer:
// the window typed in last's, else the newest's; the server's own input
// always goes.
func TestRepliesFromOneWindow(t *testing.T) {
	a, b := &attached{seq: 1}, &attached{seq: 2}
	s := &session{clients: map[*attached]struct{}{a: {}, b: {}}}
	reply := []byte("\x1b]11;rgb:2828/2828/2828\x1b\\\x1b[2;1R")
	steps := []struct {
		from *attached
		in   []byte
		want bool
	}{
		{b, reply, true}, // none typed yet: the newest
		{a, reply, false},
		{a, []byte("gh repo clone x\r"), true},
		{a, reply, true},
		{b, reply, false},
		{b, []byte("\x1b[I"), true}, // focused, b is looked at now
		{b, reply, true},
		{a, reply, false},
		{nil, reply, true},
		{a, []byte("\x1b]5522;type=read:status=DATA\x1b\\"), true}, // pasted in a
		{a, reply, true},
	}
	for i, st := range steps {
		if got := len(s.admit(st.from, st.in)) > 0; got != st.want {
			t.Fatalf("step %d: admits %q = %v, want %v", i, st.in, got, st.want)
		}
	}
}
