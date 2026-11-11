//go:build darwin || linux

package rex

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// voiceSession starts a session for voices, which the test ends.
func voiceSession(t *testing.T, c *Client) string {
	t.Helper()
	cmd, dir := testShell()
	info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Kill(info.ID) })
	return info.ID
}

// feedVoice begins sid's voice, and writes b to it.
func feedVoice(t *testing.T, c *Client, sid string, b []byte) io.WriteCloser {
	t.Helper()
	feed, err := c.FeedVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { feed.Close() })
	if _, err := feed.Write(b); err != nil {
		t.Fatal(err)
	}
	return feed
}

// readAll reads a voice to its end, failing after a while.
func readAll(t *testing.T, voice io.ReadCloser) []byte {
	t.Helper()
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(voice)
		got <- b
	}()
	select {
	case b := <-got:
		return b
	case <-time.After(5 * time.Second):
		voice.Close()
		t.Fatal("the voice never ended")
		return nil
	}
}

// A rec that comes after the voice began reads it from its beginning,
// then what comes after, until it ends.
func TestVoiceFromItsBeginning(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	sid := voiceSession(t, c)
	feed := feedVoice(t, c, sid, []byte("first "))
	voice, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	feed.Write([]byte("then"))
	feed.Close()
	if got := readAll(t, voice); string(got) != "first then" {
		t.Errorf("read %q", got)
	}
}

// A rec that comes first waits a moment for the voice to begin.
func TestVoiceWaitsForItsBeginning(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	sid := voiceSession(t, c)
	go func() {
		time.Sleep(voiceWait / 3)
		if feed, err := c.FeedVoice(context.Background(), sid); err == nil {
			feed.Write([]byte("late"))
			feed.Close()
		}
	}()
	voice, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	if got := readAll(t, voice); string(got) != "late" {
		t.Errorf("read %q", got)
	}
}

// No voice, or one that ended before a rec took it, is none to read.
func TestVoiceNone(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	sid := voiceSession(t, c)
	feedVoice(t, c, sid, []byte("unheard")).Close()
	for gone := false; !gone; time.Sleep(time.Millisecond) {
		s.voiceMu.Lock()
		gone = s.voices[sid] == nil
		s.voiceMu.Unlock()
	}
	start := time.Now()
	if voice, err := c.ReadVoice(context.Background(), sid); err == nil {
		voice.Close()
		t.Fatal("read a voice that ended unheard")
	}
	if waited := time.Since(start); waited < voiceWait {
		t.Errorf("gave up after %v", waited)
	}
	if _, err := c.ReadVoice(context.Background(), "nope"); err == nil {
		t.Error("read the voice of no session")
	}
}

// A new voice ends the one before, whose rec reads what it had, and the
// next rec reads the new one.
func TestVoiceReplaced(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	sid := voiceSession(t, c)
	feedVoice(t, c, sid, []byte("old"))
	first, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(first, buf); err != nil || string(buf) != "old" {
		t.Fatalf("read %q: %v", buf, err)
	}
	feed := feedVoice(t, c, sid, []byte("new"))
	if got := readAll(t, first); len(got) != 0 {
		t.Errorf("the old voice read on: %q", got)
	}
	second, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	feed.Close()
	if got := readAll(t, second); string(got) != "new" {
		t.Errorf("read %q", got)
	}
}

// The server keeps the last ten seconds of a voice no rec read yet.
func TestVoiceKeepsTheLast(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	sid := voiceSession(t, c)
	b := make([]byte, maxVoiceBytes+1000)
	for i := range b {
		b[i] = byte(i)
	}
	feed := feedVoice(t, c, sid, b)
	voice, err := c.ReadVoice(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	feed.Close()
	if got := readAll(t, voice); !bytes.Equal(got, b[1000:]) {
		t.Errorf("read %d bytes, want the last %d", len(got), maxVoiceBytes)
	}
}

// Voices are this machine's: a remote client feeds none.
func TestVoiceLocalOnly(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	sid := voiceSession(t, local)
	remote := pipe(s, testPeer)
	defer remote.Close()
	if feed, err := remote.FeedVoice(context.Background(), sid); err == nil {
		feed.Close()
		t.Fatal("a remote client fed a voice")
	}
}
