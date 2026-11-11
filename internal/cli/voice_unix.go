//go:build unix

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"gorex/internal/rex"
)

// Voice runs this executable as rec or sox, when it was run by one of
// those names, as the links in rex.CLIDir are: Claude Code's /voice
// records with SoX's rec on a machine with no microphone of its own, and
// this rec writes the session's voice instead, which the phone's
// microphone feeds (rex.Voice), as raw PCM, the format rec was asked for.
// It takes the voice only for a recording such as Claude Code's, raw
// audio to stdout (claudeRec); the rest goes to the real SoX on PATH,
// when there is one. The name is args[0]; a run by another name is not
// handled.
func Voice(args []string, out, errOut io.Writer) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
	}
	name := filepath.Base(args[0])
	if name != "rec" && name != "sox" {
		return 0, false
	}
	args = args[1:]
	real := realTool(name)
	version := slices.Contains(args, "--version")
	if name == "rec" && claudeRec(args) {
		if voice := readVoice(); voice != nil {
			defer voice.Close()
			io.Copy(out, voice)
			return ExitOK, true
		}
	}
	if real != "" {
		err := syscall.Exec(real, append([]string{real}, args...), handOverEnv())
		fmt.Fprintf(errOut, "%s: %v\n", name, err)
		return ExitFailed, true
	}
	if version {
		fmt.Fprintf(out, "%s: SoX v14.4.2 (GoRex: the phone's voice)\n", name)
		return ExitOK, true
	}
	if name == "rec" {
		fmt.Fprintln(errOut, "rec: no voice from the phone, and no SoX on PATH")
	} else {
		fmt.Fprintln(errOut, "sox: GoRex's sox only records the phone's voice, as rec; install SoX for the rest")
	}
	return ExitFailed, true
}

// readVoice reads the voice of the session this runs in, from its server,
// once one begins; nil when none does.
func readVoice() io.ReadCloser {
	sid, sock := os.Getenv("GOREX_SESSION"), os.Getenv("GOREX_SERVER")
	if sid == "" || sock == "" {
		return nil
	}
	c, err := rex.ConnectSocket(sock)
	if err != nil {
		return nil
	}
	voice, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		c.Close()
		return nil
	}
	return voice
}

// claudeRec tells whether args ask rec for raw audio to stdout, as Claude
// Code's do: "-t raw" and the output file "-", which sox effects may
// follow.
func claudeRec(args []string) bool {
	raw := false
	for i := range len(args) - 1 {
		raw = raw || args[i] == "-t" && args[i+1] == "raw"
	}
	return raw && slices.Contains(args, "-")
}

// handedOver marks the environment of the rec or sox this one execs: it
// may be another GoRex's link, as with two GoRex bin directories on PATH,
// which would take this one for the real SoX, and back again forever. So
// a rec or sox handed over to takes no other.
const handedOver = "GOREX_SOX_HANDED_OVER"

// handOverEnv is the environment of the real SoX: this one's, marked.
func handOverEnv() []string { return append(os.Environ(), handedOver+"=1") }

// realTool finds name on PATH past this executable's links: the real
// SoX's, "" when there is none, or when this one was handed over to.
func realTool(name string) string {
	if os.Getenv(handedOver) != "" {
		return ""
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	me, err := os.Stat(self)
	if err != nil {
		return ""
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, name)
		st, err := os.Stat(path)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 || os.SameFile(st, me) {
			continue
		}
		return path
	}
	return ""
}
