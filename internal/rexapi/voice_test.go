//go:build darwin || linux

package rexapi

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// TestVoice: the welcome lists the voice feature, and what a WebSocket on
// /api/voice carries for a block, the rec of its session reads, until the
// client closes it.
func TestVoice(t *testing.T) {
	f := startFixture(t)
	c, _, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	features, _ := c.welcome["features"].([]any)
	if !slices.Contains(features, any(featureVoice)) {
		t.Fatalf("welcome features: %v", c.welcome["features"])
	}
	m, err := f.g.current()
	if err != nil {
		t.Fatal(err)
	}
	sid := m.blockByID(block).sid
	ws := f.dial("/api/voice?block_id=" + block)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("first ")); err != nil {
		t.Fatal(err)
	}
	voice, err := f.desk.ReadVoice(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("then")); err != nil {
		t.Fatal(err)
	}
	ws.Close(websocket.StatusNormalClosure, "")
	got, err := io.ReadAll(voice)
	if err != nil || string(got) != "first then" {
		t.Errorf("rec read %q: %v", got, err)
	}
}

// A voice for no block is refused before the upgrade, and a text message
// ends one.
func TestVoiceRefused(t *testing.T) {
	f := startFixture(t)
	_, _, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, f.srv.URL+"/api/voice?block_id=nope", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a voice for no block: %v %v", resp, err)
	}
	ws := f.dial("/api/voice?block_id=" + block)
	if err := ws.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "binary") {
		t.Errorf("a text message: %v", err)
	}
}
