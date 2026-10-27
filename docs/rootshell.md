# rootshell

[rootshell](https://github.com/kitknox/rootshell), on the iPhone, the iPad
and the Mac, shows a GoRex machine's tabs and splits as its own, over SSH,
the way it shows herdr's in herdr's full control mode: GoRex plays herdr.
rootshell needs no change; the App Store and TestFlight builds do.

## How it works

In control mode rootshell runs, on the host, over the SSH connection it
already holds:

```sh
sh -c '… PATH="$HOME/.local/opt/herdr-rootshell/bin:$PATH" … herdr control'
```

`tools/rootshell/herdr`, installed in that directory, runs `GoRex control`
in herdr's place: a bridge from stdin and stdout to the session server of
the GoRex open on that machine. It speaks the part of herdr's control
stream (stream protocol 2, or 3 when rootshell asks for it, one JSON
object per line) that rootshell uses,
and reports itself as herdr `0.9.0-gorex`. The bridge starts no server: on
Windows a server started from an SSH logon would end with it.

- **Tabs** are the desktop's tabs on this machine, with their splits. Panes
  of remote hosts are left out, and tabs of only those too.
- **The layout is the desktop's.** rootshell follows it as it changes:
  the server tells the bridge of each change of the layout and the
  sessions as it happens, and rootshell shows it at once (a new program
  in a pane, within half a second of its printing; one that prints
  nothing, within 30 seconds). rootshell changes the layout through the
  GoRex window: new tab, split, close pane, close tab, rename tab, zoom
  and moving tabs go to the window, which makes them as its own buttons
  do, and rootshell shows them as soon as the window saved them. A new tab opens after the tab
  last used in rootshell, in its directory. Closing a pane or a tab ends
  its sessions, as on the desktop.
- **Size.** One terminal has one size. The tab rootshell shows takes it:
  its panes are drawn for the phone, and the desktop shows them at the
  phone's size meanwhile. Typing in one of them on the desktop takes it
  back. rootshell keeps the size of the last GoRex tab it went to while
  it stays connected, renewing it every 5 seconds: going to another GoRex
  tab, or detaching, gives it back at once, and a dropped connection or
  the app put away in the background within 30 seconds. While the
  window is dragged, the sessions resize once it stops, and a moment
  later rootshell gets each resized pane's screen again, drawn at the new
  size, as after a resize on the desktop.
- **Per-pane zoom.** Each pane is a session with a size of its own:
  zooming one pane's font in rootshell resizes only that pane's session,
  and the tab's other panes keep their sizes (protocol 3).
- **Terminal queries** (cursor position, colors, device attributes) are
  answered by the device holding the size only, not once per device.

## Set up the host

### macOS

1. Install GoRex in `/Applications` and open it.
2. Turn on Remote Login (System Settings › General › Sharing), and log in
   with a key from rootshell (rootshell can make one in the Secure Enclave).
3. Install the shim:

   ```sh
   mkdir -p ~/.local/opt/herdr-rootshell/bin
   cp tools/rootshell/herdr ~/.local/opt/herdr-rootshell/bin/
   chmod +x ~/.local/opt/herdr-rootshell/bin/herdr
   ```

   It runs `/Applications/GoRex.app/Contents/MacOS/GoRex`. For another
   place, set `GOREX` at the top of the shim.
4. Check it, in a terminal:

   ```sh
   PATH=~/.local/opt/herdr-rootshell/bin:$PATH herdr control </dev/null | head -1
   ```

   It prints one line with `control.open` while GoRex runs, and one with
   `not_found` when it does not.

### Windows

1. Install GoRex (it goes to `%LOCALAPPDATA%\Programs\GoRex`) and open it.
2. Install OpenSSH Server (Settings › System › Optional features), start
   the `sshd` service, and add rootshell's public key to
   `~/.ssh/authorized_keys` (for an administrator,
   `C:\ProgramData\ssh\administrators_authorized_keys`).
3. rootshell's command line is for `sh`, which `cmd.exe` breaks. Make Git
   Bash OpenSSH's shell, in an administrator's PowerShell:

   ```powershell
   New-ItemProperty -Path HKLM:\SOFTWARE\OpenSSH -Name DefaultShell -Value 'C:\Program Files\Git\bin\bash.exe' -PropertyType String -Force
   Restart-Service sshd
   ```

4. Install the shim, in Git Bash:

   ```sh
   mkdir -p ~/.local/opt/herdr-rootshell/bin
   cp tools/rootshell/herdr ~/.local/opt/herdr-rootshell/bin/
   chmod +x ~/.local/opt/herdr-rootshell/bin/herdr
   ```

   It runs `%LOCALAPPDATA%\Programs\GoRex\GoRex.exe`. For another place,
   set `GOREX` at the top of the shim.
5. Check it from another machine:

   ```sh
   ssh HOST 'herdr control' </dev/null | head -1
   ```

   As on macOS: `control.open` while GoRex runs, `not_found` when not.

## In rootshell

1. Add the host: a connection to it over SSH (or tssh on macOS hosts),
   with the key of step 2.
2. Turn control mode on, one of:
   - in the connection's or profile's settings, set auto-start to
     **herdr (control)**;
   - in the multiplexer session picker, turn on **Control Mode** and pick
     herdr;
   - in Quick Connect, add `--herdr-control`.
3. Leave Settings › Debug › **Force herdr Fallback Mode** off. GoRex has
   no fallback mode: with it on, rootshell finds no herdr.
4. Connect. The gateway tab keeps the shell; the GoRex tabs open beside
   it, as native tabs and splits. Escape on the gateway detaches, leaving
   the sessions running.
5. Go to a tab: rootshell sizes it for this device. Type, select, scroll as
   in any tab. Connection Info shows the bridge's version (`0.9.0-gorex`)
   and features.

Turning off **Discover herdr Sessions** keeps rootshell from asking the
shim for herdr's sessions, which it answers with none anyway.

## Limits

- Changing the layout needs the GoRex window open on the host. Without
  it, a new tab or a rename fails with "Open the GoRex window on …", and
  a split, a close or a zoom does nothing, silently.
- The window's last tab closes only on the desktop: closing it from
  rootshell fails, as GoRex closes with it.
- Closing a tab from rootshell ends its panes of remote hosts too, which
  rootshell does not show, as closing it on the desktop does.
- Dragging a divider, equalizing, moving or renaming panes, workspaces and
  worktrees fail with "herdr Action Failed" (read-only).
- While a full-screen program runs (vim, lazygit, an agent), rootshell has
  no scrollback of that pane: the server keeps the alternate screen only.
- A snapshot holds at most the history rootshell asks for (1 MiB); older
  scrollback is left on the desktop.
- The desktop's panes do not shrink to the phone's size: what the program
  draws sits in the top-left of the pane until the desktop takes the size
  back.
- Only this machine's own GoRex takes the size back by typing: a GoRex on
  another machine showing this one as a Remote Host waits for rootshell
  to let go.
- On a desktop pane not holding the size, a key that sends what looks like
  a terminal's answer (Shift+F3 sends a cursor report's) is dropped.
- The shim takes the name `herdr` in that directory: a host running
  rootshell's herdr fork too uses GoRex instead.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| rootshell says herdr is not found or too old | The shim is at `~/.local/opt/herdr-rootshell/bin/herdr` and executable; `GOREX` in it names GoRex's executable. |
| `not_found`: GoRex is not running | Open GoRex on the host; the bridge does not start the server. |
| Nothing on Windows, or `'…' is not recognized` | OpenSSH's DefaultShell is Git Bash, and `sshd` was restarted after setting it. |
| A split, close or zoom from rootshell does nothing | The GoRex window is open on the host: it makes the change. |
| The tabs are empty | GoRex saves its layout a moment after it changes: open a tab on the host, wait a second, connect again. |
| The bridge finds another server than the app's | Both use the app's data directory, or `GOREX_DIR` if set: set it the same for the SSH logon, or not at all. |
| A desktop pane stays at the phone's size | Type in it to take the size back; it comes back by itself within 30 seconds after rootshell leaves. |
