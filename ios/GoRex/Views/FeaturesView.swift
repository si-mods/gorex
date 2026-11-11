import SwiftUI

/// Every feature of the app, by area: a card of what the app is, then a
/// card of each area's features, and a search over them.
struct FeaturesView: View {
    @State private var query = ""

    private typealias Feature = (title: LocalizedStringResource, detail: LocalizedStringResource)

    private static let sections: [(title: LocalizedStringResource, features: [Feature])] = [
        ("Home & Hosts", [
            ("Every host in one list", "Each host lists its sessions as cards, and each session its windows, with the tiles of the programs in them, as the desktop's tabs show them."),
            ("Fold hosts and sessions", "Tap a host's name to fold its sessions away, or a session's chevron to fold its windows."),
            ("New sessions and windows", "The + on a session's card opens a new window in it. The host's ⋯ menu has New Session, Reconnect and Remove."),
            ("Long-press a window for its menu", "Rename Tab, Reset Name, Move Up, Move Down, New Tab, Close Tab and Close Other Tabs, as on the desktop."),
            ("Long-press a session to rename or close it", "Close Session asks first, as it closes every window in the session."),
            ("Activity under the eye", "The eye lists the windows of every host by what their programs need: waiting for input first, then failed, done and working, with how long ago."),
            ("Search windows", "The search at the bottom filters the windows by title, in the list and under the eye."),
            ("Liquid Glass", "The list, the buttons, the sheets and the key bar use the system's Liquid Glass."),
        ]),
        ("Terminal", [
            ("A Ghostty terminal", "Ghostty draws each window with Metal and encodes its keys, as Ghostty does on the Mac."),
            ("Scroll with the system's physics", "A drag follows the finger, a flick coasts and slows, and the ends bounce. Full-screen programs and those that use the mouse get the scroll as wheel events."),
            ("Pinch to size the text", "Each step is one point with a light tap, and the grid's columns and rows show for a moment. The size stays for every terminal."),
            ("Tap to click", "A program that uses the mouse gets a tap as a click, and the keyboard stays up."),
            ("The keyboard slides the terminal", "The terminal does not resize as the keyboard comes and goes. It slides up only as far as the cursor and the lines under it need."),
            ("Edge to edge", "The terminal's background fills the screen. Text under the status bar blurs and fades, so the clock stays legible."),
            ("The title opens the session", "The title shows the window's program and title. Tap it for the session's windows, New Tab and Switch Session."),
            ("A line of progress", "A program's progress (OSC 9;4) shows as a thin line at the top, red on an error."),
            ("Back where you left it", "Back from the background, the terminal connects again and every title stays. A dropped connection tries again by itself."),
            ("Diagnostics", "A terminal that can't connect offers Diagnostics: where it stopped, the path to the host, pings of three sizes and the recent log, to copy into a report. A GoRex machine in Settings › Tailscale opens the same."),
            ("Opens in its own colors", "A terminal opens in the background last seen on its host, so it does not flash."),
        ]),
        ("Keyboard & Input", [
            ("A key bar over the keyboard", "Three glass capsules: ⌘, a row of keys, and an arrow stick with the key that hides the keyboard."),
            ("Two pages of keys", "Swipe the middle capsule from Esc, Control, Option, Shift and Tab to ~ | / - and Backspace."),
            ("Modifiers for the next key", "Tap Control, Option or Shift, then a key, for a chord. They let go after that key."),
            ("An arrow stick", "Drag the stick for arrows, which repeat while you hold it. The modifiers hold with it."),
            ("Hold Backspace to repeat", "Backspace on the key bar repeats while held, as the keyboard's does."),
            ("Keys that tap back", "Every key on the bar gives a light tap as you press it."),
            ("Chinese and other input methods", "Pinyin and other input methods compose at the cursor, with their candidates beside it, and send the text once it is done."),
            ("Dictation", "Dictate into the terminal. A correction erases what was dictated and types it again."),
            ("Speak to a host without a microphone", "Hold the mic at the top right of the terminal to speak to Claude Code's /voice on a host that has no microphone. It shows while Claude Code runs on a host that takes voice, in place of the ⋯ menu."),
            ("Move the cursor with the spacebar", "Hold the spacebar and slide to move the cursor, one arrow for each cell."),
            ("Hardware keyboards", "Option is Alt, chords with Control, Shift or Option reach the program, and held keys repeat."),
            ("⌘ shortcuts", "⌘T opens a tab, ⌘W closes it, ⌘O adds a file, ⇧⌘O a photo, ⌘K clears the screen and scrollback, and ⌘+, ⌘- and ⌘0 size the text. Hold ⌘ to see them."),
        ]),
        ("Selection & Links", [
            ("Long-press to select", "A long press selects the word under the finger, with the loupe. Drag to select more; past the top or bottom the terminal scrolls on."),
            ("Selection handles", "Two handles move the ends of the selection, and a menu has Copy, Paste and Select All."),
            ("Select over the mouse", "Selection works while a program uses the mouse, and the program does not get those touches."),
            ("Tap a link to open it", "Web and mail links in the output open with a tap, as do links a program marks with OSC 8. A link followed by Chinese ends where its address does."),
            ("Paste", "Paste from the ⌘ menu on the key bar, from the selection's menu, or with ⌘V."),
        ]),
        ("Files & Photos", [
            ("Add Photo", "Photos go to the host's temporary folder as their original files, and their paths are pasted into the terminal."),
            ("Add File", "Files from the Files app go to the host the same way, and their paths are pasted."),
            ("Paths ready for the shell", "Pasted paths are quoted for the shell and end with a space. A name the shell cannot take is not pasted, and an alert says why."),
            ("A half-height photo picker", "The photo picker opens at half the screen, with the terminal above it. Pull it up for the full screen."),
            ("Upload progress", "⌘ on the key bar spins while files go up, and an alert tells of an upload that failed."),
        ]),
        ("Notifications", [
            ("When a program needs input", "When an agent such as Claude Code waits for you, a notification says so, while the app is open or soon after you leave it."),
            ("Programs' notifications", "Notifications that programs send with OSC 9 or OSC 777 show as the system's, grouped by window."),
            ("Tap to open the terminal", "A notification opens its terminal. The terminal on screen sends none."),
            ("Bells", "A bell gives a light tap, at most two a second."),
        ]),
        ("iPad", [
            ("The list beside the terminal", "On iPad and in wide windows, the list stays beside the terminal that is open."),
            ("Keys in the shortcuts bar", "The key bar's keys and four repeating arrows go in the keyboard's shortcuts bar, which shows with a hardware keyboard too and gives a Magic Keyboard its Esc."),
            ("Pointer and trackpad", "Click and drag to select as with a mouse, scroll with two fingers, and rows highlight under the pointer."),
        ]),
        ("Appearance", [
            ("System, Dark or Light", "The interface follows the system, or stays dark or light."),
            ("Themes with a preview", "861 Ghostty themes, each with a swatch, a filter of All, Dark and Light, and a search. A preview shows the chosen one in the terminal's font."),
            ("Host Colors", "Keep the colors of the host's terminal in place of a theme."),
            ("Fonts and sizes", "JetBrains Mono, Menlo or Courier New, from 8 to 24 points."),
            ("Maple Mono NF CN", "A font with Chinese and Nerd Font glyphs, to download. Only its four faces download, about 40 MB; stop it or delete it at any time."),
        ]),
        ("Hosts & Pairing", [
            ("Pair by QR code", "Scan the Rex QR code that GoRex or Rex shows to add a host. A host you have already opens instead."),
            ("Set up a host by hand", "Enter a server's address, its name, and its icon."),
            ("Startup command", "The command new terminals on a host run in place of its login shell."),
            ("Remote Hosts", "Edit the list to delete or reorder hosts, and ⓘ for a host's details."),
            ("A label for this device", "Name this device in Settings. Hosts see the label when it connects."),
        ]),
        ("Settings & About", [
            ("Search Settings", "The search at the bottom of Settings filters its rows."),
            ("About", "The version, the device, iOS and each host's Rex version. Copy version or debug info for a problem report."),
            ("Open source", "The licenses of the code, fonts and themes the app ships, and its source on GitHub."),
        ]),
    ]

    private var shown: [(title: LocalizedStringResource, features: [Feature])] {
        Self.sections.map { s in
            (s.title, s.features.filter {
                query.isEmpty || String(localized: $0.title).localizedCaseInsensitiveContains(query)
                    || String(localized: $0.detail).localizedCaseInsensitiveContains(query)
            })
        }.filter { !$0.features.isEmpty }
    }

    var body: some View {
        List {
            if query.isEmpty {
                Section {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("\(Self.sections.map(\.features.count).reduce(0, +)) Features and Details").font(.title.bold())
                        Text("Rex Go puts the terminals of your computers on your iPhone and iPad: a Ghostty terminal with a key bar, selection, scrolling and input made for touch.")
                            .foregroundStyle(.secondary)
                        Text("\(Bundle.main.version) · Full Catalog").font(.footnote).foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 8)
                }
                .listRowBackground(Color.card)
            }
            ForEach(shown, id: \.title.key) { s in
                Section {
                    ForEach(s.features, id: \.title.key) { f in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(f.title).font(.headline)
                            Text(f.detail).font(.subheadline).foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 4)
                    }
                } header: {
                    Text(s.title)
                }
                .listRowBackground(Color.card)
            }
        }
        .overlay { if shown.isEmpty { ContentUnavailableView.search(text: query) } }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .searchable(text: $query, prompt: "Search Features and Details")
        .autocorrectionDisabled()
        .navigationTitle("Features")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { DefaultToolbarItem(kind: .search, placement: .bottomBar) }
    }
}
