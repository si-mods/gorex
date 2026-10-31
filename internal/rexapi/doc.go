// Package rexapi serves Rex's API to Rex clients, over GoRex's own
// session server: a GoRex workspace is a Rex session, a tab is a window,
// and a pane with a local session is a block.
//
// The wire is Rex's own. A control connection at /api/control carries
// JSON messages, a data connection at /api/data streams a block's output,
// and /api/help, /llms.txt and /llms.html describe the API. The package
// speaks revision 7, the revision of the documentation in testdata.
//
// The server reads the layout from the GoRex server the app keeps
// (internal/rex), maps it, and tells its clients of the changes it sees.
// A method that changes the layout asks the app for it, as internal/control
// does (rex.LayoutCommand through the server's "command" op), and answers
// once the layout shows the change; with no GoRex window open, it fails
// with conflict.
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
// | session.move | not_supported: sessions are GoRex's workspaces, in the layout's order |
// | session.view (layers) | GoRex's floating panes are floating layers of one block each, after the tiled layer; a hidden floating pane is shown in no layer |
// | session.new_layer, session.close_layer, session.float_block, session.detach_block, session.resize_layer, session.raise_layer, session.lower_layer | not_supported: the app takes no layout commands for floating panes, and GoRex has no detached blocks |
// | session.help | lists every method this server implements, not only a session's |
// | session.list_clients | connected_at is when the client connected, not when it attached to this session |
// | session.new_split | conflict when the anchor names a split (GoRex splits blocks), and when side is before and the layout holds more than one block |
// | session.move_block | conflict when side is before (the app places a moved block after its anchor) |
// | session.swap_blocks | conflict across windows: GoRex swaps blocks of one tab |
// | session.resize_pane | the app keeps a split's ratio between 0.1 and 0.9; the revision is unchanged when the ratio did not move; a border is moved from the block beside it, and one no block's edge reaches fails with conflict |
// | session.create, session.new_window, session.new_split | a layout's splits are made by dividing the space of the block made before them, so nested splits come out right-nested; the blocks' order, labels and each pair's ratio are kept |
// | session.set_label | an empty label gives the layout's own name ("Session xxxxxxxx"), not a name the server picks |
// | session.set_block_label | an empty label gives the pane's default ("shell"), not "Block xxxxxxxx" |
// | session.close_window, block.close | the app's last tab cannot close: conflict |
// | session.create | GoRex sets no limit on sessions |
// | com.superlogical.terminal.format | text is always trimmed and unwrapped; html is a plain <pre>; the extra_* flags are accepted, and the vt form carries the state GoRex keeps |
// | com.superlogical.terminal.pwd | the foreground process's directory, as a plain path: GoRex keeps no reported directory separately |
// | com.superlogical.terminal.title | no owner, which GoRex does not know |
// | com.superlogical.terminal.process | the fields GoRex can read; path, argv0 and script are left out, and ancestors is left out |
// | com.superlogical.terminal.program_status | the owner is the foreground process when a report arrives |
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
// | block.event | a mouse event's payload is action, button, mods, column and row, and a wheel event's is direction (up, down, left or right), mods, column and row: Rex's help document gives them no schema |
// | block_creator.list, session.describe_block | api.events lists GoRex's terminal events (`bell`, `title_changed`, `program_status_changed`, `child_exited`), which Rex's help document leaves out |
// | server.config_reload | no configuration file: counts a generation and sends keymap_changed |
// | hosts.list | an empty list; hosts.put and hosts.remove fail with not_supported |
// | server.tailscale.* | built-in Tailscale is tsnet, a device of the gateway's own named <hostname>-gorex; the methods are for the local Unix socket only (403 elsewhere), as Rex's are; on Windows, where the socket gives no credentials, its file's permissions are the boundary |
// | session.own_block, session.disown_block, session.create | need the GoRex server to keep the gateway's connection alive, which the size locks take; otherwise not_supported |
//
// block_event events are sent for what the gateway sees change while it
// watches a block; the state a block had before the gateway started is
// not replayed.
package rexapi
