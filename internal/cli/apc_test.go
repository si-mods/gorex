package cli

import (
	"bytes"
	"testing"
)

func TestAPCWriter(t *testing.T) {
	const nonce = 42
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"plain text", []string{"hello"}, "hello"},
		{"an escape sequence", []string{"\x1b[31mred\x1b[0m"}, "\x1b[31mred\x1b[0m"},
		{
			"the stream's own message is dropped",
			[]string{"\x1b_rex1;42;{\"type\":\"resize\",\"cols\":80}\x1b\\", "hi"},
			"hi",
		},
		{
			"another stream's message stays",
			[]string{"\x1b_rex1;7;{\"type\":\"bell\"}\x1b\\"},
			"\x1b_rex1;7;{\"type\":\"bell\"}\x1b\\",
		},
		{
			"a message split across writes",
			[]string{"a\x1b_rex1;42;{\"ty", "pe\":\"theme\"}\x1b\\b"},
			"ab",
		},
		{"an escape alone waits", []string{"x\x1b", "[1m"}, "x\x1b[1m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			w := newAPCWriter(&out, nonce)
			for _, p := range tc.parts {
				if _, err := w.Write([]byte(p)); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAPCWriterUnterminated checks that a sequence with no end cannot hold
// the stream back or grow without bound.
func TestAPCWriterUnterminated(t *testing.T) {
	var out bytes.Buffer
	w := newAPCWriter(&out, 7)
	w.Write([]byte("a\x1b_rex1;7;{\"ty"))
	long := make([]byte, 3*maxAPC)
	for i := range long {
		long[i] = 'x'
	}
	w.Write(long)
	w.Write([]byte("end"))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if len(got) < len(long) {
		t.Errorf("the stream kept %d of %d bytes", len(got), len(long)+5)
	}
	if w.buf != nil && len(w.buf) > maxAPC {
		t.Errorf("the writer holds %d bytes", len(w.buf))
	}
}
