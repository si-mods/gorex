# Changelog

## 0.27.0

### Features

* **ios:** Demo Mode for a user with no host ([#552](https://github.com/si-mods/gorex/pull/552))
* **ios,web:** share the web client over Personal Hotspot ([#556](https://github.com/si-mods/gorex/pull/556))
* **web:** add terminal find, command palette, and editable shortcuts ([#551](https://github.com/si-mods/gorex/pull/551))
* **web:** render floating terminal layers and minimize or restore them ([#554](https://github.com/si-mods/gorex/pull/554))
* **web:** upload dropped files and pasted images to a remote terminal ([#548](https://github.com/si-mods/gorex/pull/548))
* **web:** persist the Tailscale sign-in and coordinate tabs ([#546](https://github.com/si-mods/gorex/pull/546))

### Bug Fixes

* **ios:** allow cancelling an active or finishing dictation ([#555](https://github.com/si-mods/gorex/pull/555))
* **macos:** declare local network usage, so programs in tabs reach the LAN again while the app's privacy answers hold ([#559](https://github.com/si-mods/gorex/pull/559))
* **rexapi:** read pane output at once and resync slow data streams ([#543](https://github.com/si-mods/gorex/pull/543))
* **web:** distinguish an unsigned reload from an expired Tailscale login ([#547](https://github.com/si-mods/gorex/pull/547))
* **web:** restore floating layers from `session.set_layer_state` with its own arguments ([#557](https://github.com/si-mods/gorex/pull/557))
* **web:** run the `execCommand` copy fallback synchronously, before awaiting the Clipboard API ([#549](https://github.com/si-mods/gorex/pull/549))

## 0.26.0

### Features

* **settings:** Server page in the user's words: one switch (`Let other devices connect`), `Devices`, `Add a device`, and `Show Details` for advanced options, without Tailcat or DERP in UI strings ([#541](https://github.com/si-mods/gorex/pull/541))
* **cli:** support Antigravity CLI (`agy`) across all platforms ([#542](https://github.com/si-mods/gorex/pull/542))
* **web:** keep several hosts connected at once, read spaces across hosts, keep the last four windows live, manage hosts while connected, and check for updates from Settings > About ([#530](https://github.com/si-mods/gorex/pull/530), [#532](https://github.com/si-mods/gorex/pull/532), [#536](https://github.com/si-mods/gorex/pull/536), [#537](https://github.com/si-mods/gorex/pull/537), [#538](https://github.com/si-mods/gorex/pull/538), [#539](https://github.com/si-mods/gorex/pull/539))

### Bug Fixes

* **web:** draw a pane only once it has a font ([#535](https://github.com/si-mods/gorex/pull/535))

## 0.25.0

### Features

* **remote:** Reachable at: a machine with remote access on can be its own relay at one of its IP addresses, set in Settings > Server or from the CLI, and another machine pairs with it by that address, without Tailscale between them ([#495](https://github.com/si-mods/gorex/pull/495), [#498](https://github.com/si-mods/gorex/pull/498), [#501](https://github.com/si-mods/gorex/pull/501))
* **remote:** on Windows, Settings says when Windows Firewall blocks the Reachable at port, and offers to allow it ([#511](https://github.com/si-mods/gorex/pull/511))
* **status:** the session server keeps which done and error records were seen, so one seen on a client is seen on the others and after a restart ([#523](https://github.com/si-mods/gorex/pull/523))
* **rexapi:** a block moves beside another in its own window or before it, and a window moves to another session ([#512](https://github.com/si-mods/gorex/pull/512), [#514](https://github.com/si-mods/gorex/pull/514))
* **rexapi:** the gateway takes an upload compressed as raw DEFLATE, which Rex Go sends for files that shrink ([#508](https://github.com/si-mods/gorex/pull/508))
* **terminal:** the terminal is libghostty-vt 1.0.1 ([#515](https://github.com/si-mods/gorex/pull/515))

### Bug Fixes

* **remote:** a pairing request by address closes its own pairing window only ([#510](https://github.com/si-mods/gorex/pull/510))
* **rexapi:** a request that uses an id again as soon as its answer came no longer ends the connection, and a file put again right after its answer is no longer refused as being transferred ([#527](https://github.com/si-mods/gorex/pull/527))

## 0.24.0

### Features

* **rex:** on Windows, an update no longer ends the programs in the panes: the session server hands its sessions to the new version, as on macOS and Linux, and restarts, once the user agrees, only when it cannot ([#483](https://github.com/si-mods/gorex/pull/483))

## 0.23.0

### ⚠ BREAKING CHANGES

* **cli:** `rec` and `sox` in a session no longer stand in for SoX to take Claude Code's `/voice` from Rex Go, which recognizes speech on the device now; the API's voice interface still works and is deprecated ([#459](https://github.com/si-mods/gorex/pull/459))

### Features

* **rexapi:** a web page reaches a host's API only from an origin in the host's list, which `gorex rexapi web allow ORIGIN`, `deny ORIGIN` and `status` keep; a page of any other origin is refused, where any page in a browser on an allowed network could reach the API ([#410](https://github.com/si-mods/gorex/pull/410))

### Bug Fixes

* **terminal:** Control+click opens a Windows path printed in a pane, as `C:\dir\file.png` or `C:/dir/file.go:12`, and a path that wraps onto the next rows, clicked on any of them ([#466](https://github.com/si-mods/gorex/pull/466), [#480](https://github.com/si-mods/gorex/pull/480))
* **cli:** a `gorex` command typed in a session reaches that session's own server, not the one of the default data directory ([#469](https://github.com/si-mods/gorex/pull/469))
* **cli:** on Windows, Ctrl-C interrupts a command, and a command run from a console prints there ([#470](https://github.com/si-mods/gorex/pull/470), [#479](https://github.com/si-mods/gorex/pull/479))

## 0.22.0

### Features

* **hosts:** a tab a remote host shares opens in the space of the same name as its space there, made when there is none, and no longer in the window focused last ([#427](https://github.com/si-mods/gorex/pull/427))

### Bug Fixes

* **cli:** a tab's or a space's name selects its focused pane, whatever panes come before it, and never a note, a web page or a remote host's pane; `gorex run` ends its session when it gets Ctrl-C or a terminate signal ([#422](https://github.com/si-mods/gorex/pull/422))
* **remote:** servers of different protocol versions reach each other over the system's Tailscale ([#424](https://github.com/si-mods/gorex/pull/424))
* **canvas:** a canvas tab stays when one of its boxes moves to another window; the zoom button says Unzoom to a screen reader while the pane is zoomed ([#423](https://github.com/si-mods/gorex/pull/423))
* **panes:** a find bar or a name field closes with its pane however the pane goes, as with its tab; a remote host's pane restarted and then closed leaves the focus on a pane that is there ([#435](https://github.com/si-mods/gorex/pull/435))
* **settings:** `settings.json` is written whole or not at all ([#437](https://github.com/si-mods/gorex/pull/437))
* **rex:** a session takes the size of the client that is left when the one that sized it is dropped for falling behind, and a resize sent right after an attach counts as that client's ([#449](https://github.com/si-mods/gorex/pull/449))
* **rexapi:** a client that stopped reading no longer holds up the others' requests while it is dropped ([#448](https://github.com/si-mods/gorex/pull/448))
* **rexapi:** a nested layout is made in its own shape, with each split's ratio; `rex-import` keeps a split's direction and ratio, and leaves out a block its window lacks, saying which ([#450](https://github.com/si-mods/gorex/pull/450), [#454](https://github.com/si-mods/gorex/pull/454))
* **rexapi:** an upload is refused before it starts for a file name the server would refuse ([#436](https://github.com/si-mods/gorex/pull/436))

### Performance Improvements

* **window:** the windows ask the server for the sessions once between them, and draw again only when something changed ([#441](https://github.com/si-mods/gorex/pull/441))
* **hosts:** a new tab, split, float or restarted shell on a remote host no longer holds the window while the host answers; one the host does not make shows its error and no dead pane ([#455](https://github.com/si-mods/gorex/pull/455))
* **rex:** a sessions event no longer inspects every session's program, and an omp session's environment is read once ([#451](https://github.com/si-mods/gorex/pull/451), [#438](https://github.com/si-mods/gorex/pull/438))
* **rexapi:** the gateway reads the session list once for its model, block events and streams, as the server tells of a change, not on a timer; an event is encoded once for all its connections; a long stream no longer copies its replay window for every chunk ([#453](https://github.com/si-mods/gorex/pull/453), [#448](https://github.com/si-mods/gorex/pull/448), [#439](https://github.com/si-mods/gorex/pull/439))

## 0.21.0

### ⚠ BREAKING CHANGES

* **control:** GoRex no longer plays herdr for rootshell: the `control` subcommand, the `herdr` shim and their guide are gone; Rex Go reaches a machine through Rex's API ([#419](https://github.com/si-mods/gorex/pull/419))

### Bug Fixes

* **rex:** a session gets its window's size back when a device that held its size lets go, as Rex Go leaving a terminal or going to the background: a pane of a remote host no longer stays at the phone's size ([#418](https://github.com/si-mods/gorex/pull/418))
* **sidebar:** tab rows stay in place when the window changes space ([#417](https://github.com/si-mods/gorex/pull/417))
* **sidebar:** a closed space stays closed, and a host icon this version does not know is ignored ([#414](https://github.com/si-mods/gorex/pull/414))
* **rex:** a program whose update renamed its running image, as omp on Windows, keeps its name, and can teleport ([#416](https://github.com/si-mods/gorex/pull/416))

## 0.20.4

### Features

* **sidebar:** each host shows its machine's icon, learned from the host or chosen in Settings › Hosts or from a remote host's context menu, with a dot for a host that is not connected; in the host view each space opens and closes on its own, and an open space of no window lists its tabs ([#411](https://github.com/si-mods/gorex/pull/411))

## 0.20.3

### Features

* **sidebar:** a tab's context menu moves it to another space, Move to Space, its sessions going on; it joins the window that shows that space, and the window's last tab leaves its space as a closed last tab does ([#401](https://github.com/si-mods/gorex/pull/401), [#402](https://github.com/si-mods/gorex/pull/402))
* **remote:** the tailcat address serves Rex's API on port 7297 to the clients that may command the layout, for Rex Go with no VPN; a relay of one's own is set with `gorex remote relay HOST` or in Settings › Remote ([#398](https://github.com/si-mods/gorex/pull/398))
* **rexapi:** Rex's API revision 12: `server.status` lists the listeners ([#388](https://github.com/si-mods/gorex/pull/388))
* **settings:** remote access and the Local API move onto the Server page, the first in the sidebar, and remote clients always share this machine's sessions ([#390](https://github.com/si-mods/gorex/pull/390))

### Bug Fixes

* **rexapi:** the primary listener's `unix://` endpoint is escaped as Rex escapes it ([#389](https://github.com/si-mods/gorex/pull/389))
* **terminal:** an autolinked URL ends before the CJK prose after it ([#404](https://github.com/si-mods/gorex/pull/404))
* **links:** a web address printed without its scheme, as `example.com/path`, opens over https instead of as a file ([#407](https://github.com/si-mods/gorex/pull/407))

## 0.20.2

### Bug Fixes

* **rex:** a session server started from a coding agent's shell no longer passes the agent's non-interactive variables (`NO_COLOR`, `CI=true`, `PAGER=cat`, `EDITOR=true` and the like) to its panes, so programs there draw in color again; a user's own values stay ([#379](https://github.com/si-mods/gorex/pull/379))

## 0.20.1

### Features

* **hosts:** a remote host takes a display alias, set in Settings › Remote Hosts › Rename; discovery keeps it ([#367](https://github.com/si-mods/gorex/pull/367))
* **windows:** once an update is installed, the app offers to restart the session server on the new build, keeping the windows open ([#366](https://github.com/si-mods/gorex/pull/366))

### Bug Fixes

* **terminal:** Command+C (Control+Shift+C elsewhere) reaches a program in the pane when the pane has no selection, so Codex CLI copies its own mouse selection ([#370](https://github.com/si-mods/gorex/pull/370))

## 0.20.0

### Features

* **terminal:** panes keep text at a contrast of at least 3 against its background unless Ghostty's `minimum-contrast` sets one; Settings › Terminal › Minimum contrast chooses it ([#346](https://github.com/si-mods/gorex/pull/346))

### Bug Fixes

* **sidebar:** the host view drops the space chip, as it lists the spaces under their hosts; a right click on the window's space renames it in place, colors it or closes it ([#352](https://github.com/si-mods/gorex/pull/352))

## 0.19.0

### Features

* **sidebar:** a host view, switched in Settings › Appearance › Sidebar view or with the palette's Toggle Sidebar View, lists under each host the sessions with tabs there ([#333](https://github.com/si-mods/gorex/pull/333))
* **ios:** Rex Go groups terminals by the desktop's Spaces across the hosts it is paired with, chosen in Settings › Grouping; the Mac's gateway serves `GET /api/spaces` ([#335](https://github.com/si-mods/gorex/pull/335))
* **attention:** the Dock badge, Needs You and notification clicks count the sessions of a remote host in a space no window shows ([#343](https://github.com/si-mods/gorex/pull/343))
* **remote:** a server shares its sessions with every paired client by default; `gorex remote share off` turns it off and is kept ([#335](https://github.com/si-mods/gorex/pull/335))
* **rex:** a session's `TERM_PROGRAM_VERSION` is the version of the Ghostty GoRex binds, not one set by hand ([#320](https://github.com/si-mods/gorex/pull/320))

### Bug Fixes

* **panes:** a file path or URL printed before full-width punctuation, such as `。` or `，`, no longer takes it into the link, so a Command-click opens or downloads the file ([#336](https://github.com/si-mods/gorex/pull/336))
* **panes:** a URL printed as text ends at its first character that is not ASCII, so CJK text after it does not join it ([#319](https://github.com/si-mods/gorex/pull/319))
* **paste:** pasting an image into a pane of a host sends the image to the host and pastes its path, when the program there cannot read this machine's clipboard ([#338](https://github.com/si-mods/gorex/pull/338))
* **rex:** a window that attaches gets each image of the session's graphics once, not once per redraw ([#324](https://github.com/si-mods/gorex/pull/324))
* **pty:** a session's shell no longer inherits the server's descriptors, so a tab opened from the iPhone does not start with `too many open files` ([#344](https://github.com/si-mods/gorex/pull/344))
* **rexapi:** a desktop pane that types into a session the iPhone sized takes the size, and the phone takes it back on its own input ([#342](https://github.com/si-mods/gorex/pull/342))
* **windows:** symbols with text presentation, such as `⏺`, draw in the text's color, not as emoji ([#332](https://github.com/si-mods/gorex/pull/332))

## 0.18.0

### Features

* **rexapi:** serve Rex's API revision 11: Rex's iPhone app uploads files into a terminal, and its wheel and pointer events carry deltas and buttons ([#305](https://github.com/si-mods/gorex/pull/305), [#315](https://github.com/si-mods/gorex/pull/315))

### Build

* **macos:** GoRex is signed with a Developer ID and notarized, so macOS keeps its privacy answers, such as media library access, across updates, and opens the app without a warning; the first update asks once more ([#312](https://github.com/si-mods/gorex/pull/312), [#316](https://github.com/si-mods/gorex/pull/316))

## 0.17.0

### Features

* **palette:** list what needs you, by state, across windows and hosts ([#272](https://github.com/si-mods/gorex/pull/272))
* **notify:** name the tab and pane in a notification's subtitle, stack a session's notifications, focus the pane on a click, and show the program's tile as the image ([#271](https://github.com/si-mods/gorex/pull/271), [#297](https://github.com/si-mods/gorex/pull/297))
* **sidebar:** a click on a host's name folds its tabs, and GoRex remembers it; a folded host still shows its last chosen tab and the tabs that need you ([#270](https://github.com/si-mods/gorex/pull/270))
* **ui:** the host panel tells this machine and its server, and shows a remote host only when it is not connected, with a Remote Hosts… button ([#216](https://github.com/si-mods/gorex/pull/216))
* **rexapi:** `POST /api/upload` puts a file in the host's temporary folder and answers its path, quoted for the session's shell, so Rex's iPhone app can add photos and files ([#242](https://github.com/si-mods/gorex/pull/242))
* **rex:** a status record keeps its last message as `last_msg`, so a finished program's row and notification still say what it did ([#257](https://github.com/si-mods/gorex/pull/257))

### Bug Fixes

* **drop:** an upload's transfers belong to its session, so moving the pane, switching spaces or closing the window keeps the transfer list and the paste ([#285](https://github.com/si-mods/gorex/pull/285))
* **drop:** a drop on a host that is reconnecting waits for the host, at most one minute, and resumes the upload ([#293](https://github.com/si-mods/gorex/pull/293))
* **drop:** name a dropped file so its path pastes, and never paste a path the host's shell cannot quote ([#298](https://github.com/si-mods/gorex/pull/298))
* **drop:** end every drop's pasted paths with a space, in host and local panes, so two drops in a row do not run together ([#227](https://github.com/si-mods/gorex/pull/227), [#295](https://github.com/si-mods/gorex/pull/295))
* **panes:** Command-click opens a bare directory name such as `docs/`, and a local pane links a path only when it exists ([#295](https://github.com/si-mods/gorex/pull/295))
* **drop:** the transfer list, the palette and the drop card show their border with glass off, and the progress track shows on a light panel ([#299](https://github.com/si-mods/gorex/pull/299))
* **rex:** closing a tab or a space through the API ends the sessions of its host panes too, through the app's kill queue ([#286](https://github.com/si-mods/gorex/pull/286))
* **space:** a client's new tab with the focus brings its hidden space into the last focused window ([#296](https://github.com/si-mods/gorex/pull/296))
* **rex:** the server's emulator answers terminal queries when the Rex API gateway is the session's authority, so programs no longer wait for their timeout ([#278](https://github.com/si-mods/gorex/pull/278))
* **rex:** an upgrade hands over a session's terminal modes, images and an exited session's screen ([#273](https://github.com/si-mods/gorex/pull/273))
* **view:** a pane's terminal keeps its size from the first frame of a move, so zoom no longer resizes the session in between ([#302](https://github.com/si-mods/gorex/pull/302))
* **control:** a split's new pane has the focus in its tab before rootshell hears the layout ([#307](https://github.com/si-mods/gorex/pull/307))
* **rexapi:** a stalled gateway no longer freezes the settings window, and an upload from the iPhone no longer times out on hello ([#246](https://github.com/si-mods/gorex/pull/246), [#294](https://github.com/si-mods/gorex/pull/294))
* **rexapi:** built-in Tailscale's control server needs https unless its address is local or private ([#290](https://github.com/si-mods/gorex/pull/290))
* **rexapi:** show Tailscale's sign-in link only over the local socket, so a LAN or tailnet client cannot claim the device ([#289](https://github.com/si-mods/gorex/pull/289))
* **rex:** remove tailcat IPv6 addresses from errors ([#291](https://github.com/si-mods/gorex/pull/291))

### Dependencies

* **mygo:** bump to si-mods/mygo 34886fc for the space after a local drop's paths and links to bare directories ([#295](https://github.com/si-mods/gorex/pull/295))

## 0.16.0

### Features

* **layout:** the session server owns the layout. The server makes every layout change, so clients such as the Rex iPhone app and the `gorex` CLI can open windows, split, rename and close with no GoRex window open. The app follows the server's layout ([#224](https://github.com/si-mods/gorex/pull/224), [#225](https://github.com/si-mods/gorex/pull/225), [#230](https://github.com/si-mods/gorex/pull/230))
* **rex:** the server owns its panes' lifecycle. It drops the pane of a session that ends, and restores lost sessions after a crash (in the home folder when their folder is gone). It places sessions that no tab shows, finishes teleports, and keeps each pane's folder, size and program current ([#233](https://github.com/si-mods/gorex/pull/233))
* **tabs:** drag a tab onto another host to teleport it, after asking ([#217](https://github.com/si-mods/gorex/pull/217))

### Bug Fixes

* **rex:** size a tab made in the background by the room it gets, and restore lost sessions in the background so the server answers at once ([#235](https://github.com/si-mods/gorex/pull/235))
* **tabs:** select the old name when a rename starts ([#235](https://github.com/si-mods/gorex/pull/235))
* **rex:** `session.set_label` renames the session ([#224](https://github.com/si-mods/gorex/pull/224))
* **panes:** upload files dropped on a host pane into a temp folder ([#223](https://github.com/si-mods/gorex/pull/223))
* **cli:** keep the gorex shim pointing at the binary ([#222](https://github.com/si-mods/gorex/pull/222))
* **remote:** share one tailcat client per machine through the local server ([#220](https://github.com/si-mods/gorex/pull/220))

## 0.15.0

### Features

* **teleport:** move a pane's work, and its agent's conversation, to another host ([#199](https://github.com/si-mods/gorex/pull/199))
* **panes:** Cmd-click a path in a host pane to download it ([#207](https://github.com/si-mods/gorex/pull/207))
* **panes:** upload files dropped on a host's pane, with a transfer list ([#195](https://github.com/si-mods/gorex/pull/195))
* **rex:** add `file.put` and `file.get` transfer connections ([#193](https://github.com/si-mods/gorex/pull/193))
* **remote:** pair Rex by QR code, show this desktop's sessions in Rex, and find the system's Tailscale ([#187](https://github.com/si-mods/gorex/pull/187))
* **ui:** minimize floating panes with a chip that shows them again, set built-in Tailscale's control server in Settings › Remote, and show the Rex API revisions on the Server page ([#186](https://github.com/si-mods/gorex/pull/186))
* **cli:** install an agent skill and session-start hooks ([#194](https://github.com/si-mods/gorex/pull/194))
* **cli:** install signed updates and hand off running sessions ([c98f9ef](https://github.com/si-mods/gorex/commit/c98f9ef))

### Bug Fixes

* **panes:** end a closing host pane's session after its uploads stop ([#206](https://github.com/si-mods/gorex/pull/206))

### Dependencies

* **mygo:** bump for exec bits in Windows-built archives ([#208](https://github.com/si-mods/gorex/pull/208))

## 0.14.0

### Features

* **canvas:** add an experimental infinite canvas layout, off by default (Settings > Appearance > Experimental): every pane is a free box on an unbounded plane that pans with the wheel, a drag on the empty plane or Space held, and zooms with Ctrl or Cmd and the wheel or a pinch; a right-click on the plane adds a terminal, browser or note where it was clicked, and a window resize resizes no pane ([#174](https://github.com/si-mods/gorex/pull/174))
* **resume:** resume the exact Claude Code conversation of a pane, rather than the directory's latest ([#181](https://github.com/si-mods/gorex/pull/181))
* **spaces:** keep a space's panes live when the window switches away, so terminals stay attached, browser pages stay loaded and program-set titles stay ([#179](https://github.com/si-mods/gorex/pull/179))
* **rexapi:** serve Rex's API revision 10, which Rex's iPhone app needs to connect ([#171](https://github.com/si-mods/gorex/pull/171))

### Bug Fixes

* **server:** keep dev builds out of the installed app's data directory, so a plain build no longer restarts the installed app's session server ([#182](https://github.com/si-mods/gorex/pull/182))
* **resume:** run a quoted program with `&` in PowerShell ([#176](https://github.com/si-mods/gorex/pull/176))
* **ui:** render Chinese UI text in Noto Sans SC on Windows where it is installed, so medium and semibold weights show, and give the Settings sidebar's items one weight ([#177](https://github.com/si-mods/gorex/pull/177))
* **glass:** show the desktop through windows on Windows 11 with Acrylic ([#173](https://github.com/si-mods/gorex/pull/173))
* **settings:** give the settings groups the terminals' face ([#175](https://github.com/si-mods/gorex/pull/175))

### Dependencies

* update MyGo to si-mods/mygo e0c6f5c ([#183](https://github.com/si-mods/gorex/pull/183))

## 0.13.0

### Features

* **panes:** make the terminal panes, floats, popovers and the palette the system's Liquid Glass on macOS 26 and later, the desktop showing through them, each pane tinted by its face at 0.4 of the Terminal opacity setting (a configured theme's whole); browser panes keep their face, and macOS builds no longer link MyGo's own glass, which Windows and Linux keep ([#169](https://github.com/si-mods/gorex/pull/169))

### Bug Fixes

* **ui:** make corners concentric, as Apple's design system: the panes take the window's radius less the gap between them, 8 on macOS 27, and the rows of menus, popovers, the palette and theme cards their container's radius less its border and padding ([#170](https://github.com/si-mods/gorex/pull/170))

## 0.12.0

### Features

* **settings:** redesign the Settings window in the macOS 27 Liquid Glass style: a full-height glass sidebar with a server status card and a Server page, settings in grouped cards with the hint under each row, capsule controls, a theme card grid, a window no wider than its column, and a Remote page regrouped into the master switch, Devices, This Mac, Network and Rex apps, its notes under each card and its add and join forms opened from buttons ([#167](https://github.com/si-mods/gorex/pull/167))

## 0.11.0

### Features

* **panes:** keep what ran in a pane when its session is lost, and show a strip over the new shell that runs it again, quoted for the shell, or continues a Claude Code, Codex, omp or Pi conversation in that directory, which the agent itself finds ([#164](https://github.com/si-mods/gorex/pull/164))
* **rex:** remove the agent session-start hooks and the Pi/omp extensions (`gorex skill`, `gorex agent-hook`), with the server's agent records and the resume command a restart typed, so GoRex installs nothing into an agent's settings ([#165](https://github.com/si-mods/gorex/pull/165))

## 0.10.1

### Features

* **rexapi:** serve API revisions 0-7 with request and result translation, and fill the wire gaps: response `ordered_after` and `requester`, error `action`, welcome `carriers`, the stream reconnect grace, and `pwd_changed`, `process_changed` and `program_status_removed` block events ([#160](https://github.com/si-mods/gorex/pull/160))

### Bug Fixes

* **panes:** hold a divider drag's terminals at their rest size, so the program hears of one size, the drag's, once the pointer lets go rather than of each width on the way, where what it drew for the grid before wrapped where it did not wrap it and a program that draws in place, as an agent's interface does, reprinted its screen from the top again and again ([#161](https://github.com/si-mods/gorex/pull/161))

## 0.10.0

### Features

* **rexapi:** hand out data tickets in `session.bootstrap`, serving Rex's API revision 8, so a Rex client opens each terminal it attaches without a `data.connect` round trip first ([#158](https://github.com/si-mods/gorex/pull/158))

## 0.9.1

### Bug Fixes

* **panes:** lay a session's output out at the size the program drew it for, the session's, not the pane's: a window whose session another machine's window sizes takes that size and the session's screen, where it laid the output out at its own size and the program's frames landed elsewhere, covering rows with rows ([#154](https://github.com/si-mods/gorex/pull/154))
* **panes:** show the rows around the cursor in a pane shorter than its session's grid, where the pane showed the empty rows above the cursor ([#156](https://github.com/si-mods/gorex/pull/156))

## 0.9.0

### Features

* **ui:** on Windows and Linux, context menus are drawn as glass, shaped as macOS's: rounded, the highlight in the space's accent, and Chinese labels in one weight rather than the Win32 menus' mixed bold and regular ([#153](https://github.com/si-mods/gorex/pull/153))

### Bug Fixes

* **palette:** blur only the panel, which is Liquid Glass, and dim the window behind it, rather than blurring the whole window, which read as a smeared window instead of glass ([#151](https://github.com/si-mods/gorex/pull/151))

## 0.8.0

### Features

* **panes:** drag a pane by its header onto another pane: on a side it splits that pane and takes its half, at the center the two swap places; the target shows where the pane will go, as Rex does ([#148](https://github.com/si-mods/gorex/pull/148))

### Bug Fixes

* **sidebar:** center the spaces at its foot under the tabs, rather than beside the settings button ([#146](https://github.com/si-mods/gorex/pull/146))

## 0.7.1

### Bug Fixes

* **tabs:** give the pill of an active tab more room before its icon and less after it, and take a sidebar row's room for tiles from the widest tab's, so that a tab alone no longer keeps the room of three tiles between its icon and its name ([#144](https://github.com/si-mods/gorex/pull/144))

## 0.7.0

### Features

* **ui:** Liquid Glass: the desktop shows through the windows, popovers, dialogs, the palette and floats float as glass, and the active tab sits on the system's own glass, as Rex's; Glass and Terminal opacity in Settings, on and 80% by default ([#141](https://github.com/si-mods/gorex/pull/141))

## 0.6.1

### Bug Fixes

* **skill:** read an agent's settings that start with a byte order mark, so `gorex skill setup` installs the session-start hook on Windows ([#139](https://github.com/si-mods/gorex/pull/139))

## 0.6.0

### Features

* **note:** a note pane is a sticky note of free text: New Note Tab and Split with Note; the layout keeps the text, and the pane stays on this machine ([#134](https://github.com/si-mods/gorex/pull/134))
* **palette:** frost the backdrop and the panel with a backdrop shader ([#133](https://github.com/si-mods/gorex/pull/133))
* **ui:** paint the background with one dithered shader and grey out inactive controls ([#136](https://github.com/si-mods/gorex/pull/136))
* **note:** a note's text size follows the text-size keys, as a terminal's (⌘+, ⌘−, ⌘0; 8 to 40 points, 13 by default) and is kept per note ([#135](https://github.com/si-mods/gorex/pull/135))
* **note:** a note can show its text as Markdown: the eye button in its header, or Toggle Note Preview (⌘E) ([#135](https://github.com/si-mods/gorex/pull/135))

## 0.5.0

### Features

* jump to the pane that needs you and badge the Dock icon ([#129](https://github.com/si-mods/gorex/pull/129))
* resume agents' conversations when sessions restart ([#130](https://github.com/si-mods/gorex/pull/130))

### Dependencies

* **terminal:** bind Ghostty dc8f59943 through si-mods/mygo; snapshots now carry Kitty images ([#131](https://github.com/si-mods/gorex/pull/131))

## 0.4.0

### Features

* **float:** throw floats with the pointer's velocity and snap to edges ([#126](https://github.com/si-mods/gorex/pull/126))

## 0.3.6

### Features

* **motion:** animate floats, tabs, overlays and status dots ([#124](https://github.com/si-mods/gorex/pull/124))
* **rexapi:** serve Rex's API to Rex's apps, over the tailnet or the local network ([#118](https://github.com/si-mods/gorex/pull/118))

## 0.3.5

### Features

* **pane:** float panes over a tab's tiled layout ([#113](https://github.com/si-mods/gorex/pull/113))

### Bug Fixes

* **fonts:** draw a title's Nerd Font icons, and register a family macOS hides ([#116](https://github.com/si-mods/gorex/pull/116))
* **pane:** resize terminals live while dragging a divider ([#121](https://github.com/si-mods/gorex/pull/121))
* **remote:** open a host's new split panes in the tab already adopted ([#120](https://github.com/si-mods/gorex/pull/120))
* **sidebar:** drop the host chip beside a hidden sidebar ([#119](https://github.com/si-mods/gorex/pull/119))
* **tabs:** keep a faded tab label inside its row ([#115](https://github.com/si-mods/gorex/pull/115))

## 0.3.4

- A session another machine's client opens on this machine gets a tab in
  this window too, last among this machine's tabs and without the focus,
  rather than running unseen: a pane or tab added from another GoRex
  through Settings › Remote Hosts shows up here. A session this machine's
  command line opens stays as it was.

## 0.3.3

- A pane header reads on its own background: when a pane takes a dark
  terminal background in a light window, or the other way around, its
  title and buttons take the palette of that background, so a hovered icon
  no longer turns black on a dark header.
- Icons are SF Symbols on macOS: the buttons of the window and its pane
  headers, the tabs, the sidebar and the settings window draw the system's
  own symbol at their size and color, as Rex does, which follows the
  design of the macOS they run on and keeps them sharp on any display.
  Windows and Linux, and a macOS too old for a symbol, draw the SVGs of
  `assets/icons` as before; brand marks are always SVGs, since SF Symbols
  has no logos. Nothing of Apple's artwork is in the app or its build.
- Browser panes. New Browser Tab (the command palette or the Shell
  menu) opens a tab whose pane shows a web page in the system's web
  view, with an address field, back, forward and reload in its header;
  Split with Browser puts one beside the focused pane. The page's title
  names its tab, and the layout keeps its URL, so it opens again when
  GoRex starts. A browser pane belongs to this machine: a remote tab
  takes none, and Find searches terminals alone. Only http and https
  pages open, and a page is untrusted: it reaches no GoRex code, asks
  for no permission, opens no window and downloads nothing.

- Importing a Rex session. `gorex rex-import` reads a Rex server's sessions
  and makes them in GoRex: a space per session, a tab per window, a pane per
  block, each named as Rex names it. Each pane runs `gorex rex-mirror`, which
  draws what the Rex block shows, from the escape sequences Rex renders (the
  data stream's snapshot is Ghostty's binary format, which the mirror reads
  past), and sends what is typed to the block; `--shell` runs a shell in each
  block's directory instead, and `--resize` gives the block the pane's size.
  Where a session came from is recorded in `rex-import.json`, so a second run
  leaves it alone. The window imports too: **Space ▸ Import from Rex**, and
  the palette's *Import from Rex*, mirror every session not recorded yet and
  report the count as a notification (a failure as a dialog). Rex's default
  labels are not copied: a window called `Window 2b63efe2` and a block called
  `shell` leave tab and pane to follow the block's title. A mirrored pane
  shows the tile of the program its Rex block runs, and its status, through
  the status protocol (OSC 7501, `app=`), so Claude Code's tile and a working
  or finished state show as they do in Rex; a pane whose program is not a
  shell names itself by its title even while it is the program its session
  started with.
- Layout commands for a client: the space a command acts on, the command a
  new pane runs, and whether a new tab takes focus; `space.create` makes a
  space, and `space.close` closes one, ending the sessions of a space no
  window shows. Closing a space, or a tab, ends its programs as before.
- A pane header reads on its own background: when a pane takes a dark
  terminal background in a light window, or the other way around, its
  title and buttons take the palette of that background, so a hovered icon
  no longer turns black on a dark header.
- Icons are SF Symbols on macOS: the buttons of the window and its pane
  headers, the tabs, the sidebar and the settings window draw the system's
  own symbol at their size and color, as Rex does, which follows the
  design of the macOS they run on and keeps them sharp on any display.
  Windows and Linux, and a macOS too old for a symbol, draw the SVGs of
  `assets/icons` as before; brand marks are always SVGs, since SF Symbols
  has no logos. Nothing of Apple's artwork is in the app or its build.
- Paste into a program that runs on Windows reaches it again. A ConPTY
  drops the OSC that a terminal's paste event is, so a program that reads
  the clipboard itself, as agents do, never learned of a paste, and
  nothing came of it. A paste now goes to the session's server instead,
  which writes it as text, bracketed when the program asked for bracketed
  pastes; `gorex paste` does the same from the command line, and a pane of
  a remote client pastes the same in a Windows session. An image alone has
  no text to write, so an image still reaches a Windows program only as a
  file it opens.
- Pane animations. Panes now spring into place when they move, split or
  zoom, and new or closing panes pop in and out. A new Animation style
  setting in Settings › Appearance lets you choose Snappy, Natural or Off,
  and it also controls how the sidebar shows and hides. While Reduce
  Motion is on, animations stay off.
- A session server restarted on purpose brings its sessions back, as one
  that crashed already does: the server after it starts the sessions of the
  layout again, with the same ids and at the directories their panes were
  in, so the windows attach to them. `gorex server restart` does it without
  starting a server itself: the open app reconnects and starts the next one,
  else the next `gorex` command or launch does; run inside a pane, the
  command says so, then its shell ends with the server. The app on Windows
  restarts a server of an older build this way, since Windows servers cannot
  upgrade in place, after asking whether to, since each pane comes back as a
  new shell; a newer build is compared first, so that two copies of the app
  do not restart each other. What the old sessions printed is lost. Quit and
  End All still ends them, and so does the idle exit. A server too old to
  know a restart is not restarted: quit it once with Quit and End All, or
  kill it.

## 0.3.2

- Sessions are spaces now: the interface says Space / 空间, and each space
  has a color — nine presets, Default, or a custom `#rrggbb` — drawn in
  the window's gradient, the active tab and the accent. The sidebar's foot
  shows a dot per space, its own ringed and other windows' dimmed, and
  Ctrl+1 to Ctrl+9 (Ctrl+Alt elsewhere) show the nth space.
- A pane's header has a button that moves the pane to a new tab.
- A remote host's tab opens here as one tab, with its splits: the sessions
  it shows are adopted in the tab the host's shared layout names, so two
  panes side by side there are not two tabs here. A session the host has
  not saved into its layout yet waits a poll or two for the save, and the
  host's focused pane is focused here even when its layout leaves panes
  out.
- A session attached by several clients takes its size from the client
  that typed last, so a viewer attaching, or a frame reporting its focus,
  no longer resizes the session under the typist; the client left takes
  the size once the typist leaves.
- Dragging a window's edge gives each session one resize, 150ms after the
  size settles, instead of one per step: one reflow, one SIGWINCH, and no
  stale prompt per step from zsh's right prompt.
- Popovers are opaque, and the app's text fields share one look in two
  sizes; a focused field takes the space's accent, and the rename fields
  no longer clip the text.

## 0.3.1

- A session server that dies — a crash, a `kill`, a reboot — no longer
  takes the windows with it. The server keeps every layout the app sends in
  an append-only journal beside the layout, and the server that starts
  after one that left no stop record starts the sessions of that layout
  again: the same ids, at the directories their panes were in, at their
  panes' sizes, so every window attaches to the sessions it had. A pane
  whose stream ends while the server still has its session stays open
  instead of closing its tab, and the app connects again on its own,
  attaching the window to the sessions that came back. A run that stops — the
  app asking it to, an idle exit, an upgrade — records why, with the sessions
  it held, so that the server after it starts nothing.

- Dropping a tab on the middle half of another tab grafts its panes beside
  the target tab's, with their sessions; the outer quarters still reorder,
  and tabs of different hosts only reorder.

## 0.3.0

- rootshell's pane actions work: dragging a split's divider
  (`pane.resize`), swapping (`pane.swap`), renaming (`pane.rename`) and
  moving a pane to another tab or a new tab (`pane.move`). Pane labels
  stay in snapshots.
- A paste of a copied image file, or of text that is one absolute path to
  an image here (quoted or not), offers the image's bytes ahead of the
  path: a program that reads the clipboard itself (Kitty's paste events)
  gets the picture even over SSH or on a remote host, where a path of this
  machine means nothing. A path pasted as text still types as the path, and
  a clipboard whose read fails whole pastes its text.
- A remote client can be given every session, or the layout, from
  Settings › Remote Hosts per client, while others keep only their own
  sessions.
- Remote hosts can share their layout read-only: Settings › Remote Hosts
  shows the host's workspaces, tabs and panes with Open, Focus, resize,
  New Tab and Close Tab commands, `gorex -S HOST layout`
  reads it, and `gorex -S HOST events` follows layout changes when Share
  Layout is on. The server filters the layout to sessions the client may
  use, shows the rest as unavailable placeholders, and drops panes of
  other hosts.
- Kitty graphics sent by file path (`t=f`) are read on the machine that
  runs the program, rewritten in-band, and replayed when a window attaches.
- Find bar: Cmd+F/Ctrl+Shift+F searches the focused pane, with match count,
  next and previous, and Escape to close; search colors come from Ghostty's
  `search-*` options. Command-click opens URLs and file links, resolving
  relative paths against the pane's program directory.
- Ghostty `clipboard-read`, `clipboard-write`, window padding, padding
  color and image-storage-limit now reach the terminal; the README lists
  which options map, are unsupported, or are ignored, and `gorex config
  check` warns about the ignored ones.
- Remote identities carry the paired name, key and created/last-seen
  times; a remote client's hello says which principal it is, and old
  allowlists get created times. `gorex clients` shows the principal and
  how long each attachment has been live.
- A login/PAM proposal documents the path from one-account shared mode to
  per-user isolation; it is not implemented.
- A Linux shared-server service and `gorex remote` administration commands
  (enable, address, pair, approve, revoke, share) are documented in
  `docs/shared-server.md`.
- Sessions carry the principal that created them; `gorex ls` shows local
  or the paired client's name.
- Program status changes notify subscribers and carry their session IDs and
  records in events; the session picker and sidebar show a session's most
  urgent status; rootshell gets the pane's and tab's `agent_status`; and
  status records survive an in-place server upgrade.

## 0.2.1

- Upgraded MyGo from v0.6.2 to v0.7.2. In the terminal, the scroll bar now
  shows where the viewport is as macOS's do, and drags it; Shift-click
  extends a selection; and a trackpad's wheel momentum no longer zooms the
  text under the pointer.
- The disk image is named after the app's slug, `gorex-0.2.1-<arch>.dmg`,
  as MyGo v0.7.0 changed, and `install.sh` installs on macOS as well as
  Linux.

## 0.2.0

- Program status (OSC 7501): tabs, panes and the palette show when a
  program such as Claude Code works, waits for you, finishes or fails, and
  a notification tells you when it happens out of sight.
- Sessions of tabs, one per window, as in Rex; GoRex runs a single
  instance.
- Connect to remote GoRex hosts from the app, pair devices by approval on
  the host, and let a host share its open sessions with paired clients.
- Vertical tabs, in a left or right sidebar, with the host chip at its foot.
- Simplified Chinese.
- The session server upgrades in place and keeps the sessions.
- Fixes: a query's answer no longer lands on the prompt when a session is
  open in several windows; remote panes get their size once attached; dead
  remote connections are dropped.

## 0.1.0

- First release, with automatic updates.
