package rexpeer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// TestSkipSnapshot checks the Ghostty snapshot at the start of a stream: the
// envelope and its records are passed, and what follows is read.
func TestSkipSnapshot(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("GHOSTSNP")
	binary.Write(&b, binary.LittleEndian, uint16(1))
	record := func(tag uint16, payload []byte) {
		binary.Write(&b, binary.LittleEndian, tag)
		binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
		binary.Write(&b, binary.LittleEndian, uint32(0))
		b.Write(payload)
	}
	record(1, []byte("terminal"))
	record(3, make([]byte, 100))
	record(6, nil) // FINISH
	b.WriteString("live output")

	s := &Stream{rd: bufio.NewReader(&b)}
	if err := s.SkipSnapshot(); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "live output" {
		t.Fatalf("after the snapshot: %q", rest)
	}
}

// TestSkipSnapshotAbsent checks a stream of escape sequences, as GoRex's
// own sends: nothing is passed over.
func TestSkipSnapshotAbsent(t *testing.T) {
	s := &Stream{rd: bufio.NewReader(bytes.NewReader([]byte("\x1b[31mred")))}
	if err := s.SkipSnapshot(); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(s)
	if string(rest) != "\x1b[31mred" {
		t.Fatalf("the stream: %q", rest)
	}
}

// TestSkipSnapshotBroken checks a snapshot with no end: it fails rather
// than reading forever.
func TestSkipSnapshotBroken(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("GHOSTSNP")
	binary.Write(&b, binary.LittleEndian, uint16(1))
	for range 200 {
		binary.Write(&b, binary.LittleEndian, uint16(3))
		binary.Write(&b, binary.LittleEndian, uint32(100<<10))
		binary.Write(&b, binary.LittleEndian, uint32(0))
		b.Write(make([]byte, 100<<10))
	}
	s := &Stream{rd: bufio.NewReader(&b)}
	if err := s.SkipSnapshot(); err == nil {
		t.Fatal("a snapshot with no end was read")
	}
}
