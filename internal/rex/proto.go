// Package rex is GoRex's session server and its client.
//
// The server owns the pseudo-terminals: shells keep running when the app
// quits, and the next launch attaches to them again, with what they printed
// replayed. The app talks to it over a Unix socket: a control connection of
// JSON lines (requests and their responses), and one connection per
// attached session that carries the terminal's bytes both ways.
package rex

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ProtocolVersion changes whenever the wire protocol does. Version 2 adds,
// and only adds, the size lock ("lockSize", "lockedResize", "unlockSize",
// "renewSize", SessionInfo.SizeLock), layout commands (LayoutCommand),
// framed attaches
// (Attach.Framed, Attach.History) and the list's ETag: a client of
// version 1 works as before, its resizes ignored while a session is locked.
// Later, still within 2, "subscribe" (Response.Event): a server before it
// answers it with an error, and a client that does not ask gets no events.
//
// Version 3 adds capabilities in Hello, layout revisions ("getLayoutState",
// Request.LayoutRevision, Response.Revision) and revisions on events. It
// only adds: a version 2 client gets the same answers, with fields it
// ignores.
const ProtocolVersion = 3

// capabilities are what a version 3 server does beyond version 2. A
// client reads them from Hello and falls back when one is absent.
var capabilities = []string{"layout.revision", "events.revision"}

// Request is a line of the control connection, from the client.
type Request struct {
	ID     int64           `json:"id"`
	Op     string          `json:"op"`
	SID    string          `json:"sid,omitempty"`
	Cols   int             `json:"cols,omitempty"`
	Rows   int             `json:"rows,omitempty"`
	Create *CreateOptions  `json:"create,omitempty"`
	Layout json.RawMessage `json:"layout,omitempty"`
	// Remote is the client of remote.allow, remote.revoke,
	// remote.approve and remote.reject, and the name of remote.pair.
	Remote *RemoteClient `json:"remote,omitempty"`
	// Network is the network to join, of remote.join.
	Network *NetworkJoin `json:"network,omitempty"`
	// Exe is the executable the server runs in its place on "upgrade".
	Exe string `json:"exe,omitempty"`
	// Keepalive, on "hello", asks the server to drop the connection once
	// it has sent nothing for Hello.Keepalive, with the sessions attached
	// through it (Attach.Conn): the client pings meanwhile. A server that
	// does not know it ignores it, and answers no Hello.Keepalive.
	Keepalive bool `json:"keepalive,omitempty"`

	// On is the setting of remote.shareSet.
	On bool `json:"on,omitempty"`

	// Owner is the size lock's owner, of lockSize, lockedResize,
	// unlockSize and renewSize: unlockSize without one unlocks whoever
	// holds it. lockSize takes the lock through this connection, which
	// must be kept alive: the lock ends as it closes, or 30 seconds after
	// it was taken or renewed last, and the sessions attached through it
	// answer the programs' queries meanwhile.
	Owner string `json:"owner,omitempty"`
	// Bytes is what "input" types into the session.
	Bytes []byte `json:"bytes,omitempty"`
	// Capture is what "capture" shows of the session.
	Capture *CaptureOptions `json:"capture,omitempty"`
	// Action is the app action "action" runs.
	Action *ActionCall `json:"action,omitempty"`
	// ETag, on "list", is that of the list the client has (Response.ETag).
	ETag string `json:"etag,omitempty"`
	// LayoutRevision, on "setLayout", makes the change only when the
	// server's layout is at that revision; 0 sets it whatever it is.
	LayoutRevision uint64 `json:"layoutRevision,omitempty"`
	// Command is the layout command of "command", and the app's answer of
	// "command.done".
	Command *LayoutCommand `json:"command,omitempty"`
}

// CommandTimeout is how long the server waits for the app to make a
// layout command, a new session included, which Windows may take seconds
// to start; the app gives up on one its window does not start making
// within AppCommandTimeout, and a client waits a while longer than the
// server, to see the layout saved.
const (
	CommandTimeout    = 6 * time.Second
	AppCommandTimeout = CommandTimeout / 2
)

// LayoutCommand is a change of the layout that a client of this machine,
// as rootshell's bridge, asks the app to make ("command"): the app, which
// owns the layout, takes the commands in order ("command.take"), makes
// them as its own buttons do, saves the layout and answers each
// ("command.done"). Panes and tabs are named by a session of theirs.
type LayoutCommand struct {
	ID int64 `json:"id,omitempty"`
	// Op is "tab.create", "pane.split", "pane.close", "tab.close",
	// "tab.rename", "pane.zoom" or "tab.move".
	Op  string `json:"op,omitempty"`
	SID string `json:"sid,omitempty"`
	// After is the tab a new or moved tab goes after, the first of this
	// machine's tabs when "" for tab.move.
	After string `json:"after,omitempty"`
	// Dir is where a new pane starts, its tab's or its pane's when "".
	Dir string `json:"dir,omitempty"`
	// Vertical splits down, else right; Name is a tab's new name; Mode is
	// zoom's "on", "off" or "toggle".
	Vertical bool   `json:"vertical,omitempty"`
	Name     string `json:"name,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Action is the action of "action", and Args its arguments.
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	// Index is where tab.move puts the tab among this machine's tabs as
	// the app makes it, counted with the tab in its place; in the answer,
	// where the tab is then.
	Index int `json:"index,omitempty"`
	// The answer: Code and Error when it was not made, New the session of
	// the pane made, and Zoomed whether the pane's tab is zoomed now.
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
	New    string `json:"new,omitempty"`
	Zoomed bool   `json:"zoomed,omitempty"`
}

// ClientInfo is one client attached to a session: a window, or a CLI
// reading it, and whether it came from a remote host.
type ClientInfo struct {
	SessionID string `json:"sessionId"`
	Peer      string `json:"peer,omitempty"`
	Remote    bool   `json:"remote,omitempty"`
	Seq       uint64 `json:"seq,omitempty"`
	Via       bool   `json:"via,omitempty"`
}

// CaptureOptions shape a capture.
type CaptureOptions struct {
	// History cuts the oldest scrollback to about this many bytes; 0 keeps
	// all of it.
	History int `json:"history,omitempty"`
}

// CaptureResult is a session's screen as escape sequences, the way an
// attach sends it, and what the session shows.
type CaptureResult struct {
	Screen    []byte `json:"screen"`
	Title     string `json:"title,omitempty"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ActionCall names an app action and its arguments.
type ActionCall struct {
	ID   string          `json:"id"`
	Args json.RawMessage `json:"args,omitempty"`
}

// RemoteClient is a client allowed to connect remotely: PublicKey is its
// tailcat node key, "nodekey:" and 64 hex digits.
type RemoteClient struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

// RemoteStatus answers remote.status. Address, the tailcat address
// clients connect to while running, is a secret.
type RemoteStatus struct {
	Enabled   bool           `json:"enabled"`
	Running   bool           `json:"running"`
	Address   string         `json:"address,omitempty"`
	Allowed   []RemoteClient `json:"allowed"`
	LastError string         `json:"lastError,omitempty"`
	// Pairing is the pairing window, Pending the requests made in it.
	Pairing RemotePairing   `json:"pairing"`
	Pending []RemotePending `json:"pending,omitempty"`
	// ShareSessions tells that remote clients see and use every session;
	// off, each only those it created.
	ShareSessions bool `json:"shareSessions"`
	// Network is the coordinator network this machine joined, if any;
	// its Members are served beside Allowed.
	Network *NetworkStatus `json:"network,omitempty"`
}

// RemotePairing is the pairing window: while open, a client not allowed
// may ask to be, with remote.pair.
type RemotePairing struct {
	Open      bool      `json:"open"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// RemotePending is a client that asked to pair, waiting for approval.
type RemotePending struct {
	Name        string    `json:"name"`
	PublicKey   string    `json:"publicKey"`
	RequestedAt time.Time `json:"requestedAt"`
}

// PairResult answers remote.pair: the fingerprint of the key the server
// saw, to compare with the one the server shows.
type PairResult struct {
	Fingerprint string `json:"fingerprint"`
}

// NetworkJoin is the network to join, of remote.join: the join URL,
// which pins the coordinator, the network key, and the name this
// machine joins by. The URL and the key are secrets.
type NetworkJoin struct {
	URL  string `json:"url"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// NetworkStatus answers the network half of remote.status: the members
// beside the allowlist, as the coordinator's directory says. The
// coordinator is the host of the join URL, said without the URL, which
// is a secret; so is every member's address, and the status itself
// goes over the local socket only.
type NetworkStatus struct {
	Name        string          `json:"name"`
	Coordinator string          `json:"coordinator"`
	Members     []NetworkMember `json:"members"`
	Revision    int             `json:"revision"`
	LastError   string          `json:"lastError,omitempty"`
}

// NetworkMember is a member of the network, as the app opens it as a
// host. Address is its tailcat address, a secret as the server's own.
type NetworkMember struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	Address   string `json:"address"`
}

// Response answers the Request with the same ID.
type Response struct {
	ID    int64           `json:"id"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	// ETag names the answer to "list": when it is the request's, Data is
	// left out, the list unchanged.
	ETag string `json:"etag,omitempty"`
	// Event, on a line of no ID, tells a connection that subscribed of a
	// change: "layout", as setLayout changes it, or "sessions", as one
	// starts, ends, or changes its size, size lock, title or foreground
	// program.
	Event string `json:"event,omitempty"`
	// Revision, on a layout event, is the layout's revision after the
	// change (LayoutState).
	Revision uint64 `json:"revision,omitempty"`
}

// Event is a line a subscribed connection gets, with no ID: the change
// it tells of, and the layout's revision for a layout change.
type Event struct {
	Kind     string `json:"event"`
	Revision uint64 `json:"revision,omitempty"`
}

// LayoutState is the layout the server keeps, with its revision: a
// client sends the revision it has to setLayout, and gets an error when
// another client changed the layout since.
type LayoutState struct {
	Layout   json.RawMessage `json:"layout"`
	Revision uint64          `json:"revision"`
}

// CreateOptions start a session.
type CreateOptions struct {
	// Command runs instead of the user's login shell.
	Command []string `json:"command,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Env     []string `json:"env,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

// SessionInfo describes a session and what runs in it now.
type SessionInfo struct {
	ID      string    `json:"id"`
	PID     int       `json:"pid"`
	Shell   string    `json:"shell"`
	Created time.Time `json:"created"`
	// Program is the name of the program in the foreground, and Args its
	// arguments; Idle tells that it is the shell itself, at its prompt.
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
	Idle    bool     `json:"idle"`
	// Dir is the working directory of the program in the foreground.
	Dir string `json:"dir"`
	// Title is the last title a program set (OSC 0, OSC 2).
	Title      string    `json:"title,omitempty"`
	LastOutput time.Time `json:"lastOutput"`
	LastInput  time.Time `json:"lastInput"`
	Output     uint64    `json:"output"`
	Bells      int       `json:"bells"`
	Exited     bool      `json:"exited"`
	ExitCode   int       `json:"exitCode"`
	Attached   int       `json:"attached"`
	Cols       int       `json:"cols"`
	Rows       int       `json:"rows"`
	// SizeLock is the owner that holds the session's size (lockSize), ""
	// while the windows set it.
	SizeLock string `json:"sizeLock,omitempty"`
	// Statuses are what the programs said they do (OSC 7501), by id, and
	// Status the one that most needs the user (MostUrgent); nil when they
	// said nothing.
	Statuses []ProgramStatus `json:"statuses,omitempty"`
	Status   *ProgramStatus  `json:"status,omitempty"`
}

// ProgramStatus is a record of the program status protocol (OSC 7501).
type ProgramStatus struct {
	// ID is a path of segments, "" for the root record.
	ID string `json:"id,omitempty"`
	// State is idle, working, blocked, done or error; Kind, for blocked,
	// permission, question or auth.
	State string `json:"state"`
	Kind  string `json:"kind,omitempty"`
	// Progress is a percentage, -1 when unknown.
	Progress int `json:"progress"`
	// App is the program's name, its ancestor's when it gave none.
	App     string    `json:"app,omitempty"`
	Title   string    `json:"title,omitempty"`
	Msg     string    `json:"msg,omitempty"`
	Updated time.Time `json:"updated"`
}

// Hello is the server's answer to "hello".
type Hello struct {
	Version int       `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Host    HostInfo  `json:"host"`
	// Exe is the server's executable, and ExeTime when it was modified
	// last as the server started: the app compares them with its own, to
	// upgrade a server of an older build.
	Exe     string    `json:"exe,omitempty"`
	ExeTime time.Time `json:"exeTime,omitzero"`
	// Upgrade tells that the server can run another executable in its
	// place, keeping its sessions (Client.Upgrade).
	Upgrade bool `json:"upgrade,omitempty"`
	// Keepalive answers a hello that asked for it: how long the server
	// waits for a request of this connection ("ping" will do) before it
	// drops it, and Conn names the connection for Attach.Conn.
	Keepalive time.Duration `json:"keepalive,omitempty"`
	Conn      string        `json:"conn,omitempty"`
	// Capabilities are what this server does beyond the version 2
	// protocol, such as "layout.revision" and "events.revision".
	Capabilities []string `json:"capabilities,omitempty"`
}

// HostInfo describes the machine the server runs on.
type HostInfo struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Chip   string `json:"chip"`
	Memory uint64 `json:"memory"`
	OS     string `json:"os"`
	User   string `json:"user"`
	Home   string `json:"home"`
	// Portable tells a machine with a battery, as a laptop, where the
	// model does not.
	Portable bool `json:"portable,omitempty"`
}

// Attach is the first line of a connection attaching to a session.
type Attach struct {
	Op   string `json:"op"` // "attach"
	SID  string `json:"sid"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	// Conn, when set, is the control connection (Hello.Conn) whose loss
	// detaches this one too: a peer that vanished without closing it is
	// noticed on the control connection, which pings.
	Conn string `json:"conn,omitempty"`
	// Framed puts a SnapshotHeader line before the snapshot, which tells
	// where it ends, for a client with no emulator of its own. History,
	// when set, cuts the oldest scrollback of the snapshot to about that
	// many bytes.
	Framed  bool `json:"framed,omitempty"`
	History int  `json:"history,omitempty"`
}

// SnapshotHeader is the line before the snapshot of a framed attach: its
// length in bytes, the session's size it was taken at, and whether
// scrollback was cut (Attach.History).
type SnapshotHeader struct {
	Snapshot  int  `json:"snapshot"`
	Cols      int  `json:"cols"`
	Rows      int  `json:"rows"`
	Truncated bool `json:"truncated,omitempty"`
}

// ServerMain runs the server when args, the program's arguments, ask
// for it, and tells whether they did: -server serves, -server -resume
// PATH serves the sessions a server upgrading to this executable handed
// it (Resume), and -server -handoff-version prints the version of the
// handoffs it reads, which an upgrading server asks first.
func ServerMain(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "-server" {
		return false, nil
	}
	log.SetPrefix("[server] ")
	switch {
	case len(args) > 1 && args[1] == "-handoff-version":
		fmt.Println(handoffVersion)
		return true, nil
	case len(args) > 2 && args[1] == "-resume":
		return true, Resume(args[2])
	}
	return true, Serve()
}

// Dir returns the directory of the server's socket, log and state.
func Dir() string {
	if d := os.Getenv("GOREX_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "GoRex")
}

// SocketPath returns the path of the server's socket: in Dir, unless that
// is too long a path for a socket, then in the temporary directory.
func SocketPath() string {
	p := filepath.Join(Dir(), "server.sock")
	if len(p) < 100 {
		return p
	}
	h := fnv.New32a()
	h.Write([]byte(p))
	name := fmt.Sprintf("gorex-%x.sock", h.Sum32())
	if uid := os.Getuid(); uid >= 0 { // Windows has none, and a temporary directory per user
		name = fmt.Sprintf("gorex-%d-%x.sock", uid, h.Sum32())
	}
	return filepath.Join(os.TempDir(), name)
}
