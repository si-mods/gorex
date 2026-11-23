package cli

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIconCmd(t *testing.T) {
	dir := t.TempDir()
	svgPath := filepath.Join(dir, "star.svg")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>`
	if err := os.WriteFile(svgPath, []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code, _ := Main([]string{"icon", "--bg", "#D97757", "--fg", "ffffff", svgPath}, &out, &errOut, "")
	if code != ExitOK {
		t.Fatalf("code = %d, err = %s", code, errOut.String())
	}
	want := "\x1b]7502;format=svg:bg=d97757:fg=ffffff:data=" + base64.StdEncoding.EncodeToString([]byte(svg)) + "\x1b\\"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}

	out.Reset()
	errOut.Reset()
	code, _ = Main([]string{"icon", "clear"}, &out, &errOut, "")
	if code != ExitOK || out.String() != "\x1b]7502;clear\x1b\\" {
		t.Fatalf("clear got code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	bigPath := filepath.Join(dir, "big.svg")
	if err := os.WriteFile(bigPath, []byte(strings.Repeat("a", maxCLIIconBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _ = Main([]string{"icon", bigPath}, &out, &errOut, "")
	if code != ExitUsage {
		t.Fatalf("oversized file code = %d, want %d", code, ExitUsage)
	}
}
