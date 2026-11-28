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
	"slices"
	"strings"
	"time"
	"unicode"

	"gorex/internal/layout"
)

// ProtocolVersion changes whenever the wire protocol does. Version 2 adds,
// and only adds, the size lock ("lockSize", "lockedResize", "unlockSize",
// "renewSize", SessionInfo.SizeLock), layout commands (LayoutCommand),
// framed attaches
// (Attach.Framed, Attach.History) and the list's ETag: a client of
// version 1 works as before, its resizes ignored while a session is locked.
// Later, still within 2, "subscribe" (Response.Event): a server before it
// answers it with an error, and a client that does not ask gets no events;
// and the attach's client id (Attach.Client), which SessionInfo.SizedBy
// names the client whose terminal sizes the session by.
//
// Version 3 adds capabilities in Hello, layout revisions ("getLayoutState",
// Request.LayoutRevision, Response.Revision) and revisions on events. It
// only adds: a version 2 client gets the same answers, with fields it
// ignores. Still within 3, the capability "snapshot.ghostty" (see
// Attach.Snapshot and SnapshotHeader.Format): a client that does not ask
// attaches to the VT snapshot as before. Still within 3, the capability
// "input.events": "key", "mouse" and "focusReport", which encode a
// client's input as the program's modes ask (KeyInput, MouseInput), and
// the capability "terminal.query": "modes", "format", "inputMode",
// "clear" and "reset", which report on and change a session's terminal
// (ModeStatus, FormatResult, InputMode), and SessionInfo.Foreground, the
// foreground process's ID. Still within 3, the capability "file.transfer":
// connections that send a file into a session's directory ("file.put",
// FilePut) and take one from it ("file.get", FileGet), and the
// "transfer" events of their progress (Transfer). Still within 3, the
// capability "teleport": "teleport" and "teleport.pack", each on a
// connection of its own, which move a pane's work to another machine
// (Teleport). Still within 3, the capability "layout.server": the server
// makes every change of the layout itself, and takes the app's
// arrangement of a tab ("tab.set") and of its windows ("windows.set"),
// with the LayoutCommand fields TabLayout, Spaces, Color and Float.
// Still within 3, the capability "tab.to_space": "tab.to_space", which
// moves a tab to another space (LayoutCommand.ToSpace).
// Still within 3, the capability "status.seen": "seeStatus", which tells that the user saw a session's
// status records, and SessionInfo.StatusSeen, which every client reads
// back. Still within 3, the capability "dir.list": "dir.list", which
// lists the directories in a directory of the server's machine (DirList).
// Still within 3, the capability "space.home": "space.home", which sets
// or clears a space's home (layout.Workspace.Host and Dir), a tab.create
// of no Dir in a space whose home is this machine starting in its folder
// or failing with "folder_missing", and CreateOptions.DirStrict. Still
// within 3, the capability "projects.v2": a host keeps its projects, a
// folder each, and its tabs with no project (CapabilityProjects).
// Still within 3, the capability "gorex.push": the gateway pushes Live
// Activity updates through the push relay (CapabilityPush,
// docs/NET-003-push-relay.md).
const ProtocolVersion = 3

// CapabilityShutdownRestart is what a server that stops to be replaced — one
// the next starts the sessions of its layout again — says it does.
const CapabilityShutdownRestart = "shutdown.restart"

// CapabilityShutdownIfIdle is what a server that stops only when it holds
// no session, nor one it is starting, says it does (Request.IfIdle). A
// server without it ignores the field and stops at once.
const CapabilityShutdownIfIdle = "shutdown.ifIdle"

// BusyPrefix starts the error of a "shutdown" with IfIdle that a server in
// use refuses.
const BusyPrefix = "busy: "

// CapabilitySnapshotGhostty is what a server that takes a session's screen
// as Ghostty's binary snapshot says it does: Attach.Snapshot "ghostty",
// and SnapshotHeader.Format "ghostty" in the header of a framed attach.
const CapabilitySnapshotGhostty = "snapshot.ghostty"

// SnapshotGhostty is Attach.Snapshot's value, and SnapshotHeader.Format's,
// for a screen as Ghostty's binary snapshot ("GHOSTSNP") rather than as
// the escape sequences that replay it.
const SnapshotGhostty = "ghostty"

// CapabilityInputEvents is what a server that encodes a client's input
// events for the program, as the program's modes ask, says it does: the
// "key", "mouse" and "focusReport" ops.
const CapabilityInputEvents = "input.events"

// CapabilityTerminalQuery is what a server that reports on and changes a
// session's terminal says it does: the "modes", "format", "inputMode",
// "clear" and "reset" ops.
const CapabilityTerminalQuery = "terminal.query"

// CapabilityFileTransfer is what a server that takes files into its
// sessions' directories and gives files from them says it does: the
// "file.put" and "file.get" connections, and "transfer" events.
const CapabilityFileTransfer = "file.transfer"

// CapabilityFileTemp is what a server that takes a put into a temporary
// folder of the user's (FilePut.Temp) says it does.
const CapabilityFileTemp = "file.temp"

// CapabilityLayoutServer is what a server that makes every change of the
// layout itself says it does: tab.set and windows.set, from this
// machine's clients.
const CapabilityLayoutServer = "layout.server"

// CapabilityTabToSpace is what a server that moves a tab to another space
// says it does: the "tab.to_space" command (LayoutCommand.ToSpace).
const CapabilityTabToSpace = "tab.to_space"

// CapabilityStatusSeen is what a server that keeps when its sessions'
// status records were seen says it does: the "seeStatus" op, and
// SessionInfo.StatusSeen.
const CapabilityStatusSeen = "status.seen"

// CapabilityDirList is what a server that lists the directories of its
// machine says it does: the "dir.list" op (DirList).
const CapabilityDirList = "dir.list"

// CapabilityDirMkdir is what a server that makes directories on its
// machine says it does: the "dir.mkdir" op (DirMkdir).
const CapabilityDirMkdir = "dir.mkdir"

// CapabilityAgents is what a server that detects installed agent CLIs
// says it does: the "agents.list" op (AgentsResult).
const CapabilityAgents = "agents.list"

// CapabilityPrompts is a client's, not the server's: the client shows the
// questions a config action asks and answers them, and the server sends it
// prompt events (docs/MOD-009-client-prompts.md). The server's own
// CapabilityPrompts in Hello says it asks them.
const CapabilityPrompts = "prompts"

// CapabilitySpaceHome is what a server that keeps a space's home, a
// project's folder, says it does: the "space.home" command, a tab.create
// that starts in the folder of a space whose home is this machine, and
// CreateOptions.DirStrict. A server without it ignores DirStrict and
// starts a session whose directory is gone at home.
const CapabilitySpaceHome = "space.home"

// FolderMissing is the code of a tab.create, and the start of a create's
// error, that failed because the session must start in a folder
// (CreateOptions.DirStrict) that is not a directory it can read.
const FolderMissing = "folder_missing"

// CapabilityProgramIcon is what a server that parses and relays custom
// program icons (OSC 7502, docs/MOD-011-program-icons.md) has.
const CapabilityProgramIcon = "program.icon"

// CapabilityDelegate is what a server that launches and tracks delegated
// coding agent tabs (docs/MOD-016-peer-delegation.md) says it does: the
// "delegate", "delegate.send", and "delegate.result" ops.
const CapabilityDelegate = "delegate"

// capabilities are what a version 3 server does beyond version 2. A
// client reads them from Hello and falls back when one is absent.
var capabilities = []string{"layout.revision", "events.revision", CapabilityShutdownRestart, CapabilityShutdownIfIdle, CapabilitySnapshotGhostty, CapabilityInputEvents, CapabilityTerminalQuery, CapabilityFileTransfer, CapabilityFileTemp, CapabilityTeleport, CapabilityLayoutServer, CapabilityTabToSpace, CapabilityStatusSeen, CapabilityDirList, CapabilityDirMkdir, CapabilityAgents, CapabilitySpaceHome, CapabilityProjects, CapabilitySpacesDelete, CapabilityPrompts, CapabilityPush, CapabilityProgramIcon, CapabilityDelegate}

// Request is a line of the control connection, from the client.
type Request struct {
	ID int64  `json:"id"`
	Op string `json:"op"`
	// TailnetTarget is the target discovery named, of remote.tailnetDial:
	// the server relays the connection to it over the system tailnet.
	TailnetTarget string          `json:"tailnetTarget,omitempty"`
	SID           string          `json:"sid,omitempty"`
	Cols          int             `json:"cols,omitempty"`
	Rows          int             `json:"rows,omitempty"`
	Create        *CreateOptions  `json:"create,omitempty"`
	Layout        json.RawMessage `json:"layout,omitempty"`
	// Remote is the client of remote.allow, remote.revoke,
	// remote.approve and remote.reject, and the name of remote.pair.
	Remote *RemoteClient `json:"remote,omitempty"`
	// Code names the request by address of remote.approve and
	// remote.reject, in place of Remote.
	Code string `json:"code,omitempty"`
	// TailcatAddress is the address of remote.tailcatDial: the server
	// connects to it with its tailcat client, and relays the connection.
	// remote.tailcatStatus also names it: the server pings the address
	// with its client of it, and answers how the link runs
	// (TailcatStatus).
	TailcatAddress string `json:"tailcatAddress,omitempty"`
	// Relay is the relay of remote.relaySet: a DERP server's host, "" for
	// tailcat's own.
	Relay string `json:"relay,omitempty"`
	// ReachableAt is the address of remote.reachableSet: an IP address of
	// this machine, "" for off.
	ReachableAt string `json:"reachableAt,omitempty"`
	// Exe is the executable the server runs in its place on "upgrade".
	Exe string `json:"exe,omitempty"`
	// Keepalive, on "hello", asks the server to drop the connection once
	// it has sent nothing for Hello.Keepalive, with the sessions attached
	// through it (Attach.Conn): the client pings meanwhile. A server that
	// does not know it ignores it, and answers no Hello.Keepalive.
	Keepalive bool `json:"keepalive,omitempty"`
	// Transfers, on "subscribe", asks for the "transfer" events too
	// (CapabilityFileTransfer): a subscriber that does not ask gets none.
	Transfers bool `json:"transfers,omitempty"`
	// Restart, on "shutdown", asks the server to stop as one that a new
	// server replaces: the next one starts the sessions of its layout
	// again (Client.Restart, CapabilityShutdownRestart). Without it the
	// sessions end with the server. A server that does not know it
	// ignores it.
	Restart bool `json:"restart,omitempty"`
	// IfIdle, on "shutdown", stops the server only if it holds no session
	// and starts none, checked and stopped in one step; a server in use
	// answers an error that starts with BusyPrefix (CapabilityShutdownIfIdle).
	IfIdle bool `json:"ifIdle,omitempty"`

	// On is the setting of remote.controlSet and
	// remote.tailnetSet.
	On bool `json:"on,omitempty"`

	// Owner is the size lock's owner, of lockSize, lockedResize,
	// unlockSize and renewSize: unlockSize without one unlocks whoever
	// holds it. A lock that ends without a size gives the session the
	// size of its authority (SessionInfo.SizedBy). lockSize takes the
	// lock through this connection, which must be kept alive: the lock
	// ends as it closes, or 30 seconds after it was taken or renewed
	// last, and the sessions attached through it answer the programs'
	// queries meanwhile.
	Owner string `json:"owner,omitempty"`
	// Client is the id an attach gave (Attach.Client) of the stream
	// "resync" sends the session's screen again on.
	Client string `json:"client,omitempty"`
	// Prompt, on "prompt.open", is the question a config action asks
	// (docs/MOD-009-client-prompts.md). PromptTimeout is how long the
	// server waits for an answer, in milliseconds: 0 is five minutes.
	Prompt        *PromptSpec `json:"prompt,omitempty"`
	PromptTimeout int         `json:"promptTimeout,omitempty"`
	// PromptAction names the action that asks, which the clients show.
	PromptAction string `json:"promptAction,omitempty"`
	// CallID names the question a client answers, of "prompt.answer",
	// and of the prompt events.
	CallID string `json:"callId,omitempty"`
	// Answer is "prompt.answer"'s answer.
	Answer *PromptAnswer `json:"answer,omitempty"`
	// Bytes is what "input" types into the session.
	Bytes []byte `json:"bytes,omitempty"`
	// Paste is the text "paste" writes to the session's program as a
	// paste, bracketed when the program asked for bracketed pastes: a
	// console that drops the terminal's paste event, a ConPTY on Windows,
	// gets a paste this way (SessionInfo.ConPTY).
	Paste string `json:"paste,omitempty"`
	// Actor names who sent an "input" or "paste" request ("cli", "human",
	// or "delegate"), and Agent names the short badge label for CLI input
	// (docs/DSK-016-agent-pane-highlight.md).
	Actor string `json:"actor,omitempty"`
	Agent string `json:"agent,omitempty"`
	// Theme is the host terminal's default colors, of "theme.set".
	Theme *Theme `json:"theme,omitempty"`
	// Seen is the time of the latest status record "seeStatus" tells the
	// user saw (ProgramStatus.Updated).
	Seen time.Time `json:"seen,omitzero"`
	// Capture is what "capture" shows of the session.
	Capture *CaptureOptions `json:"capture,omitempty"`
	// Action is the app action "action" runs.
	Action *ActionCall `json:"action,omitempty"`
	// ETag, on "list", is that of the list the client has (Response.ETag).
	ETag string `json:"etag,omitempty"`
	// LayoutRevision, on "setLayout", makes the change only when the
	// server's layout is at that revision; 0 sets it whatever it is. The
	// server takes a whole layout only from a client that names
	// CapabilityLayoutServer in Capabilities, as tests and tools that seed
	// one do (Client.SetLayout): an app of before it would write its whole
	// layout over the server's changes.
	LayoutRevision uint64   `json:"layoutRevision,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	// Command is the layout command of "command", and the app's answer of
	// "command.done".
	Command *LayoutCommand `json:"command,omitempty"`
	// Key is the key event of "key", a client's input for the program,
	// encoded as the program's modes ask (CapabilityInputEvents).
	Key *KeyInput `json:"key,omitempty"`
	// Mouse is the pointer event of "mouse", and of the wheel.
	Mouse *MouseInput `json:"mouse,omitempty"`
	// FocusReport is the "focusReport" op's payload: true for the
	// terminal's window gaining the focus, false for losing it.
	FocusReport *bool `json:"focusReport,omitempty"`
	// Format is what "format" shows of the session's terminal
	// (CapabilityTerminalQuery).
	Format *FormatOptions `json:"format,omitempty"`
	// Teleport is the package that follows "teleport" on its connection
	// (CapabilityTeleport).
	Teleport *TeleportManifest `json:"teleport,omitempty"`
	// DirList is what "dir.list" lists (CapabilityDirList).
	DirList *DirList `json:"dirList,omitempty"`
	// DirMkdir is what "dir.mkdir" makes (CapabilityDirMkdir).
	DirMkdir *DirMkdir `json:"dirMkdir,omitempty"`
	// Delegate is what "delegate" runs (CapabilityDelegate).
	Delegate *DelegateOptions `json:"delegate,omitempty"`
	// DelegateSend is what "delegate.send" sends to a live delegated session.
	DelegateSend *DelegateSendOptions `json:"delegateSend,omitempty"`
}

// DelegateOptions are the parameters of the "delegate" op
// (docs/MOD-016-peer-delegation.md).
type DelegateOptions struct {
	Agent           string `json:"agent,omitempty"`
	Dir             string `json:"dir,omitempty"`
	Name            string `json:"name,omitempty"`
	Prompt          string `json:"prompt"`
	Mode            string `json:"mode,omitempty"`
	Resume          string `json:"resume,omitempty"`
	From            string `json:"from,omitempty"`
	Keep            bool   `json:"keep,omitempty"`
	Async           bool   `json:"async,omitempty"`
	TimeoutMs       int    `json:"timeoutMs,omitempty"`
	CancelOnTimeout bool   `json:"cancelOnTimeout,omitempty"`
}

// DelegateSendOptions are the parameters of "delegate.send".
type DelegateSendOptions struct {
	SID       string `json:"sid"`
	Prompt    string `json:"prompt"`
	Async     bool   `json:"async,omitempty"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

// DelegateResult is the answer to "delegate", "delegate.send", and
// "delegate.result".
type DelegateResult struct {
	Session      string `json:"session"`
	Agent        string `json:"agent"`
	Mode         string `json:"mode,omitempty"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exitCode,omitempty"`
	AgentSession string `json:"agentSession,omitempty"`
	Result       string `json:"result,omitempty"`
	RawCapture   string `json:"rawCapture,omitempty"`
	Error        string `json:"error,omitempty"`
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

// LayoutCommand is a change of the layout that a client, as the CLI, the
// Rex API gateway or the app itself, asks for ("command"): the server makes it, on
// the layout it keeps, starting and ending the sessions of its panes. An
// action, but float.set_state, and config.reload are the window's: the app takes those in order ("command.take"), makes them and
// answers each ("command.done"). Panes are named by their session, and
// tabs by their lasting ID (Tab), or by a session of theirs.
type LayoutCommand struct {
	ID int64 `json:"id,omitempty"`
	// Tab names the tab of a tab.* command by its lasting ID
	// (layout.Tab.ID), in place of SID, and of a pane.split of no SID,
	// which splits the tab's focused pane, as one of no session; in
	// tab.create's answer, the ID of the tab made. An app that does not
	// know it answers by SID alone.
	Tab string `json:"tab,omitempty"`
	// Op is "space.create", "space.rename", "space.close", "tab.create",
	// "pane.split", "pane.close", "tab.close", "tab.rename",
	// "pane.rename", "pane.focus", "pane.zoom", "tab.move", "pane.resize",
	// "pane.swap", "pane.move", "pane.restart", "tab.set", "windows.set",
	// "layout.apply", "pane.teleporting", "pane.teleported", "kills.taken",
	// "show.taken", "tab.to_space", "space.home", "project.create",
	// "project.adopt", "project.move", "project.update", "spaces.rename",
	// "spaces.delete" or "adopt.taken".
	// space.home sets the folder Dir, absolute, of the workspace Space,
	// which makes it a project of this machine; a Dir of "" clears it.
	// Host must be "": a project of another host is made there
	// (remote_home), and a folder that is a project already fails with
	// "exists", Space naming it. The desktop of before docs/MOD-002-projects.md's
	// phase 2 sends it. A tab.create of no Dir in a project starts in its
	// folder, or fails with FolderMissing when it is gone, and starts no
	// session elsewhere (CapabilitySpaceHome).
	// project.create makes a project of the folder Dir named Name in the
	// space SpaceName, and its first tab in the folder; it fails with
	// "exists", Space naming the project, for a folder that is one, and
	// with FolderMissing for one that is not there. project.adopt arranges
	// sessions that run already into the tabs of Adopt, in its project,
	// starting none (CapabilityProjects). project.move moves the tab Tab
	// to the project ToSpace, or to the tabs with no project; project.update
	// renames the project Space (Name), colors it (Color) and moves it to
	// the space SpaceName; spaces.rename moves every project of space From
	// to space To, Index in the answer counting them; spaces.delete moves
	// every project of space From to Default and offers the name no more,
	// Index counting them. adopt.taken, a local
	// client's only, tells that the app's host adopted the adoption of ID
	// Target (layout.Store.Adopt), which the server forgets.
	// tab.to_space moves the tab, with its panes, their sessions, its
	// floats, focus and zoom as they are, to the space ToSpace, as the
	// app's Move to Space does: before the tab of Target there, else at
	// its end. The active tab of ToSpace stays, and a space the move
	// leaves with no tab goes, as the app's does.
	// pane.restart starts a new shell in the place of the pane of SID.
	// pane.teleported, a local client's only, tells of the teleport of the
	// pane of SID to the host of address Host (hosts.json), the session
	// Target there (Teleport): the server shows that session in the pane's
	// place, a tab of the host's. pane.teleporting, before the host starts
	// it, notes it in the layout (layout.Store.Teleports), so that the
	// window opens no tab of its own for it; with no Target, it drops the
	// note of a teleport that failed. kills.taken tells that the app took
	// Kills, the layout's sessions of other hosts to end
	// (layout.Store.Kills). tab.create with Focus in a space no window
	// shows notes it in the layout, with when (layout.Store.Show), for a
	// window to show it; show.taken tells that one did, for Space. tab.set,
	// windows.set, layout.apply, kills.taken and show.taken are a local
	// client's only too. The server makes its own changes too:
	// the pane of a session that ends goes, a session a remote client
	// starts here gets a tab, and so does one no pane has as the app opens;
	// the panes' facts follow their sessions.
	Op  string `json:"op,omitempty"`
	SID string `json:"sid,omitempty"`
	// Space is the workspace the command acts on, by ID; "" for the
	// window that has the commands, as a client of before spaces had it.
	// Of space.create, it is the ID the new space takes, unless a space
	// has it; a new one when "".
	Space string `json:"space,omitempty"`
	// After is the tab a new or moved tab goes after, the first of this
	// machine's tabs when "" for tab.move: of tab.create, by a session of
	// it; of tab.set, by its ID, the tab going to Index without one.
	After string `json:"after,omitempty"`
	// Dir is where a new pane starts, its tab's or its pane's when "".
	Dir string `json:"dir,omitempty"`
	// Command runs instead of the user's login shell in a new pane or tab.
	Command []string `json:"command,omitempty"`
	// Focus makes a new or moved tab the active one.
	Focus bool `json:"focus,omitempty"`
	// Vertical splits down, else right; Name is a tab's, a pane's or a
	// space's new name, and in the answer a pane's name as the app keeps
	// it; Mode is zoom's "on", "off" or "toggle".
	Vertical bool   `json:"vertical,omitempty"`
	Name     string `json:"name,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Direction is the edge pane.resize moves or the side of Target
	// pane.move puts the pane on, "left", "right", "up" or "down"; Amount
	// is resize's share of the split and Ratio the share of the moved
	// pane's split that its first side, left or above, takes. Target is
	// the other pane of a swap or a move, in the pane's tab or, for a
	// move, another.
	Direction string  `json:"direction,omitempty"`
	Amount    float32 `json:"amount,omitempty"`
	// Of tab.to_space, Target is the tab of ToSpace the tab goes before,
	// by its ID, else by a session of this machine it has; "" for the end.
	Target string  `json:"target,omitempty"`
	Ratio  float32 `json:"ratio,omitempty"`
	// ToSpace is the space tab.to_space moves the tab to, by ID; of
	// project.move, the project, "" for the tabs with no project.
	ToSpace string `json:"to_space,omitempty"`
	// Launchers sets a project's pinned agent CLI IDs on project.update
	// (nil leaves it unchanged; non-nil empty slice pins none).
	// ResetLaunchers resets it back to default (all detected CLIs).
	Launchers      *[]string `json:"launchers,omitempty"`
	ResetLaunchers bool      `json:"reset_launchers,omitempty"`
	// SpaceName is the space of a project (layout.Workspace.Space) that
	// project.create and project.update give it, "" for Default; nil
	// gives Default to a new one and keeps an existing one's.
	SpaceName *string `json:"space_name,omitempty"`
	// Adopt is what project.adopt arranges (layout.Adoption): its project
	// and its tabs, each a split of sessions of this machine.
	Adopt *layout.Adoption `json:"adopt,omitempty"`
	// From and To are the space names spaces.rename renames; From is the
	// space spaces.delete deletes.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Host is a host of the window's, by its address as hosts.json has
	// it, of the teleport commands, whose Target is the session there; of
	// space.home, the home host by its ID (layout.Workspace.Host).
	Host string `json:"host,omitempty"`
	// NewTab moves a pane into a tab of its own, named Name.
	NewTab bool `json:"new_tab,omitempty"`
	// Action is the action of "action", and Args its arguments. Remote
	// tells an action a remote client asked for.
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Remote bool            `json:"remote,omitempty"`
	// Index is where tab.move puts the tab among this machine's tabs as
	// the app makes it, counted with the tab in its place; in the answer,
	// where the tab is then. Of tab.set with no After, it is the tab's
	// place among all the tabs of its space.
	Index int `json:"index,omitempty"`
	// TabLayout is the tab tab.set puts in Space, by its ID: in place of
	// the tab of that ID, wherever it is, else as a new one. It arranges
	// the sessions the layout has; it neither starts nor ends one
	// (CapabilityLayoutServer).
	TabLayout *layout.Tab `json:"tabLayout,omitempty"`
	// Kills are the kills of the layout the app took (kills.taken), which
	// the server forgets.
	Kills []layout.Kill `json:"kills,omitempty"`
	// Spaces are the spaces windows.set says the windows show, in the
	// order of their places.
	Spaces []string `json:"spaces,omitempty"`
	// Color is the color space.create and space.rename give the space: a
	// preset's id (layout.Palette), a "#rrggbb", or "default" for the
	// default look; "" keeps it.
	Color string `json:"color,omitempty"`
	// Float makes the pane pane.split makes float over the tab's others.
	Float bool `json:"float,omitempty"`
	// CreatedBy and Agent record who created a pane or tab via CLI or
	// delegation, and the badge label it shows (docs/DSK-016-agent-pane-highlight.md).
	CreatedBy string `json:"createdBy,omitempty"`
	Agent     string `json:"agent,omitempty"`
	// Base is the revision (layout.Tab.Rev) of the tab a change is built
	// on, of tab.set, tab.close and pane.close: the server refuses it with
	// conflict once the tab is at another, as another client changed it
	// first; 0 checks nothing. Revs, in the answer, are the revisions of
	// the tabs the change changed, each one past the one before.
	Base uint64            `json:"base,omitempty"`
	Revs map[string]uint64 `json:"revs,omitempty"`
	// Batch are the changes of layout.apply, the app's of one save: of
	// space.create, space.rename, space.close, windows.set, tab.set,
	// tab.close and pane.close, each built on its tab as the layout had it
	// before (Base). The server makes all of them, in order, and keeps the
	// layout they make once, or none: a conflict of one refuses them all.
	// It checks the sessions on that layout: none in two panes, and none
	// that runs gone but those the batch closes.
	Batch []LayoutCommand `json:"batch,omitempty"`
	// The answer: Code and Error when it was not made, New the session of
	// the pane made, and Zoomed whether the pane's tab is zoomed now.
	// Changed and Reason answer pane.resize, pane.swap and pane.move.
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
	New     string `json:"new,omitempty"`
	Zoomed  bool   `json:"zoomed,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ClientInfo is one client attached to a session: a window, or a CLI
// reading it, and whether it came from a remote host.
type ClientInfo struct {
	SessionID string    `json:"sessionId"`
	Peer      string    `json:"peer,omitempty"`
	Principal string    `json:"principal,omitempty"`
	Remote    bool      `json:"remote,omitempty"`
	Seq       uint64    `json:"seq,omitempty"`
	Via       bool      `json:"via,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Age       int64     `json:"age,omitempty"` // seconds
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

// KeyInput is a key event of "key", as a client sends it: the physical
// key by its W3C KeyboardEvent.code ("KeyA", "ArrowUp"), what happened to
// it, the modifiers held and those that made its text, the text typed,
// the code point the key types without shift, and whether an input
// method is composing. The server encodes it for the program as the
// program's modes ask (terminal.Terminal.EncodeKey).
type KeyInput struct {
	Key string `json:"key"`
	// Action is "press", "release" or "repeat"; "" is "press".
	Action string `json:"action,omitempty"`
	// Mods and Consumed are the modifiers, joined with "+": shift, ctrl,
	// alt, super, caps_lock, num_lock, shift_side, ctrl_side, alt_side,
	// super_side.
	Mods     string `json:"mods,omitempty"`
	Consumed string `json:"consumed,omitempty"`
	Text     string `json:"text,omitempty"`
	// Unshifted is the code point the key types without modifiers, 0 for
	// none.
	Unshifted int  `json:"unshifted,omitempty"`
	Composing bool `json:"composing,omitempty"`
}

// MouseInput is a pointer event of "mouse", in cells counting from zero
// from the top left of the screen: a button pressed, released or held
// through motion, or the wheel, which "wheel" names in Button. The server
// encodes it for the program as the mouse modes ask
// (terminal.Terminal.EncodeMouse).
type MouseInput struct {
	// Action is "press", "release" or "motion"; "" is "press".
	Action string `json:"action,omitempty"`
	// Button is "none", "left", "right", "middle", or a wheel's "wheel_up",
	// "wheel_down", "wheel_left", "wheel_right".
	Button string `json:"button,omitempty"`
	Mods   string `json:"mods,omitempty"`
	// Col and Row are the cell of the pointer.
	Col int `json:"col,omitempty"`
	Row int `json:"row,omitempty"`
}

// FormatOptions choose what "format" shows of a session's screen.
type FormatOptions struct {
	// Kind is "text" for the screen and scrollback as plain text, with the
	// lines a program's long output wrapped joined, or "vt" for the escape
	// sequences that redraw it.
	Kind string `json:"kind,omitempty"`
}

// FormatResult is what "format" shows: the screen in the form asked for.
type FormatResult struct {
	Content []byte `json:"content"`
	Title   string `json:"title,omitempty"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// ModeState is one terminal mode, as a program leaves it.
type ModeState struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
	// ANSI tells an ANSI mode, set with ESC [ number h, from a DEC private
	// one, set with ESC [ ? number h.
	ANSI bool `json:"ansi,omitempty"`
	Set  bool `json:"set"`
	// Default tells a mode a new terminal has on, and a reset restores.
	Default bool `json:"default,omitempty"`
}

// ModeStatus is a terminal's modes of "modes": which screen shows, and
// every mode the server tracks.
type ModeStatus struct {
	// Screen is "primary", or "alternate" while mode 47, 1047 or 1049
	// switched the screen.
	Screen string      `json:"screen"`
	Modes  []ModeState `json:"modes"`
}

// InputMode is how a session's terminal handles typed input, of
// "inputMode": the tty's line discipline.
type InputMode struct {
	Echo      bool `json:"echo"`
	Canonical bool `json:"canonical"`
	Signals   bool `json:"signals"`
}

// ActionCall names an app action and its arguments.
type ActionCall struct {
	ID   string          `json:"id"`
	Args json.RawMessage `json:"args,omitempty"`
}

// RemoteClient is a client allowed to connect remotely: PublicKey is its
// tailcat node key, "nodekey:" and 64 hex digits.
type RemoteClient struct {
	Name      string    `json:"name"`
	PublicKey string    `json:"publicKey"`
	Created   time.Time `json:"created,omitzero"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
}

// RemoteStatus answers remote.status. Address, the tailcat address
// clients connect to while running, is a secret.
type RemoteStatus struct {
	Enabled   bool           `json:"enabled"`
	Running   bool           `json:"running"`
	Address   string         `json:"address,omitempty"`
	Allowed   []RemoteClient `json:"allowed"`
	LastError string         `json:"lastError,omitempty"`
	// Relay is the DERP relay of one's own the address names, "" for
	// tailcat's.
	Relay string `json:"relay,omitempty"`
	// PublicKey is the host's node public key, which a relay operator's
	// list names to let this machine on (docs/NET-004-public-relay.md);
	// "" until remote access has keys.
	PublicKey string `json:"publicKey,omitempty"`
	// ReachableAt is the address where this machine is its own relay, ""
	// while off; ReachableAddress, a secret as Address, the second tailcat
	// address while its relay runs, and ReachableError why it does not.
	ReachableAt      string `json:"reachableAt,omitempty"`
	ReachableAddress string `json:"reachableAddress,omitempty"`
	ReachableError   string `json:"reachableError,omitempty"`
	// ReachableFirewall is "blocked" while the relay runs and Windows
	// Firewall drops connections to it (firewall.go), "blockRule" when a
	// rule that blocks GoRex does, else "".
	ReachableFirewall string `json:"reachableFirewall,omitempty"`
	// Pairing is the pairing window, Pending the requests made in it.
	Pairing RemotePairing   `json:"pairing"`
	Pending []RemotePending `json:"pending,omitempty"`
	// PendingByAddress are the requests made by address (pairaddr.go),
	// apart from Pending: each is approved by its code alone.
	PendingByAddress []RemoteAddrPending `json:"pendingByAddress,omitempty"`
	// RemoteControl tells that remote clients may run app actions through
	// this window.
	RemoteControl bool           `json:"remoteControl"`
	Tailnet       *TailnetStatus `json:"tailnet,omitempty"`
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

// RemoteAddrPending is a request to pair by address, waiting for
// approval while its connection lasts: Code is what both sides show,
// Source the address it came from.
type RemoteAddrPending struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	RequestedAt time.Time `json:"requestedAt"`
}

// PairResult answers remote.pair: the fingerprint of the key the server
// saw, to compare with the one the server shows.
type PairResult struct {
	Fingerprint string `json:"fingerprint"`
}

// TailcatStatus answers remote.tailcatStatus: how the link to a host's
// address runs, as a disco ping through the server's tailcat client of
// it measured it. Direct is that the pong came over a direct path, whose
// UDP endpoint Endpoint names then; else the pong came through a relay,
// which Relay names: the host of a relay of one's own, a DERP region's
// code, or its number as "region N". LatencySeconds is the ping's
// round-trip.
type TailcatStatus struct {
	Direct         bool    `json:"direct"`
	Endpoint       string  `json:"endpoint,omitempty"`
	Relay          string  `json:"relay,omitempty"`
	RegionID       int     `json:"regionId,omitempty"`
	LatencySeconds float64 `json:"latencySeconds"`
}

// Response answers the Request with the same ID.
type Response struct {
	ID    int64           `json:"id"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	// ETag names the answer to "list": when it is the request's, Data is
	// left out, the list unchanged.
	ETag string `json:"etag,omitempty"`
	// Event, on a line of no ID, is one a subscribed connection gets.
	Event
}

// Event is a line a subscribed connection gets, with no ID. Kind is the
// change: "layout", as setLayout changes it, "sessions", as one starts,
// ends, rings its bell, or changes its size, size lock, title or
// foreground program, or "transfer", as a file transfer goes on.
type Event struct {
	Kind string `json:"event,omitempty"`
	// Revision, on a layout event, is the layout's revision after the
	// change (LayoutState).
	Revision uint64 `json:"revision,omitempty"`
	// Sessions carries what the session list changed to, with the status
	// records of each session that reported some.
	Sessions []EventSession `json:"sessions,omitempty"`
	// Transfer is the file transfer a transfer event tells of.
	Transfer *Transfer `json:"transfer,omitempty"`
	// Theme is the host terminal's default colors on a "theme" event.
	Theme *Theme `json:"theme,omitempty"`
	// Ports are the terminals whose listening sockets changed, on a
	// "ports" event.
	Ports []PortsEvent `json:"ports,omitempty"`
	// Prompt is the question a config action asks, or its end, on a
	// "prompt" event.
	Prompt *PromptEvent `json:"prompt,omitempty"`
}

// PromptSpec is a question a config action asks: one line of text, or one
// or more items of a list (docs/MOD-009-client-prompts.md).
type PromptSpec struct {
	// Kind is "input" or "pick".
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Placeholder string       `json:"placeholder,omitempty"`
	Value       string       `json:"value,omitempty"`
	Confirm     string       `json:"confirm,omitempty"`
	Multiple    bool         `json:"multiple,omitempty"`
	AllowOther  bool         `json:"allowOther,omitempty"`
	Items       []PromptItem `json:"items,omitempty"`
}

// PromptItem is one item of a pick.
type PromptItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

// PromptAnswer is what a client answers: text for an input or an
// allow_other pick, IDs for a pick, or Cancelled.
type PromptAnswer struct {
	Text      string   `json:"text,omitempty"`
	IDs       []string `json:"ids,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
	// Unsupported says that this client cannot show the question: it is
	// removed from it, and the others keep it.
	Unsupported bool `json:"unsupported,omitempty"`
}

// PromptEvent is a "prompt" event: a question the client shows, or the
// end of one that another client answered, or that timed out or was
// cancelled.
type PromptEvent struct {
	// Op is "requested" or "ended".
	Op     string      `json:"op"`
	CallID string      `json:"callId"`
	Spec   *PromptSpec `json:"spec,omitempty"`
	// TimeoutMS is how long the action waits, on "requested".
	TimeoutMS int `json:"timeoutMs,omitempty"`
	// Reason is "answered", "timeout" or "cancelled", on "ended".
	Reason string `json:"reason,omitempty"`
	// Action names the action that asks.
	Action string `json:"action,omitempty"`
}

// Theme is the host terminal's default colors, as hex "#rrggbb" strings.
type Theme struct {
	Foreground string   `json:"foreground,omitempty"`
	Background string   `json:"background,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
	Palette    []string `json:"palette,omitempty"`
}

// EventSession is one session in an Event.
type EventSession struct {
	ID          string          `json:"id"`
	Status      *ProgramStatus  `json:"status,omitempty"`
	Statuses    []ProgramStatus `json:"statuses,omitempty"`
	StatusSeen  time.Time       `json:"statusSeen,omitzero"`
	ProgramIcon *ProgramIcon    `json:"programIcon,omitempty"`
}

// LayoutState is the layout the server keeps, with its revision: a
// client sends the revision it has to setLayout, and gets an error when
// another client changed the layout since.
type LayoutState struct {
	Layout   json.RawMessage `json:"layout"`
	Revision uint64          `json:"revision"`
}

// Session actor kinds recorded in AgentActor.CreatedBy
// (docs/DSK-016-agent-pane-highlight.md).
const (
	ActorHuman    = "human"
	ActorCLI      = "cli"
	ActorDelegate = "delegate"
)

// AgentActor records who created a session, when the CLI or peer delegation
// last sent input to it, the short agent badge label, and whether the agent
// mark is still active (cleared as soon as a person types in the UI).
type AgentActor struct {
	CreatedBy      string    `json:"createdBy"`
	LastCLIInputAt time.Time `json:"lastCliInputAt,omitzero"`
	Agent          string    `json:"agent,omitempty"`
	Active         bool      `json:"active,omitempty"`
}

// NormalizeAgentName trims and bounds an agent badge label so scripts cannot
// inject control characters or excessively wide strings into tab titles.
func NormalizeAgentName(s string) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) {
			continue
		}
		if count >= 24 {
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

// CreateOptions start a session.
type CreateOptions struct {
	// Command runs instead of the user's login shell.
	Command []string `json:"command,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	// DirStrict starts the session in Dir or not at all: a Dir that is
	// not a directory the server can read fails the create, its error
	// starting with FolderMissing, and starts no program. Without it a
	// session whose Dir is gone starts at home, as one of a layout does
	// after a restart (CapabilitySpaceHome).
	DirStrict bool     `json:"dirStrict,omitempty"`
	Env       []string `json:"env,omitempty"`
	Cols      int      `json:"cols,omitempty"`
	Rows      int      `json:"rows,omitempty"`
	// CreatedBy ("human", "cli", or "delegate"), Agent, and StampInput
	// record CLI or delegated actor ownership at creation
	// (docs/DSK-016-agent-pane-highlight.md).
	CreatedBy  string `json:"createdBy,omitempty"`
	Agent      string `json:"agent,omitempty"`
	StampInput bool   `json:"stampInput,omitempty"`
	// prelude is text the session's screen shows before the program's
	// first output: a teleported pane's scrollback; id the session's id,
	// a new one when "": a teleport's, its hop's.
	prelude []byte
	id      string
}

// SessionInfo describes a session and what runs in it now.
type SessionInfo struct {
	ID    string `json:"id"`
	PID   int    `json:"pid"`
	Shell string `json:"shell"`
	// Command tells a session started with a command, Shell its program,
	// rather than the user's shell.
	Command bool      `json:"command,omitempty"`
	Created time.Time `json:"created"`
	// Program is the name of the program in the foreground, and Args its
	// arguments; Idle tells that it is the shell itself, at its prompt.
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
	Idle    bool     `json:"idle"`
	// Foreground is the ID of the process that has the terminal in the
	// foreground, the program the shell runs, or 0 when there is none.
	Foreground int `json:"foreground,omitempty"`
	// Dir is the working directory of the program in the foreground.
	Dir string `json:"dir"`
	// AgentSession is the conversation Claude Code has open in the
	// foreground, and AgentConfigDir its config dir when not the default.
	// For omp, they are its conversation and its data dir, and agentFile
	// the conversation's file.
	AgentSession   string `json:"agentSession,omitempty"`
	AgentConfigDir string `json:"agentConfigDir,omitempty"`
	agentFile      string
	// ran tells that a program ran in the foreground since the session
	// started (session.facts): a shell started again after a crash ran
	// none yet.
	ran bool
	// Title is the last title a program set (OSC 0, OSC 2).
	Title      string    `json:"title,omitempty"`
	LastOutput time.Time `json:"lastOutput"`
	LastInput  time.Time `json:"lastInput"`
	Output     uint64    `json:"output"`
	Bells      int       `json:"bells"`
	Exited     bool      `json:"exited"`
	ExitCode   int       `json:"exitCode"`
	Attached   int       `json:"attached"`
	// ConPTY tells that the session's console is a ConPTY, which drops the
	// OSC that a terminal's paste event is: a paste reaches its program as
	// the text of a "paste" request.
	ConPTY bool `json:"conpty,omitempty"`
	Cols   int  `json:"cols"`
	Rows   int  `json:"rows"`
	// SizeLock is the owner that holds the session's size (lockSize), ""
	// while the windows set it.
	SizeLock string `json:"sizeLock,omitempty"`
	// SizedBy is the id (Attach.Client) of the client whose terminal the
	// session's size follows (the authority): the one holding the size
	// lock, that typed or claimed the focus last, or that attached last,
	// one that gave a size before one that gave none.
	// "" while no client attached with one, as a version 2 client does
	// not.
	SizedBy string `json:"sizedBy,omitempty"`
	// Owner is the public key of the remote client that created the
	// session, "" for one created locally; OwnerName is its paired name
	// when known.
	Owner     string `json:"owner,omitempty"`
	OwnerName string `json:"ownerName,omitempty"`
	// DelegatedFrom names the originating host/session for a delegated
	// coding-agent tab, and DelegatedAgent is its agent CLI ID
	// (docs/MOD-016-peer-delegation.md).
	DelegatedFrom  string `json:"delegatedFrom,omitempty"`
	DelegatedAgent string `json:"delegatedAgent,omitempty"`
	// AgentActor records who created the session and when the CLI last
	// sent input to it (docs/DSK-016-agent-pane-highlight.md).
	AgentActor AgentActor `json:"agentActor"`
	// Statuses are what the programs said they do (OSC 7501), by id, and
	// Status the one that most needs the user (MostUrgent); nil when they
	// said nothing. StatusSeen is when the latest done or error record a client showed
	// the user was updated ("seeStatus"): MostUrgent(Statuses,
	// StatusSeen) is what still needs the user. Zero from a server before
	// CapabilityStatusSeen.
	Statuses    []ProgramStatus `json:"statuses,omitempty"`
	Status      *ProgramStatus  `json:"status,omitempty"`
	StatusSeen  time.Time       `json:"statusSeen,omitzero"`
	ProgramIcon *ProgramIcon    `json:"programIcon,omitempty"`
}

// ProgramIcon is a custom icon reported by a program (OSC 7502).
type ProgramIcon struct {
	Format string `json:"format"`
	Mode   string `json:"mode,omitempty"`
	Data   string `json:"data"`
	Bg     string `json:"bg,omitempty"`
	Fg     string `json:"fg,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

// ProgramName is the program's name for showing: what it reported through
// the status protocol, else the name of its executable.
func (s SessionInfo) ProgramName() string {
	if s.Status != nil && s.Status.App != "" {
		return s.Status.App
	}
	return s.Program
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
	// LastMsg is the last message a working report for the record
	// carried: what the program did last, kept when a later report
	// carries none, as Claude Code reports done without one.
	LastMsg string `json:"last_msg,omitempty"`
}

// Message is what a record tells: its message, else, once it is done
// or failed, what it did last. A working or blocked record without one
// tells nothing: its last message is of an earlier step.
func (s *ProgramStatus) Message() string {
	if s.Msg != "" || s.State != "done" && s.State != "error" {
		return s.Msg
	}
	return s.LastMsg
}

// Hello is the server's answer to "hello".
type Hello struct {
	Version int       `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Host    HostInfo  `json:"host"`
	// Machine names the server's machine for good (MachineID), "" from a
	// server before it.
	Machine string `json:"machine,omitempty"`
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
	// Client is the remote client this connection is, with its label and
	// the times it was allowed and last connected; nil for a local one.
	Client *RemoteClient `json:"client,omitempty"`
	// RexPairURL is a ready mobile endpoint, shown only to a verified system Tailscale peer.
	RexPairURL string `json:"rexPairURL,omitempty"`
}

// Can tells whether the server does capability, as "layout.revision" or
// CapabilityShutdownRestart. A server of before version 3 says none.
func (h Hello) Can(capability string) bool {
	return slices.Contains(h.Capabilities, capability)
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
	// Snapshot, when "ghostty" (SnapshotGhostty), sends the screen in
	// Ghostty's binary snapshot format instead of the escape sequences
	// that replay it: a framed attach then says so in
	// SnapshotHeader.Format, and the title and Kitty graphics the escape
	// sequences carry come after it as output. History does not apply:
	// the binary snapshot holds the whole scrollback.
	Snapshot string `json:"snapshot,omitempty"`
	// Focused tells that the user looks at this terminal: the session
	// takes its size at once, though another client typed last. A focus
	// request sent once the attach is answered may reach the server
	// before it registers the attach, which this one never does.
	Focused bool `json:"focused,omitempty"`
	// Client is an id of the client's own: SessionInfo.SizedBy names the
	// session's authority by it, which tells a window whether the size
	// the program draws for is its own.
	Client string `json:"client,omitempty"`
	// Mute tells that the client's terminal answers none of the program's
	// queries, as the rexapi gateway, which types through the input ops:
	// while it is the session's authority, the server's emulator answers.
	Mute bool `json:"mute,omitempty"`
}

// FilePut is the first line of a connection that sends a file into a
// session's directory (CapabilityFileTransfer). The server answers a
// FileAnswer: the length of what it has of the file from an earlier try
// (Offset) and the SHA-256 of it (Sum). The client answers
// {"offset":Offset} to go on from there, or {"offset":0} when its own
// first bytes differ, then sends the rest of the Size bytes as they are.
// The server keeps them in a hidden file beside the target,
// ".gorex-HASH.partial", HASH the first 16 hex digits of the SHA-256 of
// the name, a NUL and the ID, until they are all there, then moves it into
// place and answers a FileAnswer with Path. A connection lost on the way
// leaves the hidden file for the next try; a put into the directory a
// day after it was last written removes it.
type FilePut struct {
	Op  string `json:"op"` // "file.put"
	SID string `json:"sid"`
	// Dir is where the file goes: absolute, of the server user's home
	// directory when it is "~" or starts with "~/", or relative to the
	// directory of the session's program in the foreground
	// (SessionInfo.Dir), which it is when "".
	Dir string `json:"dir,omitempty"`
	// Name is the file's name, without a directory.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// ID is the client's id of the transfer, of letters, digits, "_" and
	// "-", at most 64: a try again of the same file uses the same.
	ID string `json:"id"`
	// Conflict is what to do with a file of the name there already:
	// "rename" (the default) names the new one "name (1).ext", or the
	// first number free; "overwrite" replaces it; "skip" keeps it and
	// takes no file.
	Conflict string `json:"conflict,omitempty"`
	// Abort, set, sends no file: the server removes the partial of Name
	// for ID in Dir, unless a transfer writes it yet, and answers OK. With
	// Dir absolute, as the put's first answer named it (FileAnswer.Dir),
	// the session need not be there any more, for a partial a put of the
	// same client opened since the server started; one of before a
	// restart a put into its directory removes a day after.
	Abort bool `json:"abort,omitempty"`
	// Temp, set, sends the file into the server user's own folder in the
	// temporary directory instead of Dir (CapabilityFileTemp). A server
	// before it ignores it and uses Dir.
	Temp bool `json:"temp,omitempty"`
	// Drop is the client's id of this sending of the file, the same for
	// each of its tries, which ID is not: a try of a Drop the server
	// finished lately, whose answer was lost, answers where its file went,
	// Skipped, and takes no file. A server before it takes the file again.
	Drop string `json:"drop,omitempty"`
}

// FileGet is the first line of a connection that takes a file from a
// session's machine (CapabilityFileTransfer). The server answers a
// FileAnswer with Size, Name and MTime, then sends the file's bytes from
// Offset to Size, and closes the connection.
type FileGet struct {
	Op  string `json:"op"` // "file.get"
	SID string `json:"sid"`
	// Path is the file's: absolute, of the server user's home directory
	// when it starts with "~/", or relative to the directory of the
	// session's program in the foreground. A regular file only.
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	// ID, when set, is the client's id of the transfer, as FilePut's,
	// which the transfer events name it by.
	ID string `json:"id,omitempty"`
}

// DirList asks for the directories in a directory of the server's
// machine ("dir.list", CapabilityDirList), for a folder picker: their
// names only, no files. The server answers a DirListResult.
type DirList struct {
	// Path is the directory: absolute; "" or "~" for the home directory;
	// "~/x" and a relative path are of the home directory. It is cleaned,
	// so ".." goes up a name, then its symlinks are resolved; Windows'
	// junctions are kept as they are. DirTop names the top of the
	// machine: "/" on macOS and Linux, and on Windows the list of its
	// drives, whose names are their roots, as `C:\`. On Windows, a drive
	// with no root after it, as "C:x", is of the drive's root (`C:\x`),
	// and a path rooted with no drive (`\x`, "/x") or an incomplete share
	// ("//", `\\server`) answers DirNotFound.
	Path string `json:"path,omitempty"`
	// Hidden lists hidden directories too: those whose name starts with a
	// dot, and on Windows those of the hidden attribute.
	Hidden bool `json:"hidden,omitempty"`
}

// DirListResult is the answer to "dir.list". A directory that is not
// there, or is not a directory, answers Code DirNotFound, and one the
// server may not read DirDenied, with Path, Parent and Home still set,
// so that a picker can go elsewhere; any other failure is an error, as a
// listing that takes over 10 seconds, on a mount that hangs, or one over
// the few a connection or the server runs at once (DirListBusy). A
// listing past its deadline goes on counting toward the server's limit
// till the system returns it: a host with mounts that hang may refuse
// listings till they return, or the server restarts.
type DirListResult struct {
	// Path is the directory listed: absolute, cleaned, its symlinks
	// resolved but not Windows' junctions; DirTop for Windows' drives.
	// A path through a loop of symlinks answers DirNotFound.
	Path string `json:"path"`
	// Parent is the directory above Path, "" at the top: of a drive's
	// root on Windows, it is DirTop.
	Parent string `json:"parent"`
	// Home is the home directory, its symlinks resolved.
	Home string `json:"home"`
	// Dirs are the names of the directories in Path, symlinks and
	// junctions to one among them, sorted without regard to case, at most
	// DirListMax.
	Dirs []string `json:"dirs"`
	// More tells that Path holds more than DirListMax: Dirs are the first.
	More bool   `json:"more"`
	Code string `json:"code,omitempty"`
}

// DirMkdir asks the server to make a directory on its machine
// ("dir.mkdir", CapabilityDirMkdir), for the folder picker that creates
// the folder it is to use; the folders under it are made as needed. The
// server answers with an error only.
type DirMkdir struct {
	// Path is the directory to make, as DirList.Path reads it: absolute;
	// "~" and "~/x" of the home directory; a relative path of the home
	// directory. DirTop is no answer.
	Path string `json:"path,omitempty"`
}

// AgentInfo describes a known agent CLI installed on the server's machine
// ("agents.list", CapabilityAgents, docs/MOD-010-agent-launchers.md).
type AgentInfo struct {
	// ID is the canonical program key matching programs.go (for example
	// "claude", "codex", "agy", "omp", "devin").
	ID string `json:"id"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// Command is the argv that launches the agent directly so exiting it
	// ends the session.
	Command []string `json:"command"`
}

// AgentsResult is the answer to "agents.list".
type AgentsResult struct {
	Agents []AgentInfo `json:"agents"`
}

// DirTop is the DirList.Path of the top of a machine: on Windows, the
// list of its drives.
const DirTop = "/"

// DirListMax is how many names a DirListResult holds at most.
const DirListMax = 2000

// The codes of a DirListResult.
const (
	DirNotFound = "not_found"
	DirDenied   = "denied"
)

// FileAnswer is a line of the server on a file.put or file.get
// connection: Error, after which it closes it, or OK with what the step
// tells. A number left out is 0.
type FileAnswer struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	// Path is where a put's file is, or the file kept when Skipped.
	Path    string `json:"path,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	// Offset and Sum are what a put has of the file already: its length,
	// and the SHA-256 of it in hex.
	Offset int64  `json:"offset,omitempty"`
	Sum    string `json:"sum,omitempty"`
	// Dir is the directory a put's file goes into, absolute, in its
	// first answer: an abort names it (FilePut.Abort).
	Dir string `json:"dir,omitempty"`
	// Size, Name and MTime are the file a get sends.
	Size  int64     `json:"size,omitempty"`
	Name  string    `json:"name,omitempty"`
	MTime time.Time `json:"mtime,omitzero"`
}

// Transfer is a file transfer as a "transfer" event tells of it: Direction
// is "in" for a put and "out" for a get, Done the bytes there so far, and
// State "active", then "done", "failed" or "skipped". Path is the file's,
// once known. The events of an active transfer come at most every 250 ms;
// the last state always comes.
type Transfer struct {
	ID        string `json:"id"`
	SID       string `json:"sid"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Direction string `json:"direction"`
	Size      int64  `json:"size"`
	Done      int64  `json:"done"`
	State     string `json:"state"`
}

// SnapshotHeader is the line before the snapshot of a framed attach: its
// length in bytes, the session's size it was taken at, whether scrollback
// was cut (Attach.History), and, when Format is "ghostty", that the
// length counts Ghostty's binary snapshot and not escape sequences.
type SnapshotHeader struct {
	Snapshot  int    `json:"snapshot"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Truncated bool   `json:"truncated,omitempty"`
	Format    string `json:"format,omitempty"`
}
