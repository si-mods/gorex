package rexapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// A voice is what the phone's microphone hears, for the rec a block's
// program runs, as Claude Code's /voice does on a host with no microphone
// (rex.Voice). It is GoRex's own, outside Rex's API: GET
// /api/voice?block_id=… upgrades to a WebSocket whose binary messages
// carry raw PCM, signed 16-bit little-endian, 16 kHz, mono, and the client
// closes it as the user stops, which ends the voice. The server sends
// nothing but the close: a normal one as the voice ended on the host, as
// when the program stopped its rec. The welcome no longer lists its
// feature, gorex.voice.
//
// Deprecated: nothing on the host reads a voice any more, since
// GoRex's rec is gone, and the interface will go.

// voiceIdle is how long a voice may go without a message: the phone sends
// one every tenth of a second.
const voiceIdle = 10 * time.Second

// handleVoice feeds a block's voice with what a WebSocket carries.
func (g *Gateway) handleVoice(w http.ResponseWriter, r *http.Request) {
	if !g.hello.Can(rex.CapabilityVoice) {
		writeError(w, http.StatusBadRequest, "the GoRex server takes no voice")
		return
	}
	m, err := g.current()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("reading the layout: %v", err))
		return
	}
	b := m.blockByID(r.URL.Query().Get("block_id"))
	if b == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no block %s", r.URL.Query().Get("block_id")))
		return
	}
	if _, aerr := g.blockInfo(b); aerr != nil {
		writeError(w, aerr.Status, aerr.Message)
		return
	}
	ctx := r.Context()
	feed, err := g.opt.Client.FeedVoice(ctx, b.sid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, strings.TrimPrefix(err.Error(), "rex: "))
		return
	}
	defer feed.Close()
	// As on the control connection, the access rule decides who connects.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessage)
	for {
		readCtx, readCancel := context.WithTimeout(ctx, voiceIdle)
		typ, msg, err := ws.Read(readCtx)
		readCancel()
		if err != nil {
			ws.CloseNow()
			return
		}
		if typ != websocket.MessageBinary {
			ws.Close(websocket.StatusPolicyViolation, "a voice carries binary messages")
			return
		}
		if _, err := feed.Write(msg); err != nil {
			ws.Close(websocket.StatusNormalClosure, "the voice ended")
			return
		}
	}
}
