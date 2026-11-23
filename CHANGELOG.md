# Changelog

## 0.53.0

### Features

* **desktop:** build on MyGo 0.10.1; a click on a window behind only brings it to the front ([#1017](https://github.com/si-mods/gorex/pull/1017))

## 0.52.0

### Features

* **ios:** approve or deny a blocked record from the Live Activity ([#1003](https://github.com/si-mods/gorex/pull/1003))
* **push:** mark the records the Live Activity showed as seen ([#1004](https://github.com/si-mods/gorex/pull/1004))
* **push:** start the Live Activity remotely on a blocked record ([#1006](https://github.com/si-mods/gorex/pull/1006))

### Bug Fixes

* **ios:** warm a socket-reset tailcat client instead of trying it ([#1010](https://github.com/si-mods/gorex/pull/1010))
* **rex:** close the journal a run stops with, so the next one compacts it ([#1013](https://github.com/si-mods/gorex/pull/1013))
* **ios,web:** the program tile of a tab the agent launcher opened, and of any session that runs a command ([#1015](https://github.com/si-mods/gorex/pull/1015))
* **ios:** let New Project's Space row pick the space ([#1014](https://github.com/si-mods/gorex/pull/1014))

## 0.51.0

### Features

* **desktop:** alert pushes replace the local blocked notification ([#1001](https://github.com/si-mods/gorex/pull/1001))
* **remote:** the gorex alias for the team's DERP relay ([#1005](https://github.com/si-mods/gorex/pull/1005))

### Bug Fixes

* **desktop:** retire a tailcat client whose try timed out, also when a connection uses it, so a host stays reachable after its server restarts ([#1009](https://github.com/si-mods/gorex/pull/1009))

## 0.50.0

### Features

* **desktop:** send Live Activity updates through the push relay ([#995](https://github.com/si-mods/gorex/pull/995))
* **ios:** bind the Live Activity's push tokens at the relay ([#996](https://github.com/si-mods/gorex/pull/996))
* **desktop:** bring the iOS space picker's panel to the desktop ([#989](https://github.com/si-mods/gorex/pull/989))
* **desktop:** shared empty states for blank and no-result surfaces ([#991](https://github.com/si-mods/gorex/pull/991))
* **ios:** shared empty states and no-results views ([#992](https://github.com/si-mods/gorex/pull/992))

### Bug Fixes

* **ios:** keep the client across a resume — the close code, not the frame, decides ([#993](https://github.com/si-mods/gorex/pull/993))
* **ios:** give the text style a terminal's emoji-capable chars ask for ([#990](https://github.com/si-mods/gorex/pull/990))

## 0.49.1

### Bug Fixes

* **desktop:** drop the tab count from a project's row ([#985](https://github.com/si-mods/gorex/pull/985))
* **web:** drop the tab count from a project's row ([#987](https://github.com/si-mods/gorex/pull/987))

## 0.49.0

### Features

* **projects:** quick-launch agent CLIs from the New Tab row ([#975](https://github.com/si-mods/gorex/pull/975))
* **cli:** update any Linux install, not only the user one ([#979](https://github.com/si-mods/gorex/pull/979))

### Bug Fixes

* **i18n:** localize the agent-launcher strings ([#984](https://github.com/si-mods/gorex/pull/984))
* **ios:** animate the space picker's menu over a steady capsule ([#977](https://github.com/si-mods/gorex/pull/977))
* **ios:** drop the tab count from a project's row ([#978](https://github.com/si-mods/gorex/pull/978))
* **ios:** filter the screen's line for the Live Activity and skip a bare rule ([#980](https://github.com/si-mods/gorex/pull/980))
* **panes:** take an own resize once it settles at the session ([efc017f](https://github.com/si-mods/gorex/commit/efc017fb))

## 0.48.0

### Features

* **tabs:** ask before closing a tab or pane that runs a program ([#971](https://github.com/si-mods/gorex/pull/971))
* **desktop:** ease exits in and grow overlays from where they open ([#974](https://github.com/si-mods/gorex/pull/974))
* **ios:** open a panel of the spaces from the space picker ([#969](https://github.com/si-mods/gorex/pull/969))
* **ios:** keep pane programs in the offline copy, show their tiles ([#973](https://github.com/si-mods/gorex/pull/973))

### Bug Fixes

* **desktop:** readable text on glass, one close-button shape, pickers as menus ([#972](https://github.com/si-mods/gorex/pull/972))
* **sidebar:** do not clip the current project's face on the panes' side ([#968](https://github.com/si-mods/gorex/pull/968))
## 0.47.0

### Features

* **desktop, web, ios:** ask a config action's question across GoRex's clients ([#959](https://github.com/si-mods/gorex/pull/959))
* **rexapi:** relay a pane's loopback port for the phone ([#947](https://github.com/si-mods/gorex/pull/947))
* **web:** bring the project sidebar in line with the desktop ([#943](https://github.com/si-mods/gorex/pull/943))
* **ios:** answer an action's question on the phone ([#957](https://github.com/si-mods/gorex/pull/957))
* **ios:** read a pairing code from a photo in the scanner ([#958](https://github.com/si-mods/gorex/pull/958))
* **ios:** block GPU presentation while the app is in the background ([#963](https://github.com/si-mods/gorex/pull/963))
* **ios:** secure the lock-screen snapshot window and background launches ([#964](https://github.com/si-mods/gorex/pull/964))
* **ios:** pull to refresh reconnects the hosts not online ([#945](https://github.com/si-mods/gorex/pull/945))

### Bug Fixes

* **desktop:** give the question an opaque panel of the window's theme ([#961](https://github.com/si-mods/gorex/pull/961))
* **ios:** drop escape codes and undrawable glyphs from a notification's text ([#956](https://github.com/si-mods/gorex/pull/956))
* **ios:** reread the proxy at once when a dial through it fails ([#951](https://github.com/si-mods/gorex/pull/951))
* **ios:** offer the loopback relay only on the host's network ([#950](https://github.com/si-mods/gorex/pull/950))
* **ios:** animate the project cards' fold like No Project's ([#948](https://github.com/si-mods/gorex/pull/948))
* **ios:** let a renamed tab's name show over its terminal ([#949](https://github.com/si-mods/gorex/pull/949))
* **ios:** re-inject the root's objects into sheets an iOS app on Mac drops ([#946](https://github.com/si-mods/gorex/pull/946))

## 0.46.0

### Features

* **desktop:** list tabs with no project as a group like a project, named No Project ([#938](https://github.com/si-mods/gorex/pull/938))
* **ios:** give each project its own card with a folder tile, and fold No Project like a project ([#933](https://github.com/si-mods/gorex/pull/933))
* **ios:** show a pane's listening ports and open one ([#939](https://github.com/si-mods/gorex/pull/939))
* **ios:** put Enter on the key bar's first page ([#934](https://github.com/si-mods/gorex/pull/934))
* **ios:** open a terminal with the standard push, and keep the list scheme steady ([#930](https://github.com/si-mods/gorex/pull/930))
* **ios:** reconnect hosts and terminals at once when the network changes ([#932](https://github.com/si-mods/gorex/pull/932))
* **ios:** keep host connections for an app-wide grace in the background ([#929](https://github.com/si-mods/gorex/pull/929))
* **rexapi:** ask the person a question through the gateway ([#931](https://github.com/si-mods/gorex/pull/931))
* **rexapi:** run the configuration's actions in the gateway ([#924](https://github.com/si-mods/gorex/pull/924))

### Bug Fixes

* **ios:** let a project's rename show in its windows' titles ([#936](https://github.com/si-mods/gorex/pull/936))
* **ios:** reach each shared host the way the app reaches it ([#935](https://github.com/si-mods/gorex/pull/935))
* **rex:** give each server its own ports timings ([#928](https://github.com/si-mods/gorex/pull/928))
* **rex:** find a process's children under every thread ([#926](https://github.com/si-mods/gorex/pull/926))

## 0.45.0

### Features

* **desktop:** set a project apart from its tabs in the sidebar ([#907](https://github.com/si-mods/gorex/pull/907))
* **rexapi:** walk back through a principal's focus at revision 19 ([#917](https://github.com/si-mods/gorex/pull/917))
* **rexapi:** drop an event too large to send at revision 18 ([#914](https://github.com/si-mods/gorex/pull/914))
* **rexapi:** report a terminal's listening ports at revisions 15 and 16 ([#912](https://github.com/si-mods/gorex/pull/912))
* **ios:** carry the touched element into the next state ([#916](https://github.com/si-mods/gorex/pull/916))
* **ios:** draw the Lock Screen card in two shades of the program's hue ([#919](https://github.com/si-mods/gorex/pull/919))
* **ios:** draw the terminal activity's Lock Screen face as a bold card ([#913](https://github.com/si-mods/gorex/pull/913))

### Bug Fixes

* **ios:** show the program's own OSC 7501 message as the Live Activity line ([#910](https://github.com/si-mods/gorex/pull/910))
* **ios:** show a TUI agent's latest line in the Live Activity, not its status bar ([#906](https://github.com/si-mods/gorex/pull/906))

## 0.44.5

### Features

* **ios:** show the open terminal as a Live Activity ([#900](https://github.com/si-mods/gorex/pull/900))
* **ios:** add the Activity widget ([#892](https://github.com/si-mods/gorex/pull/892))
* **ios:** gather the developer settings on a page of their own ([#899](https://github.com/si-mods/gorex/pull/899))
* **ios:** prism loading indicator and launch screen ([#871](https://github.com/si-mods/gorex/pull/871))
* **desktop:** bounce a needy tab's program tile as iOS's does ([#898](https://github.com/si-mods/gorex/pull/898))

### Bug Fixes

* **ios:** lay the expanded Dynamic Island out under the camera ([#901](https://github.com/si-mods/gorex/pull/901))
* **desktop:** draw a project's folder icon solid, as iOS does ([#895](https://github.com/si-mods/gorex/pull/895))
* **ios:** open a byte stream on the session of its ticket's moment ([#839](https://github.com/si-mods/gorex/pull/839))

### Performance

* **rexapi:** publish the model from the pump; reads use the snapshot ([#893](https://github.com/si-mods/gorex/pull/893))
* **ios:** move terminal drawing, dictation, transfers, sharing and list work off the main thread ([#872](https://github.com/si-mods/gorex/pull/872), [#873](https://github.com/si-mods/gorex/pull/873), [#874](https://github.com/si-mods/gorex/pull/874), [#875](https://github.com/si-mods/gorex/pull/875), [#877](https://github.com/si-mods/gorex/pull/877), [#878](https://github.com/si-mods/gorex/pull/878), [#881](https://github.com/si-mods/gorex/pull/881), [#884](https://github.com/si-mods/gorex/pull/884), [#886](https://github.com/si-mods/gorex/pull/886), [#887](https://github.com/si-mods/gorex/pull/887), [#888](https://github.com/si-mods/gorex/pull/888), [#889](https://github.com/si-mods/gorex/pull/889), [#890](https://github.com/si-mods/gorex/pull/890), [#891](https://github.com/si-mods/gorex/pull/891))

## 0.44.4

### Bug Fixes

* **note:** save a note 400 ms after the last edit, off the UI thread ([#865](https://github.com/si-mods/gorex/pull/865))
* **ios/share:** close a client's WebSockets when Hotspot Share revokes or denies it, or stops ([#866](https://github.com/si-mods/gorex/pull/866))

### Performance

* **desktop:** an idle poll no longer updates every window, and counts the badge once per round ([#867](https://github.com/si-mods/gorex/pull/867))

## 0.44.3

### Features

* **ios:** add a Developer Mode floating connection HUD ([#849](https://github.com/si-mods/gorex/pull/849))

### Bug Fixes

* **projects:** name a shell at its prompt by its shell, not an ended program's title ([#801](https://github.com/si-mods/gorex/pull/801))
* **rex:** read `file.get` and `dir.list` as the session user when `GOREX_SESSION_USER` is set ([#851](https://github.com/si-mods/gorex/pull/851))

## 0.44.2

### Features

* **ui:** add design tokens for type, radius and shadows ([#829](https://github.com/si-mods/gorex/pull/829))

### Bug Fixes

* **projects:** use folder icons for projects on desktop and web ([#832](https://github.com/si-mods/gorex/pull/832))
* **tabs:** stop the active tab painting the system accent over its pill ([#836](https://github.com/si-mods/gorex/pull/836))
* **projects:** keep a working session's dots on its tab's row ([#835](https://github.com/si-mods/gorex/pull/835))
* **ui:** let a program's title that names itself reach its tab's label ([#837](https://github.com/si-mods/gorex/pull/837))

## 0.44.1

### Features

* **hostqr:** open the web client from the pair dialog ([#831](https://github.com/si-mods/gorex/pull/831))

## 0.44.0

### Features

* **settings:** draw the app's icon on the server card ([#822](https://github.com/si-mods/gorex/pull/822))
* set up the gorex skill and session-start hook for Devin ([#824](https://github.com/si-mods/gorex/pull/824))
* **hostqr:** offer the web client's code in the pair dialog ([#828](https://github.com/si-mods/gorex/pull/828))

### Bug Fixes

* **web:** keep the workers.dev address when the custom domain is set ([#823](https://github.com/si-mods/gorex/pull/823))
* **cli:** run Devin's Windows hook through sh, not cmd ([#825](https://github.com/si-mods/gorex/pull/825))
* **rex:** read dir.list in batches and keep only the first names ([#826](https://github.com/si-mods/gorex/pull/826))
* **rex:** use the packaged ConPTY so a program reads terminal replies ([#827](https://github.com/si-mods/gorex/pull/827))

## 0.43.0

### Features

* give the Devin program its brand tile on the desktop, web and iOS ([#818](https://github.com/si-mods/gorex/pull/818))

### Documentation

* **github:** give the PR template Problem, Cause, Fix and Test sections ([#819](https://github.com/si-mods/gorex/pull/819))
* **github:** add screenshots and issue lines to the PR template ([#817](https://github.com/si-mods/gorex/pull/817))

### CI

* fail the PR verified check when the body misses its line ([#815](https://github.com/si-mods/gorex/pull/815))

## 0.42.0

### Features

* **icon:** GoRex and Rex Go have a new icon: a glass prism that splits a white beam into warm light, made in Icon Composer; GoRex Dev shows it on a violet gradient ([#814](https://github.com/si-mods/gorex/pull/814))
* **ios:** a Liquid Glass prism icon, and Settings > App Icon to pick Spectrum or Classic instead ([#808](https://github.com/si-mods/gorex/pull/808), [#811](https://github.com/si-mods/gorex/pull/811), [#812](https://github.com/si-mods/gorex/pull/812))
* **ios:** sync the API key and settings through iCloud ([#798](https://github.com/si-mods/gorex/pull/798))
* **ios:** put the space picker in the sidebar's bar on iPad ([#794](https://github.com/si-mods/gorex/pull/794))
* **ios:** keep a terminal's bar buttons out of a status bar at the side ([#780](https://github.com/si-mods/gorex/pull/780))

### Bug Fixes

* **projects:** keep a named tab's program icon in the sidebar ([#813](https://github.com/si-mods/gorex/pull/813))
* **projects:** take another host's panes out of this machine's layout as it loads ([#799](https://github.com/si-mods/gorex/pull/799))
* **desktop:** drop the green activity dot from tabs and the pane bar ([#807](https://github.com/si-mods/gorex/pull/807))
* **desktop:** projects show no folder, and an empty one opens no tab ([#806](https://github.com/si-mods/gorex/pull/806))
* **ios:** a project's row no longer shows its folder ([#805](https://github.com/si-mods/gorex/pull/805))
* **ios:** tile the tabs with no project instead of folding them ([#802](https://github.com/si-mods/gorex/pull/802))
* **ios:** download a speech model from GitHub when Hugging Face cannot be reached ([#800](https://github.com/si-mods/gorex/pull/800))

### CI

* run only the iOS checks for a change to the iOS app alone ([#809](https://github.com/si-mods/gorex/pull/809))
* **web:** deploy the web client to Cloudflare Workers on web pushes ([#804](https://github.com/si-mods/gorex/pull/804))

## 0.41.2

### Bug Fixes

* **projects:** tabs the window does not show take their session's title and program icon, not "Shell"; a new project no longer names its first tab after itself, and a first tab named so is cleared once; an empty "Tabs with no project" row is hidden; tabs sit flush under their project ([#793](https://github.com/si-mods/gorex/pull/793))
* **web:** a project's tabs start at the project's leading edge, with no guide line ([#792](https://github.com/si-mods/gorex/pull/792))
* **ios:** a project's tabs line up with the project row ([#796](https://github.com/si-mods/gorex/pull/796))
* keep the new pane when a host pane restarts ([#791](https://github.com/si-mods/gorex/pull/791))
* **server:** keep the session user across an in-place upgrade ([#795](https://github.com/si-mods/gorex/pull/795))
* **ios:** make a resume use the session of its moment ([#785](https://github.com/si-mods/gorex/pull/785))

## 0.41.1

### Features

* **projects:** delete a space: its projects move to Default, no tab closes; in the desktop's and Rex Go's Edit Spaces and the web space menu, with `gorex.space.delete` ([#786](https://github.com/si-mods/gorex/pull/786), [#787](https://github.com/si-mods/gorex/pull/787))

### Bug Fixes

* **projects:** each project in the sidebar keeps its own expanded state; choosing one closes no other ([#783](https://github.com/si-mods/gorex/pull/783))
* **release:** fetch ghostty's Zig packages before the web wasm build, which a Zig 0.17 crash in fresh caches broke ([#784](https://github.com/si-mods/gorex/pull/784))

### Build

* cache the ghostty-vt wasm in the web-r2 jobs ([#788](https://github.com/si-mods/gorex/pull/788))

## 0.41.0

### ⚠ BREAKING CHANGES

* **projects:** projects now live on each host, inside a space: Space → Host → Project → Tab → Pane. A host's server keeps its projects (a folder each) and their tabs; the desktop window shows the hosts' layouts. The layout is migrated once as the server loads it, with no shell stopped or started (old layout kept as `layout.json.v039`); tabs of projects homed on another host move there when that host connects ([#776](https://github.com/si-mods/gorex/pull/776), [#779](https://github.com/si-mods/gorex/pull/779))
* **remote:** the Share Layout switch is gone: every client a host lets in (Remote Access, paired devices) now reads its layout: project and space names, project folders, and tab and pane names ([#776](https://github.com/si-mods/gorex/pull/776))

### Features

* **projects:** the sidebar lists each host's projects in order of last use, with Tabs with no project; a host's + makes a project from one of its folders, a project's + opens a tab in its folder; spaces are names on projects, picked atop the sidebar ([#779](https://github.com/si-mods/gorex/pull/779))
* **rexapi:** `gorex.project.create`, `adopt`, `move`, `update`, `gorex.space.rename` and `list`; a session's `project` field with its folder, space, last use and color ([#776](https://github.com/si-mods/gorex/pull/776), [#778](https://github.com/si-mods/gorex/pull/778))
* **web:** projects under each host, inside a space ([#775](https://github.com/si-mods/gorex/pull/775))
* **ios:** projects under each host, inside a space; the iCloud space sync is gone; "Window" reads "Tab" ([#778](https://github.com/si-mods/gorex/pull/778))

### Bug Fixes

* let the verify hook skip instead of failing when go is missing ([#781](https://github.com/si-mods/gorex/pull/781))

### Documentation

* projects: folders on each host, inside a space ([#773](https://github.com/si-mods/gorex/pull/773))

## 0.40.1

### Bug Fixes

* **settings:** start the sidebar's server card at the top on Windows ([#769](https://github.com/si-mods/gorex/pull/769))

## 0.40.0

### Features

* **hosts:** offer the GoRex machines the tailnet finds in the Add Remote Host sheet, each with Add Host; discovery no longer saves or connects to a host by itself, and a host it saved that the user never took goes as the hosts load ([#761](https://github.com/si-mods/gorex/pull/761))

### Chores

* split `state.go` into `app.go`, `float.go` and `browser.go` ([#765](https://github.com/si-mods/gorex/pull/765))
* one `internal/hosts` for hosts.json, read the same by the app, the CLI and the session server ([#768](https://github.com/si-mods/gorex/pull/768))
* guard `gh` targets and local full test runs; slim CLAUDE.md ([#767](https://github.com/si-mods/gorex/pull/767))

## 0.39.0

### Bug Fixes

* **projects:** keep the project popover's actions and the sidebar's New Project in reach with many projects; show each project's folder and an offline or unknown home host; Retry and Change Folder on the home host notes; Remove Folder with Undo; ⇧⌘N opens New Project… ([#756](https://github.com/si-mods/gorex/pull/756))
* **projects:** make the New Project sheet's folder choice clear and keyboard-driven: a short breadcrumb, the folder it will use, readable in light mode ([#757](https://github.com/si-mods/gorex/pull/757))
* **web:** fix the Projects folder sheet (breadcrumb, drive list, missing folder) and tell same-name projects apart ([#754](https://github.com/si-mods/gorex/pull/754))
* **ios:** keep project folders and names in sync with the home host: an offline home host reads as offline, a rename keeps the folder and leaves no empty project ([#755](https://github.com/si-mods/gorex/pull/755))
* **ios:** make projects clearer to use: New Project always in reach, choose a folder from its row, one word for windows, delete asks first ([#760](https://github.com/si-mods/gorex/pull/760))
* **rex:** refuse device paths from list_dir, set_home and file.put ([#759](https://github.com/si-mods/gorex/pull/759))

### Documentation

* keep local test runs to the changed packages ([#764](https://github.com/si-mods/gorex/pull/764))

### Chores

* remove dead code, share duplicated helpers, add a maintenance audit ([#758](https://github.com/si-mods/gorex/pull/758))

## 0.38.0

### Features

* **projects:** a project is bound to a folder on a home host; New Tab in a project starts there, and a missing folder or offline home host is reported instead of opening elsewhere ([#722](https://github.com/si-mods/gorex/pull/722))
* **projects:** New Project sheet with a host picker and folder browser, Change Folder and Remove Folder, and the folder under the project's name ([#746](https://github.com/si-mods/gorex/pull/746))
* **web:** New Project with a folder browser, and new windows in a project's folder on its home host ([#741](https://github.com/si-mods/gorex/pull/741))
* **ios:** bind Rex Go projects to a home folder ([#743](https://github.com/si-mods/gorex/pull/743))
* **ios:** add a gallery of the images a pane shows ([#736](https://github.com/si-mods/gorex/pull/736))
* **ios:** close a single pane from the menu or the pane bar ([#734](https://github.com/si-mods/gorex/pull/734))

### Bug Fixes

* **rex:** refuse a file get through a link that leaves its folder ([#732](https://github.com/si-mods/gorex/pull/732))
* **rex:** open no file once a download's context is done, so a cancelled download keeps its partial ([#748](https://github.com/si-mods/gorex/pull/748))
* **ios:** ask before a program's file link downloads, refuse UNC paths ([#729](https://github.com/si-mods/gorex/pull/729))
* **ios:** keep pane switches from bringing the keyboard back ([#733](https://github.com/si-mods/gorex/pull/733))
* **ios:** drop gallery images whose file is missing ([#744](https://github.com/si-mods/gorex/pull/744))
* **ios:** collect image paths a program wrapped across rows ([#749](https://github.com/si-mods/gorex/pull/749))

### Documentation

* projects, Rex API and Rex Go sync docs ([#747](https://github.com/si-mods/gorex/pull/747))

## 0.37.0

### Features

* **rexapi:** require a pairing token on the local-network listener ([#684](https://github.com/si-mods/gorex/pull/684))
* **rex:** list a host's directories with `dir.list` and `gorex.dir.list` ([#717](https://github.com/si-mods/gorex/pull/717))
* **ios:** open a file path tapped in the terminal ([#721](https://github.com/si-mods/gorex/pull/721))

### Bug Fixes

* **rex:** refuse Windows device and named-pipe paths in file gets and directory listings ([#723](https://github.com/si-mods/gorex/pull/723))
* **panes:** resync a pane's screen in band on its stream ([#690](https://github.com/si-mods/gorex/pull/690))
* **layout:** go on after a take refused for a change already on its way ([#706](https://github.com/si-mods/gorex/pull/706))
* **tailnet:** replace a restarted server's tailcat client after one try ([#713](https://github.com/si-mods/gorex/pull/713))
* **tailnet:** keep the built-in node off until the user turns it on ([#716](https://github.com/si-mods/gorex/pull/716))
* **ios:** reset a window drag let go of off the rows on iOS 26 ([#693](https://github.com/si-mods/gorex/pull/693))
* **ios:** give the title pill and pane bar press feedback ([#718](https://github.com/si-mods/gorex/pull/718))
* **ios:** disable the two-finger pane swipe for now ([#677](https://github.com/si-mods/gorex/pull/677))

## 0.36.0

### Features

* **tailnet:** run desktop Tailscale discovery on the built-in node on Windows and macOS ([#573](https://github.com/si-mods/gorex/pull/573))
* **rexapi:** serve `server.stop` ([#675](https://github.com/si-mods/gorex/pull/675))
* **hosts:** send pane and tab renames to the remote host ([#686](https://github.com/si-mods/gorex/pull/686))

### Bug Fixes

* **layout:** keep a revision told while the outbox reads the layout ([#692](https://github.com/si-mods/gorex/pull/692))

### Maintenance

* remove code no build uses ([#672](https://github.com/si-mods/gorex/pull/672))

## 0.35.0

### Features

* **linux:** round the windows' corners ([#576](https://github.com/si-mods/gorex/pull/576))

### Bug Fixes

* **rex:** resync a lagging client in band instead of dropping it ([#673](https://github.com/si-mods/gorex/pull/673))
* **ios:** make the two-finger pane swipe follow the fingers ([#665](https://github.com/si-mods/gorex/pull/665))
* **ios:** let shared space references correct discovered memberships ([#666](https://github.com/si-mods/gorex/pull/666))
* **linux:** keep the pane bar under window buttons on the right with a left sidebar ([#575](https://github.com/si-mods/gorex/pull/575))

### Performance Improvements

* **ios:** keep a window's panes connected so a pane switch is instant ([#667](https://github.com/si-mods/gorex/pull/667))

## 0.34.0

### Bug Fixes

* **panes:** log why a pane's stream was lost and when it came back, in `app.log` ([#663](https://github.com/si-mods/gorex/pull/663))

## 0.33.0

### Features

* **ios:** split a pane from a single-pane window ([#660](https://github.com/si-mods/gorex/pull/660))
* **ios:** switch panes with a two-finger swipe, and keep the pane map as an indicator ([#653](https://github.com/si-mods/gorex/pull/653))

### Bug Fixes

* answer a new terminal's color queries in the app's appearance ([#661](https://github.com/si-mods/gorex/pull/661))
* one recovery policy for a lost terminal stream ([#654](https://github.com/si-mods/gorex/pull/654))
* **ios:** answer a notification tap on the main thread ([#655](https://github.com/si-mods/gorex/pull/655))
* **ios:** cut a long title to the pill ([#657](https://github.com/si-mods/gorex/pull/657))
* **ios:** keep a window's title on one line in the title menu ([#644](https://github.com/si-mods/gorex/pull/644))
* **ios:** the title opens the session's sheet on a long press too ([#659](https://github.com/si-mods/gorex/pull/659))
* **tailnet:** quiet the two test flakes reddening the linux shard, and let a test-only change owe no tester bullet ([#658](https://github.com/si-mods/gorex/pull/658))

## 0.32.0

### Features

* **ios:** switch a tab's panes like Rex Go ([#639](https://github.com/si-mods/gorex/pull/639))
* **sidebar:** join the sidebar's head and the pane bar into one title bar on Windows ([#650](https://github.com/si-mods/gorex/pull/650))

### Bug Fixes

* **ios:** keep the terminal above the bottom bars and fade what scrolls under them ([#648](https://github.com/si-mods/gorex/pull/648))
* **ios:** show the reconnect note above the key bar ([#643](https://github.com/si-mods/gorex/pull/643))
* **ios:** tap the hand as the key bar's mic is pressed ([#642](https://github.com/si-mods/gorex/pull/642))
* **ios:** tap the hand on the voice capsule's buttons ([#646](https://github.com/si-mods/gorex/pull/646))
* **panes:** attach a pane of this machine again when its stream is lost ([#641](https://github.com/si-mods/gorex/pull/641))
* **sidebar:** drop the space row's icon and working dots ([#649](https://github.com/si-mods/gorex/pull/649))
* **testflight:** set What to Test again, and say why not in seconds ([#645](https://github.com/si-mods/gorex/pull/645))

## 0.31.0

### Features

* **desktop:** adopt mygo 0.9.1's corner radius, concealed copies, WithAccent and dev -background ([#625](https://github.com/si-mods/gorex/pull/625))
* **ios:** add observable Space Sync settings view ([#626](https://github.com/si-mods/gorex/pull/626))
* **rexapi,web:** add WebTransport serverCertificateHashes LAN pairing ([#629](https://github.com/si-mods/gorex/pull/629))

### Bug Fixes

* **ios:** keep bottom key bar background transparent ([#631](https://github.com/si-mods/gorex/pull/631))
* **resume:** bypass shell functions when resuming Claude ([#632](https://github.com/si-mods/gorex/pull/632))
* **web:** name the local network permission that blocked a WebTransport host ([#635](https://github.com/si-mods/gorex/pull/635))
* **web:** say why a WebTransport host did not answer ([#633](https://github.com/si-mods/gorex/pull/633))

## 0.30.0

### Features

* **ios:** add a gradient blur fade behind the bottom key bar ([#616](https://github.com/si-mods/gorex/pull/616))

### Bug Fixes

* **deps:** sync si-mods/mygo with upstream 0.9.1: a wrapped link opens whole, scrolled rows are reused, and a killed program's mouse and key reports turn off ([#622](https://github.com/si-mods/gorex/pull/622))
* **ios:** open the next terminal before the tab switch slides ([#615](https://github.com/si-mods/gorex/pull/615))
* **ios:** eliminate the upward jump on the header pill's tab switch ([#600](https://github.com/si-mods/gorex/pull/600))
* **ui:** sync renamed remote pane and tab titles ([#621](https://github.com/si-mods/gorex/pull/621))

## 0.29.0

### Features

* **ios:** add a centralized SpaceStore for cross-host spaces ([#580](https://github.com/si-mods/gorex/pull/580))
* **ios:** sync spaces over iCloud KVS, and on the desktop passively ([#581](https://github.com/si-mods/gorex/pull/581))
* **ios:** add a scroll-to-bottom button for full-screen and mouse-captured TUIs ([#601](https://github.com/si-mods/gorex/pull/601))
* **ios:** transition the KeyBar into a horizontal voice capsule while dictating ([#591](https://github.com/si-mods/gorex/pull/591))
* **ios:** improve terminal touch gestures, scroll-to-bottom, tab switching, and live font updates ([#589](https://github.com/si-mods/gorex/pull/589))
* **ios:** animate program tiles by session state, like the macOS Dock's bounce ([#594](https://github.com/si-mods/gorex/pull/594))
* **ios:** animate the header pill's drag and the tab switch's slide ([#593](https://github.com/si-mods/gorex/pull/593))
* **rex:** run sessions as another user, with the server's data kept to itself ([#609](https://github.com/si-mods/gorex/pull/609))
* **rexapi,web,ios:** align with official Rex API revision 14 ([#596](https://github.com/si-mods/gorex/pull/596))
* **ui:** show an animated three-dot working indicator on desktop ([#604](https://github.com/si-mods/gorex/pull/604))
* **ui:** animate the active tab pill and the tab content's slide on switch ([#592](https://github.com/si-mods/gorex/pull/592))

### Bug Fixes

* **ios:** show when a terminal reconnects, and restart a lost one when its host is back ([#610](https://github.com/si-mods/gorex/pull/610))
* **ios:** remove the tile bounce animation for the working state ([#603](https://github.com/si-mods/gorex/pull/603))
* **ios:** show animated working dots and an error dot in window rows ([#602](https://github.com/si-mods/gorex/pull/602))
* **ios:** use a repeating KeyframeAnimator so active and done program tiles keep bouncing ([#598](https://github.com/si-mods/gorex/pull/598))
* **ios:** prevent the header pill's swipe from opening the session sheet ([#597](https://github.com/si-mods/gorex/pull/597))
* **ios:** fade the TopEdge blur smoothly through UIKit's transition coordinator ([#595](https://github.com/si-mods/gorex/pull/595))
* **ios:** drop the TopEdge material blur during navigation transitions ([#590](https://github.com/si-mods/gorex/pull/590))
* **ios:** sync host colors and apply theme changes live ([#584](https://github.com/si-mods/gorex/pull/584))
* **ios:** vertically center the title in StatusRow when its detail is empty ([#586](https://github.com/si-mods/gorex/pull/586))
* **ios:** support selectedTextRange edits in InputDocument for the Doubao IME ([#585](https://github.com/si-mods/gorex/pull/585))
* **ios:** resolve the session ID after spaces_apply on a new session or window ([#583](https://github.com/si-mods/gorex/pull/583))
* **ios:** add zh-Hans localizations for the centralized Space UI ([#582](https://github.com/si-mods/gorex/pull/582))
* **ui:** render header icons at natural SF Symbol proportions, like Rex ([#611](https://github.com/si-mods/gorex/pull/611))
* **ui:** unify header icon button sizes to 16 ([#608](https://github.com/si-mods/gorex/pull/608))
* **ui:** unify sidebar icon button sizes ([#607](https://github.com/si-mods/gorex/pull/607))
* **ui:** use a circular hover radius for icon buttons and space dot buttons ([#605](https://github.com/si-mods/gorex/pull/605))
* **web:** fall back when ResizeObserver's device-pixel-content-box is unsupported ([#578](https://github.com/si-mods/gorex/pull/578))
* **web:** adapt the above-tabs bar for narrow mobile viewports ([#577](https://github.com/si-mods/gorex/pull/577))

## 0.28.1

### Bug Fixes

* **rex:** on macOS, run the server from a copy of the app, as a job of `launchd`'s, so that an update, a crash or a quit of the app no longer ends the sessions and the conversations in their panes ([#572](https://github.com/si-mods/gorex/pull/572))

## 0.28.0

### Features

* **cli:** draw a server's pairing QR with `gorex pair` ([#567](https://github.com/si-mods/gorex/pull/567))

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
  `docs/REF-004-shared-server.md`.
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
