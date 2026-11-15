package rexapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func openBytesStreamTest(t *testing.T, f *fixture, ticket string) *websocket.Conn {
	t.Helper()
	ws := f.dial("/api/data", bytesProtocol)
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(raw) != 16 {
		t.Fatalf("bad ticket %q: %v", ticket, err)
	}
	hello := append([]byte{0x01}, raw...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	typ, msg, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if typ != websocket.MessageBinary || len(msg) != 32 || msg[0] != 0x81 {
		t.Fatalf("unexpected welcome: typ=%v len=%d", typ, len(msg))
	}
	return ws
}

func TestRev13DownloadBytesStream(t *testing.T) {
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	want := []byte("hello official rev 13 download_begin!\n")
	p := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(p, want, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Second)
	_ = os.Chtimes(p, old, old)

	r := result(t, c.request("dl1", "com.superlogical.terminal.download_begin", map[string]any{
		"session_id": sid,
		"block_id":   block,
		"args": map[string]any{
			"ref":  "download:11111111-2222-3333-4444-555555555555",
			"path": "sample.txt",
		},
	}))
	ticket, _ := r["ticket"].(string)
	version, _ := r["version"].(string)
	if ticket == "" || version == "" || r["name"] != "sample.txt" {
		t.Fatalf("unexpected result: %v", r)
	}

	ws := openBytesStreamTest(t, f, ticket)
	defer ws.CloseNow()
	var got bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				break
			}
			t.Fatalf("read stream: %v", err)
		}
		if len(msg) == 0 {
			continue
		}
		got.Write(msg)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("got %q, want %q", got.String(), string(want))
	}

	// if_none_match with same version returns not_modified without a ticket.
	r2 := result(t, c.request("dl2", "com.superlogical.terminal.download_begin", map[string]any{
		"session_id": sid,
		"block_id":   block,
		"args": map[string]any{
			"ref":           "download:11111111-2222-3333-4444-666666666666",
			"path":          "sample.txt",
			"if_none_match": version,
		},
	}))
	if r2["not_modified"] != true || r2["ticket"] != nil {
		t.Fatalf("expected not_modified without ticket, got %v", r2)
	}
}

func TestRev14TunnelDialAndListen(t *testing.T) {
	f := startFixture(t)
	c, sid, _, _ := downloadSession(t, f)

	// Start an echo TCP server for dial tunnel.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(nc net.Conn) {
				defer nc.Close()
				_, _ = io.Copy(nc, nc)
			}(conn)
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// 1. Dial tunnel.
	res := callChecked(t, c, "session.new_block", map[string]any{
		"session_id": sid,
		"flavor":     tunnelFlavorDial,
		"options": map[string]any{
			"host":           "127.0.0.1",
			"port":           port,
			"client_address": "127.0.0.1:9999",
		},
	})
	dialBlock := res["block_id"].(string)

	info := result(t, c.request("info1", "com.superlogical.tunnel.info", map[string]any{
		"session_id": sid,
		"block_id":   dialBlock,
	}))
	if info["flavor"] != "dial" || info["client_address"] != "127.0.0.1:9999" {
		t.Fatalf("unexpected dial info: %v", info)
	}

	connRes := callChecked(t, c, "data.connect", map[string]any{
		"session_id":  sid,
		"block_id":    dialBlock,
		"data_ref_id": "conn:1",
		"subprotocol": bytesProtocol,
	})
	ws := openBytesStreamTest(t, f, connRes["ticket"].(string))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("ping-tunnel")); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageBinary, []byte{}); err != nil {
		t.Fatal(err)
	}
	var echoed bytes.Buffer
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				break
			}
			t.Fatalf("read dial stream: %v", err)
		}
		if len(msg) > 0 {
			echoed.Write(msg)
		}
	}
	if echoed.String() != "ping-tunnel" {
		t.Fatalf("dial echo = %q, want ping-tunnel", echoed.String())
	}

	// 2. Listen tunnel.
	lres := callChecked(t, c, "session.new_block", map[string]any{
		"session_id": sid,
		"flavor":     tunnelFlavorListen,
		"options": map[string]any{
			"address":       "127.0.0.1",
			"port":          0,
			"client_target": "127.0.0.1:3000",
		},
	})
	listenBlock := lres["block_id"].(string)
	linfo := result(t, c.request("info2", "com.superlogical.tunnel.info", map[string]any{
		"session_id": sid,
		"block_id":   listenBlock,
	}))
	listenAddr := linfo["address"].(string)

	tcpClient, err := net.Dial("tcp", listenAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer tcpClient.Close()

	var payload map[string]any
	for {
		ev := c.nextType("event")
		outer, _ := ev["payload"].(map[string]any)
		outerPayload, _ := outer["payload"].(map[string]any)
		inner, _ := outerPayload["event"].(map[string]any)
		if inner == nil || inner["type"] != "block_event" {
			continue
		}
		p, _ := inner["payload"].(map[string]any)
		if p != nil && p["block_id"] == listenBlock && p["name"] == "connection_requested" {
			payload = p["payload"].(map[string]any)
			break
		}
	}
	ref := payload["ref"].(string)

	lconnRes := callChecked(t, c, "data.connect", map[string]any{
		"session_id":  sid,
		"block_id":    listenBlock,
		"data_ref_id": ref,
		"subprotocol": bytesProtocol,
	})
	lws := openBytesStreamTest(t, f, lconnRes["ticket"].(string))
	defer lws.CloseNow()

	if _, err := fmt.Fprint(tcpClient, "hello-listen"); err != nil {
		t.Fatal(err)
	}
	_, msg, err := lws.Read(ctx)
	if err != nil || string(msg) != "hello-listen" {
		t.Fatalf("listen read = %q, %v", string(msg), err)
	}

	// Close tunnel block cleanly.
	callChecked(t, c, "block.close", map[string]any{
		"session_id": sid,
		"block_id":   listenBlock,
	})
	callChecked(t, c, "block.close", map[string]any{
		"session_id": sid,
		"block_id":   dialBlock,
	})
}
