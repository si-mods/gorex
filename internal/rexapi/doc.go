// Package rexapi serves Rex's API to Rex clients, over GoRex's own
// session server: a GoRex workspace is a Rex session, a tab is a window,
// and a pane with a local session is a block.
//
// The wire is Rex's own. A control connection at /api/control carries
// JSON messages, a data connection at /api/data streams a block's output,
// and /api/help, /llms.txt and /llms.html describe the API. The package
// speaks revision 12; the documentation in testdata is revision 10's, and
// /llms.txt appends what revisions 11 and 12 add, then GoRex's own
// methods (gorex.move_window, gorex.see_block, gorex.merge_windows,
// gorex.move_block_to_new_window). Revision 8 added
// session.bootstrap's data_tickets, which hands a client the tickets
// data.connect would issue, so a terminal opens without a round trip
// first; revision 9 added session.set_layer_state and a layer's state, so
// a floating layer can be minimized; revision 10 added built-in
// Tailscale's control_url, for a control server such as Headscale;
// revision 11 added terminal uploads; and revision 12 added
// server.status's listeners, the endpoints the server accepts
// connections on.
//
// The server reads the layout from the GoRex server the app keeps
// (internal/rex), maps it, and tells its clients of the changes it sees.
// A method that changes the layout asks the server for it
// (rex.LayoutCommand through the server's "command" op), which makes it,
// and answers once the layout shows the change.
//
// Input reaches a block through the server's new input ops: "key" encodes
// a W3C key event with the session's modes (terminal.Terminal.EncodeKey),
// "mouse" and "focusReport" the pointer and the focus, and a paste goes
// through the server's "paste". The session's size follows the client
// that owns the block, through the server's size locks: the gateway takes
// the lock for that client, renews it every five seconds, and gives the
// block to the next client in line when its owner gives it up, detaches,
// or drops.
//
// # Deviations
//
// Where GoRex cannot do what Rex does, the method fails with the code the
// doc gives the closest meaning to, and says why. These are the
// deviations, by method:
//
// | Method | What GoRex does instead |
// | --- | --- |
// | session.move | not_supported: sessions are GoRex's workspaces, in the layout's order; gorex.move_window, GoRex's own, moves a window to another session |
// | session.bootstrap (data_tickets) | a ticket for each terminal block of the sessions it attaches, since every GoRex block is a terminal; a request that names another creator gets no ticket for it, and the same fallback to data.connect applies |
// | session.view (layers) | GoRex's floating panes are floating layers of one block each, after the tiled layer; a hidden pane is a minimized layer |
// | session.set_layer_state | a floating layer is a floating pane: minimizing hides it and gives its focus away, and focus raises it and gives it the focus; the tiled layer cannot be minimized |
// | session.new_layer, session.close_layer, session.float_block, session.detach_block, session.resize_layer, session.raise_layer, session.lower_layer | not_supported: the app takes no layout commands for floating panes, and GoRex has no detached blocks |
// | session.help | lists every method this server implements, not only a session's |
// | session.list_clients | connected_at is when the client connected, not when it attached to this session |
// | session.new_split | conflict when the anchor names a split (GoRex splits blocks), and when side is before and the layout holds more than one block |
// | session.move_block | conflict when the block's window or the anchor's is zoomed, where Rex ends the zoom; an anchor in a floating layer fails with not_found; the moved block takes the focus of the anchor's window whether or not it had the focus, and that window becomes active only when the move closes the active window the block left; a window whose last tiled block moves away makes its topmost floating layer its tiled one; the new split's ratio is kept between 0.1 and 0.9; a split's ID names the blocks under it, so every split above the block's old and new places gets a new ID too |
// | session.swap_blocks | conflict across windows: GoRex swaps blocks of one tab |
// | session.resize_pane | the app keeps a split's ratio between 0.1 and 0.9; the revision is unchanged when the ratio did not move; a border is moved from the block beside it, and one no block's edge reaches fails with conflict |
// | session.create, session.new_window, session.new_split | a layout's splits are made by dividing the space of the block made before them, so nested splits come out right-nested; the blocks' order, labels and each pair's ratio are kept |
// | session.set_label | an empty label gives the layout's own name ("Session xxxxxxxx"), not a name the server picks |
// | session.set_block_label | an empty label gives the pane's default ("shell"), not "Block xxxxxxxx" |
// | session.close_window, block.close | closing the last tab leaves the space empty, as a Rex session goes on with no windows; a GoRex window that showed it shows another space or closes |
// | window IDs | named by the GoRex tab's lasting ID (layout.Tab.ID), so they stay across restarts of the gateway and the app; a tab of an app before tab IDs is named by its first session |
// | session.create | GoRex sets no limit on sessions |
// | com.superlogical.terminal.format | text is always trimmed and unwrapped; html is a plain <pre>; the extra_* flags are accepted, and the vt form carries the state GoRex keeps |
// | com.superlogical.terminal.pwd | the foreground process's directory, as a plain path: GoRex keeps no reported directory separately |
// | com.superlogical.terminal.title | no owner, which GoRex does not know |
// | com.superlogical.terminal.process | the fields GoRex can read; path, argv0 and script are left out, and ancestors is left out |
// | com.superlogical.terminal.program_status | the owner is the foreground process when a report arrives; last_msg, GoRex's own, is the last msg a report for the record carried, kept when a later one carries none; seen_at, GoRex's own beside records, here and in program_status_changed, is when the latest done or error record a client saw was updated (gorex.see_block), which the help documents add to the result's schema |
// | com.superlogical.terminal.modes | the modes GoRex follows (trackedModes in internal/rex), not every mode Ghostty keeps |
// | com.superlogical.terminal.input_mode | not_found on Windows: a ConPTY has no line discipline |
// | com.superlogical.terminal.clipboard_read | not_found: GoRex keeps no clipboard copies |
// | com.superlogical.terminal.set_theme | the colors are kept per block and sent as a theme message on its stream; GoRex's emulator keeps its own colors |
// | com.superlogical.terminal.resize, .size | the pixel size of a cell is ignored, and reported as 0 |
// | com.superlogical.terminal.write, block.event | a session whose program ended answers ErrSessionEnded as internal |
// | client.key | prefix reaches no Lua binding, so only key goes to the block; handled is always false |
// | client.keymap | empty: GoRex has no configuration file nor Lua |
// | client.set_script | not_supported when it asks for scripts; an empty list succeeds |
// | client.action | block methods only, named com.superlogical.terminal.*; Lua actions and other servers fail with not_found and not_supported |
// | client.perform | not_supported: GoRex does not ask clients to perform actions |
// | client.reply | not_found: nothing waits for a reply |
// | block.event | a mouse event's payload is action, button, mods, column and row, with revision 11's pixel (validated, and left unused, since GoRex keeps no pixel size for a cell) and buttons (the first held button becomes the event's button); a wheel event's payload is revision 11's deltas (delta_mode, delta_x, delta_y, delta_z), which the server turns into its discrete wheel events, one per line, where a page is 10 lines and a pixel is a 40th of one, and older clients' direction is still taken |
// | block_creator.list, session.describe_block | api.events lists GoRex's terminal events (`bell`, `title_changed`, `pwd_changed`, `process_changed`, `program_status_changed`, `program_status_removed`, `child_exited`), which Rex's help document leaves out |
// | server.config_reload | no configuration file: counts a generation and sends keymap_changed |
// | hosts.list | an empty list; hosts.put and hosts.remove fail with not_supported |
// | server.tailscale.* | built-in Tailscale is tsnet, a device of the gateway's own named <hostname>-gorex; the methods are for the local Unix socket only (403 elsewhere), as Rex's are; on Windows, where the socket gives no credentials, its file's permissions are the boundary |
// | server.status, session.bootstrap (server_status) listeners | the local socket (primary), --listen's address, and the local network's while it listens, but not the tailcat address, a secret; on Windows the socket's admission is left out, since its file's permissions, not credentials, are the boundary. A gateway that serves no listener of its own, as in tests, leaves the list out. The help document is revision 10's: help.all and session.help add the list's schema for a client at revision 12 or later; /api/help serves the revision 10 document as it is |
// | server.status, session.bootstrap (server_status), tailscale_status_changed | tailscale.auth_url only over the local Unix socket: the sign-in link binds the device to whoever opens it first, and a new link alone sends the other transports no tailscale_status_changed |
// | block_owner_changed | two reasons of GoRex's own: taken (a desktop pane took the session's size, or the lock's lease ran out, so the block has no owner) and input (a client that took the block typed into it, and took it again) |
// | data stream (terminal_reset) | a stream far behind its connection, or whose attach the GoRex server ended, starts over in the middle: ESC \, RIS and clears, a resize message, terminal_reset (GoRex's own), the screen as escape sequences with up to 256 KiB of scrollback, then theme and child_exited as they apply; a client sets again what it set on its terminal, as the cursor style (docs/rex-api.md) |
// | session.own_block, session.disown_block, session.create | need the GoRex server to keep the gateway's connection alive, which the size locks take; otherwise not_supported |
// | welcome (features), /api/upload | GoRex's own, which Rex has not: a POST of a file (application/octet-stream, with a Content-Length) to /api/upload?block_id=…&name=… puts it into the GoRex server user's temporary folder, as the app puts a file dropped on a pane of a host, and answers its path and the path as a word of the block's shell; the welcome lists the feature gorex.upload when the server takes such files, and with it gorex.upload.deflate-raw: the body may then come as Content-Encoding deflate-raw (raw DEFLATE, RFC 1951, no zlib or gzip wrapper), the file's size in the query's size, which the body must inflate to exactly, and the file limit holds on that size; any other encoding is a 415. Revision 11's com.superlogical.terminal.upload_begin and its rex.data.bytes.v1 stream are implemented (see upload.go) and take the same limits; the upload's file limits, and the conversions of a page (10 lines) and a pixel (a 40th of a line), are GoRex's own, which Rex's document does not name |
// | welcome (features), /api/voice | GoRex's own, which Rex has not: a WebSocket to /api/voice?block_id=… carries the phone's microphone as binary messages of raw PCM (signed 16-bit little-endian, 16 kHz, mono) to the rec the block's program runs, as Claude Code's /voice does on a host with no microphone, until the client closes it (rex.Voice, voice.go). Deprecated: nothing on the host reads a voice any more, since GoRex's rec is gone, so the welcome no longer lists the feature gorex.voice, and the endpoint will go |
// | welcome (features), gorex.move_window | GoRex's own, which Rex has not: the welcome lists gorex.move_window when the GoRex server moves a tab to another space (rex.CapabilityTabToSpace), and the method gorex.move_window moves a window (session_id, window_id) to another session (to_session_id), before one of its windows (before_window_id) or at its end, as the desktop's Move to Space does (movewindow.go): the window keeps its ID, label, layout, floating layers, focus and zoom, its blocks their IDs and programs; the other session's active window stays; a session the move empties is destroyed, as the desktop's space goes with its last tab; an owner of a block that is not attached to the session it went to lets it go (block_owner_changed, reason disconnect). The result is the left session's revision (left out once destroyed), to_revision, and session_destroyed. session.help, help.all and /llms.txt describe it after Rex's methods; /api/help does not |
// | welcome (features), gorex.merge_windows, gorex.move_block_to_new_window | GoRex's own, which Rex has not (mergewindows.go): the welcome lists gorex.merge_windows when the GoRex server takes a window's layout (rex.CapabilityLayoutServer), and the method puts a window's (window_id) layout beside another's (into_window_id), a horizontal split half each, and closes it, as the desktop's merge of a tab dropped on another's middle; the into window keeps its ID and label, takes the floating layers and the focus, ends its zoom and becomes active, all in one layout.apply that checks both windows' revisions; windows of different hosts' panes, a merged window with a pane of no terminal (a note, a browser), and windows changed meanwhile fail with conflict. gorex.move_block_to_new_window, always listed, moves a tiled block (block_id) of a window of two blocks or more into a new window of its own after it, as the desktop's Move Pane to New Tab, and answers its window_id; the active window stays. The blocks keep their IDs and programs. session.help, help.all and /llms.txt describe them after Rex's methods; /api/help does not |
// | welcome (features), gorex.see_block | GoRex's own, which Rex has not: the welcome lists gorex.see_block when the GoRex server keeps when a session's status records were seen (rex.CapabilityStatusSeen), and the method gorex.see_block (session_id, block_id, seen_at) tells it that the user saw the block's records updated till seen_at, as the desktop's pane does when it shows (seen.go): a time of a whole second covers that second, since updated_at has whole seconds; the seen time only moves forward, never past the latest done or error record, and a change sends program_status_changed with seen_at. Every client, the desktop's panes too, reads the same seen time, so a done record seen on one device is seen on all, and stays seen across restarts and server upgrades. The result is an empty object. session.help, help.all and /llms.txt describe it after Rex's methods; /api/help does not |
// | welcome (features), /api/spaces | GoRex's own, which Rex has not: the welcome lists gorex.spaces, and a GET of /api/spaces answers machine_id, this machine's lasting ID (rex.MachineID), and for each session whose space holds panes of other hosts its session_id, revision and remote_blocks, each a machine and the block_id that machine's own gateway gives the pane (spaces.go); no address, key nor name of a host. The views stay Rex's, without those panes, and a change of them is a new revision of the session |
//
// block_event events are sent for what the gateway sees change while it
// watches a block; the state a block had before the gateway started is
// not replayed.
package rexapi
