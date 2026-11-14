package rex

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

type serverOpFunc func(s *Server, req Request, peer string, kept *keptConn) (any, error)
type sessionOpFunc func(s *Server, ss *session, req Request, peer string, kept *keptConn) (any, error)

func withSession(fn sessionOpFunc) serverOpFunc {
	return func(s *Server, req Request, peer string, kept *keptConn) (any, error) {
		ss, err := s.session(req.SID)
		if err != nil {
			return nil, err
		}
		return fn(s, ss, req, peer, kept)
	}
}

func serverOp(op string) serverOpFunc {
	switch op {
	case "hello":
		return (*Server).doHello
	case "ping", "subscribe":
		return func(*Server, Request, string, *keptConn) (any, error) { return nil, nil }
	case "list":
		return (*Server).doList
	case "create":
		return (*Server).doCreate
	case "kill":
		return (*Server).doKill
	case "clients":
		return (*Server).doClients
	case "action":
		return (*Server).doAction
	case "getLayout":
		return (*Server).doGetLayout
	case "getLayoutState":
		return (*Server).doGetLayoutState
	case "setLayout":
		return (*Server).doSetLayout
	case "command", "command.done":
		return (*Server).doCommand
	case "upgrade":
		return (*Server).doUpgrade
	case "shutdown":
		return (*Server).doShutdown
	case "input":
		return withSession(doInput)
	case "key":
		return withSession(doKey)
	case "mouse":
		return withSession(doMouse)
	case "focusReport":
		return withSession(doFocusReport)
	case "modes":
		return withSession(func(_ *Server, ss *session, _ Request, _ string, _ *keptConn) (any, error) { return ss.Modes(), nil })
	case "format":
		return withSession(doFormat)
	case "inputMode":
		return withSession(func(_ *Server, ss *session, _ Request, _ string, _ *keptConn) (any, error) { return ss.InputMode() })
	case "clear":
		return withSession(func(_ *Server, ss *session, _ Request, _ string, _ *keptConn) (any, error) {
			ss.ClearScreen()
			return nil, nil
		})
	case "reset":
		return withSession(func(_ *Server, ss *session, _ Request, _ string, _ *keptConn) (any, error) {
			ss.ResetTerminal()
			return nil, nil
		})
	case "paste":
		return withSession(func(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
			return nil, ss.paste(req.Paste)
		})
	case "capture":
		return withSession(doCapture)
	case "focus":
		return withSession(doFocus)
	case "seeStatus":
		return withSession(func(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
			ss.seeStatus(req.Seen)
			return nil, nil
		})
	case "resize", "lockSize", "lockedResize", "unlockSize", "renewSize":
		return withSession(doSizeOp)
	default:
		return nil
	}
}

func (s *Server) doHello(_ Request, peer string, kept *keptConn) (any, error) {
	h := Hello{
		Version:      ProtocolVersion,
		PID:          os.Getpid(),
		Started:      s.started,
		Machine:      s.machine,
		Exe:          s.exe,
		ExeTime:      s.exeTime,
		Upgrade:      canUpgrade(),
		Capabilities: append([]string(nil), capabilities...),
	}
	// A machine not read yet is none: hostInfo may still be writing it.
	select {
	case <-s.hostDone:
		h.Host = s.host
	case <-time.After(3 * time.Second):
	}
	// The pairing address is for a remote peer's QR code. A local
	// client never shows it, and asking the gateway for it would wait
	// on the gateway's own requests when the gateway says hello.
	if peer != "" {
		h.Client = s.remoteClientFor(peer)
		if RexPairURL != nil {
			h.RexPairURL = RexPairURL()
		}
	}
	if kept != nil {
		h.Keepalive, h.Conn = keepaliveTimeout.get(), kept.id
	}
	return h, nil
}

func (s *Server) doList(_ Request, _ string, _ *keptConn) (any, error) {
	all := s.all()
	infos := make([]SessionInfo, 0, len(all))
	for _, ss := range all {
		infos = append(infos, s.info(ss))
	}
	return infos, nil
}

func (s *Server) doCreate(req Request, peer string, _ *keptConn) (any, error) {
	o := CreateOptions{}
	if req.Create != nil {
		o = *req.Create
	}
	ss, err := s.create(o, peer)
	if err != nil {
		return nil, err
	}
	// A remote client's session gets a tab, as one this machine's
	// client starts with the app closed, which shows it as it opens.
	s.cmdMu.Lock()
	app := s.desks > 0
	s.cmdMu.Unlock()
	if peer != "" || !app {
		s.place(ss)
	}
	return s.info(ss), nil
}

func (s *Server) doKill(req Request, _ string, _ *keptConn) (any, error) {
	s.ops.RLock()
	ss, err := s.session(req.SID)
	if err == nil {
		s.end(ss)
	}
	s.ops.RUnlock()
	if err == nil {
		// Its pane goes before the answer, so that no crash after
		// it starts the session again.
		s.ended(ss.id)
	}
	return nil, nil // or already gone
}

func (s *Server) doClients(_ Request, _ string, _ *keptConn) (any, error) {
	out := []ClientInfo{}
	for _, ss := range s.all() {
		ss.mu.Lock()
		var attached []*attached
		for a := range ss.clients {
			attached = append(attached, a)
		}
		ss.mu.Unlock()
		for _, a := range attached {
			ci := ClientInfo{SessionID: ss.id, Peer: a.peer, Remote: a.remote, Seq: a.seq, Via: a.via != nil, Since: a.since}
			if a.remote {
				if c := s.remoteClientFor(a.peer); c != nil {
					ci.Principal = c.Name
				}
			}
			ci.Age = int64(time.Since(a.since).Seconds())
			out = append(out, ci)
		}
	}
	slices.SortFunc(out, func(a, b ClientInfo) int {
		if a.SessionID != b.SessionID {
			return strings.Compare(a.SessionID, b.SessionID)
		}
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *Server) doAction(req Request, peer string, _ *keptConn) (any, error) {
	if req.Action == nil || req.Action.ID == "" {
		return nil, errors.New("rex: no action")
	}
	if req.SID != "" {
		if _, err := s.session(req.SID); err != nil {
			return nil, err
		}
	}
	return s.command(LayoutCommand{Op: "action", SID: req.SID, Action: req.Action.ID, Args: req.Action.Args, Remote: peer != ""}), nil
}

func (s *Server) doGetLayout(_ Request, peer string, _ *keptConn) (any, error) {
	s.settled()
	s.mu.Lock()
	l := s.layout
	s.mu.Unlock()
	if l == nil {
		return nil, nil
	}
	return s.filterLayout(l, peer)
}

func (s *Server) doGetLayoutState(_ Request, peer string, _ *keptConn) (any, error) {
	s.settled()
	s.mu.Lock()
	st := LayoutState{Layout: s.layout, Revision: s.layoutRev}
	s.mu.Unlock()
	if peer != "" && len(st.Layout) > 0 {
		filtered, err := s.filterLayout(st.Layout, peer)
		if err != nil {
			return nil, err
		}
		st.Layout = filtered
	}
	return st, nil
}

func (s *Server) doSetLayout(req Request, _ string, _ *keptConn) (any, error) {
	if !json.Valid(req.Layout) {
		return nil, errors.New("layout is not JSON")
	}
	if !slices.Contains(req.Capabilities, CapabilityLayoutServer) {
		return nil, errors.New("rex: the session server keeps the layout now, and this GoRex would write over it: update GoRex")
	}
	// As an apply does: none commits between the read and this.
	s.applyMu.Lock()
	s.mu.Lock()
	was := s.layout
	s.mu.Unlock()
	l := countSetRevs(was, req.Layout)
	rev, err := s.commitLayout(l, req.LayoutRevision)
	s.applyMu.Unlock()
	if err != nil {
		return nil, err
	}
	return LayoutState{Layout: l, Revision: rev}, nil
}

func (s *Server) doCommand(req Request, _ string, _ *keptConn) (any, error) {
	if req.Command == nil {
		return nil, errors.New("rex: no command")
	}
	if req.Op == "command.done" {
		s.commandDone(*req.Command)
		return nil, nil
	}
	return s.command(*req.Command), nil
}

func (s *Server) doUpgrade(req Request, _ string, _ *keptConn) (any, error) {
	// Once it runs, the connection closes with this executable.
	return nil, s.upgrade(req.Exe)
}

func (s *Server) doShutdown(req Request, _ string, _ *keptConn) (any, error) {
	// A restart is a stop a new server replaces: the next one starts
	// the sessions of the layout again.
	reason := stopRequest
	if req.Restart {
		reason = stopRestart
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.shutdown(reason)
	}()
	return nil, nil
}

func doInput(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	return nil, ss.inputProgram(req.Bytes)
}

func doKey(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	if req.Key == nil {
		return nil, ErrBadInput
	}
	return nil, ss.Key(*req.Key)
}

func doMouse(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	if req.Mouse == nil {
		return nil, ErrBadInput
	}
	return nil, ss.Mouse(*req.Mouse)
}

func doFocusReport(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	if req.FocusReport == nil {
		return nil, ErrBadInput
	}
	return nil, ss.FocusReport(*req.FocusReport)
}

func doFormat(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	o := FormatOptions{}
	if req.Format != nil {
		o = *req.Format
	}
	return ss.Format(o)
}

func doCapture(_ *Server, ss *session, req Request, _ string, _ *keptConn) (any, error) {
	o := CaptureOptions{}
	if req.Capture != nil {
		o = *req.Capture
	}
	return ss.capture(o.History), nil
}

func doFocus(_ *Server, ss *session, _ Request, peer string, kept *keptConn) (any, error) {
	var via net.Conn
	if kept != nil {
		via = kept.conn
	}
	ss.focusFrom(via, peer)
	return nil, nil
}

func doSizeOp(_ *Server, ss *session, req Request, peer string, kept *keptConn) (any, error) {
	switch req.Op {
	case "resize":
		var via net.Conn
		if kept != nil {
			via = kept.conn
		}
		ss.resizeFrom(via, peer, req.Cols, req.Rows)
	case "lockSize":
		if kept == nil || req.Owner == "" {
			return nil, errors.New("rex: lockSize needs an owner, on a connection kept alive")
		}
		ss.lockSize(req.Owner, req.Cols, req.Rows, kept.conn)
	case "lockedResize":
		if !ss.lockedResize(req.Owner, req.Cols, req.Rows) {
			return nil, errSizeUnlocked
		}
	case "unlockSize":
		ss.unlockSize(req.Owner, req.Cols, req.Rows)
	case "renewSize":
		if !ss.renewSize(req.Owner) {
			return nil, errSizeUnlocked
		}
	}
	return nil, nil
}

func (s *Server) do(req Request, peer string, kept *keptConn) (any, error) {
	if strings.HasPrefix(req.Op, "remote.") {
		return s.remoteDo(req)
	}
	if fn := serverOp(req.Op); fn != nil {
		return fn(s, req, peer, kept)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}
