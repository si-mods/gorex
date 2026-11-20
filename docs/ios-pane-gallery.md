# Rex Go iOS: pane gallery

Status: built (#736, #744, #749).

This doc defines a gallery of images for each terminal pane in Rex Go on iOS. The build differs from the design in some places. Section 9 lists the differences. The gallery reuses the download code of the section "The iOS client" in `docs/web-open-files.md` (#721: `FileLink`, and `OpenedFiles` in `TerminalView+Open.swift`).

## 1. Problem

An agent such as Claude Code writes the images that it makes to files on the host. It prints only the path in its output. A program such as omp draws images directly into the terminal with the Kitty image protocol. Both kinds of image scroll away with the output. A screenshot or a chart that appeared in a conversation is hard to find later.

This doc adds a gallery for each terminal pane on iOS. The first item of the "More" menu at the top right of the terminal opens the gallery of the current pane. A grid lists all images that this pane showed. The user taps one image to see it full screen, and swipes left or right to change images, as in the system "Photos" app.

## 2. Scope

- Collection: the iOS app scans the terminal output that it receives. The server does not change. The Rex API does not change.
- The gallery has only two sources of images:
  1. Image file paths that the output prints.
  2. Kitty inline images.
- The gallery does not collect images that the user uploads to the agent.
- The gallery belongs to a pane (host plus pane `block_id`). Each pane has one gallery. Other panes of the same session do not share it.

Known limit: the phone may be disconnected while the pane makes output. iOS then sees that output only in the snapshot that it gets at reconnect. The snapshot holds only the screen and the scrollback that the server keeps. The gallery misses images outside this range. This is the cost of the choice to scan on iOS.

## 3. Collection

### 3.1 Where the scan runs

`TerminalSession.receive` (`ios/GoRex/Terminal/TerminalSession.swift`) is the one place where all output enters the terminal. It handles two events:

- `.snapshot`: the snapshot at connect or reconnect. `VTerminal.replay()` makes the bytes to write. The scanner scans these bytes.
- `.output`: live output. The scanner scans the raw bytes.

The scanner is a nonisolated type of pure logic. It keeps state for each pane. A path or a Kitty sequence can split between two `receive` calls. The scan runs in the background. It must not block the write to the terminal.

The output bytes supply Kitty images, OSC 8 links and printed image paths. The screen text supplies printed image paths too (section 3.4). Claude Code and similar programs wrap a long path to the screen width over several rows. They separate the rows with a line feed or a cursor move. The next row also has a hanging indent. So no row in the output bytes holds the full path. A user reported this on 2026-11-18. A Windows host printed `C:\Users\…\shots\*.png`, and the user received none of the images.

### 3.2 Image paths

- The recognition rules are the same as the rules of "tap a path to open the file". The scanner reuses the path rules of that section: `FileLink.target` (`ios/GoRex/Terminal/FileLink.swift`), which follows `pathAt` of `web/src/links.ts`. It also reuses the parse of OSC 8 `file://` links. The scanner collects an OSC 8 link only when the visible text is the target path. Suppose the target differs from the text. Then the app must show the real target to the user. It must ask for a confirmation before it downloads the file (`docs/lessons.md`, "Link whose target differs from its text"). The gallery downloads thumbnails automatically, so it must not collect such a link.
- The scanner also collects Windows absolute paths. These are a drive letter followed by `\` or `/` (`C:\dir\x.png`, `C:/dir/x.png`). The scanner does not collect UNC paths (`\\server\share\x.png`, `//server/share/x.png`) or device paths. The server refuses them. A Windows host that reads a UNC path also connects to that machine (`docs/lessons.md`, "Untrusted path to the host's filesystem"). Inside quotes, the scanner reads up to the matching quote, so paths with spaces are collected too. Claude Code on a Windows host prints this kind of path. A tap on such a path still does not open the file. `FileLink` does not accept a backslash or `:`, the same as the desktop and the web client. The gallery is the way to see these images.
- Mouse tracking does not affect collection. A program such as Claude Code turns on mouse tracking. A tap on a path then goes to the program. The gallery still lists these images.
- The scanner collects only paths with an image extension: `png`, `jpg`, `jpeg`, `gif`, `webp`, `heic`, `avif`, `bmp`, `tiff`. It does not collect SVG, because neither ImageIO nor UIImage can draw it.
- The scanner collects paths from two places: the output bytes and the screen text. Before the scan, it removes control sequences (CSI, OSC and others) from the output bytes and reads only the visible text. It does not join a path across rows in the output bytes. The screen text supplies the joined long paths (section 3.4).
- The app stores a relative path in the form in which it was recorded. When the user opens the image, the server resolves the path against the current directory of the foreground program, as for a tap-to-open. After the current directory changes, a relative path may fail to open. An entry that fails to open shows as "cannot read". The app does not remove it automatically. "File does not exist" gets the same treatment. For a relative path, this answer does not prove that the file is absent.
- The app does not contact the host at record time. It does not check that the file exists. It downloads the thumbnail the first time the thumbnail loads. An entry whose download fails shows a placeholder.
- The thumbnail or original of an absolute path may fail to download. An absolute path starts with `/`, `~/` or a drive letter. The host then answers "file does not exist" (`no_file`). The app then removes that entry from the gallery. It does not remove relative paths (see the previous item). Paths that an agent mentions in text are often examples, or files that do not exist yet (a user, 2026-11-17). The app does not add a removed path to the removal list. So the path is collected again when it appears later in live output. If the file exists by then, the entry shows normally. The snapshot at reconnect still holds the original line of text. So the app adds the removed path to a "does not exist" list in `index.json` (at most 2000 entries). The scanner does not collect the path when it sees it in a snapshot, and does not mark it as a new image. The scanner collects it again only when live output prints it again. It then removes the path from the list. Other errors (permission, folder, too large, connection lost) still show a placeholder. The entry stays.
- The user may view an image full screen while the app removes it because the file does not exist. The viewer then moves to the next image. If the gallery becomes empty, the viewer closes.
- The download limit for a thumbnail file is 32 MiB. The app downloads at most 3 files at the same time. For an image over 50 million pixels, the app makes no thumbnail and shows a placeholder. When the user opens the gallery, the app downloads the thumbnails of all visible cells at once. These limits prevent memory exhaustion.
- The scanner does not collect paths in hidden text (SGR 8). The user cannot see such text, and the gallery downloads thumbnails automatically.
- Deduplication: one pane keeps one entry for one path. The time is the time of the first appearance.

### 3.3 Kitty inline images

- The scanner extracts `ESC _ G … ESC \` sequences from the output bytes. The server already rewrites image files into inline data (`internal/rex/graphics.go`). So iOS handles only inline `t=d`.
- The scanner joins the data of one image by the chunk rule of Kitty (`m=1` means a continuation chunk). Then it decodes base64:
  - `f=100` (PNG): the app saves the PNG as it is.
  - `f=24` and `f=32` (RGB and RGBA raw pixels, with `s` and `v` for the size): the app converts the data to PNG and saves it.
  - With compression `o=z`: the app decompresses with zlib first.
  - Other formats: the scanner skips them.
- The limit for one image is 4 MiB (the same as `maxGraphicsImage` of the server). The scanner skips larger images. It also skips an image with a width or height over 16384 pixels, or with more than 50 million pixels in total. The size comes from the output of a program, so the app does not trust it. The multiplication can overflow, and the decode can exhaust memory.
- In one pane, a new image with the same image number (`i=`) can arrive within 2 seconds of the previous one. It then replaces the previous image and adds no entry. A program that refreshes an animation or a chart frame by frame with one number leaves only the last frame in the gallery.
- Deduplication: by the SHA-256 of the image data. The server replays the image history at reconnect. The gallery does not record the same image twice.

### 3.4 Screen text

- The terminal output stops for 0.3 seconds. The app then reads the screen text from Ghostty (`ghostty_surface_read_text`) and gives it to the scanner. The scanner scans it in the background. The app reads the text as the terminal drew it. Cursor moves of the program, partial redraws and rewrites by ConPTY have already taken effect. The read runs on the write queue of `TerminalView`. It does not use the main thread. The release of the surface runs on the same queue. So the surface is always valid during the read.
- Read range: the app reads the whole scrollback and the screen when two conditions are true. The scrollback is at most 200 lines. The scrollback plus the screen is at most 300 lines. Otherwise it reads only the screen. The coordinates of Ghostty cannot locate one line of scrollback above the screen. Reading the whole scrollback has no limit.
- The app also reads once after the snapshot of a connect or reconnect is written to the terminal. While output does not stop, the app reads at most once every 2 seconds. It skips a read when the text is exactly the same as the last text read.
- Ghostty has already joined soft wraps (automatic wraps of the terminal) into one line when it reads.
- Joining hard wraps: the scanner joins two rows when all of these conditions are true:
  - A row fills the whole row width, measured in screen columns (CJK and emoji count as 2).
  - The row ends with a path character.
  - After the app removes the leading spaces, the next row starts with a path character.
  - The last word of the first row itself looks like the start of a path. It contains `/` or `\`, or it starts with `~`, `.` or a drive letter.
  
  The scanner joins at most 32 rows in a chain. Claude Code wraps by word, so it often pushes a long path whole to the next row. Suppose the previous row is exactly full and ends with an ordinary word such as `to`. The scanner does not join the rows. If it did, it would build a path such as `to/tmp/x.png` that the user never saw. It would also remove the real `/tmp/x.png` as a fragment. The scanner uses the joined result only when the join gives an image path that neither row has alone. Otherwise it scans the original rows. Suppose the end of the previous row is already a complete image path, and the start of the next row is also an image path. The scanner does not join them. This prevents the join of two paths in a list into one path.
- The scanner treats the result of a screen scan as a snapshot (section 3.2). It does not add back the paths that the user removed, or the paths in the "does not exist" list. It deduplicates by pane. Reading the same screen again makes no new entry and no new-image mark.
- The scan of the output-byte paths stays. It collects paths that scroll away fast. When a path wraps, the byte scan gets only the second part, for example `apping-test/x.png`. After the screen join succeeds, the scanner removes these fragments. It does not add them to the removal list or the "does not exist" list. It adds them to an in-memory "fragment" list for each pane (at most 500 entries). The byte scan meets these fragments again in a reconnect snapshot, or when a program redraws the whole conversation. It then does not collect them, and does not mark them as new images.
- Hidden text (SGR 8): Ghostty reads hidden characters as they are in the original. The byte scan records paths in hidden text in an in-memory list for each pane (at most 500 entries). The screen scan skips these paths. A program may fold a hidden path over several rows. This method does not recognize that case, and the app still collects the joined path. This case is rare. The design does not handle it now.
- The scanner cannot collect a path that is outside the read range and not in the snapshot.

## 4. Storage

- Location: `Gallery/<host>/<block_id>/` in the Application Support directory of the app. The app does not put the files in the Photos library. It does not upload them.
- `index.json`: the list of entries. Each entry has `id`, the source (`path` or `kitty`), the path or file name, the time of the first appearance, and the size (when known).
- The app saves the PNG of a Kitty image locally. It is the original. For a path image, the app caches only the thumbnail (longest side 512 px). For full-screen view, the app downloads the original again. It removes the temporary file of the original when the view ends.
- Each pane has at most 500 entries and at most 256 MiB of file size in total. When either limit is exceeded, the app removes the oldest entries and their files.
- The app records an entry that the user removes ("Remove from gallery", "Clear gallery") in the removal list of `index.json` (a path or an image hash). After a reconnect, the app does not add back the snapshot images or the images that the server replays. The removal list holds at most 2000 entries. The app removes the oldest first.
- After the pane closes on the host, the gallery stays until the user removes it, or until 30 days pass without an open.

## 5. Interface

### 5.1 Entry

- The first item of the "More" menu at the top right of the terminal page is "Gallery" (`TerminalScreen.swift`, `ToolbarItemGroup(placement: .topBarTrailing)`). It shows the number of images. Its icon is the SF Symbol `photo.on.rectangle`. The toolbar gets no new button. Back, the title, the microphone and More already fill the header. One more button would squeeze the title into an ellipsis (a user, 2026-11-17).
- The "More" button gets no small dot (a user, 2026-11-17). When the gallery has new images that the user has not seen, only the "Gallery" item in the menu shows the new-image mark. The mark disappears after the user opens the gallery.
- When the gallery has no images, the "Gallery" item still shows. It opens an empty state.

### 5.2 Gallery page

- It opens as a full-screen sheet. The colors follow the terminal (the same `colorScheme` rule as SessionSheet).
- The title is the pane name (the same as the title capsule). The subtitle is the number of images.
- Grid: square thumbnails. An iPhone has 3 columns in portrait and 5 columns in landscape. An iPad adapts to the width. The gap between images is 2 pt, the same as "Photos".
- Order: by time, with the newest at the bottom. The grid scrolls to the bottom when it opens.
- A small icon at the lower left of each thumbnail shows the source. A path image has the SF Symbol `doc`. A Kitty image has no mark.
- With no images, the page shows an empty state. It has one sentence that says which two kinds of image the gallery collects.
- A long press on a thumbnail opens a menu: "Copy path" (for path images), "Share", "Remove from gallery".
- The menu at the top right has "Select" (select several, then share or remove) and "Clear gallery" (needs a confirmation).

### 5.3 Full-screen view

- A tap on a thumbnail opens the full-screen view. A swipe left or right changes the image. A two-finger pinch zooms. A swipe down closes the view. A single tap shows or hides the toolbar.
- The app decodes the full-screen image at reduced size. The long side is at most the long side of the screen in points times 3 (the native pixels of an @3x device). The app does not decode the original resolution.
- The toolbar at the bottom has Share (the system share sheet, which can save to "Photos"). The gallery belongs to the current pane only, so it has no "go back to the source pane".
- The top shows the file name and the time of the first appearance.
- A path image shows progress while the original loads. When the load fails, the view shows an error. The error text is the same as for "tap a path to open the file".
- Files that are not images do not enter the gallery. So the full-screen view handles only images and does not use QuickLook.

## 6. Tests

- XCTest:
  - Path scan: a path that splits across two `receive` calls. A path with color sequences. A non-image extension is not collected. A repeated path is recorded once.
  - Kitty: chunk joining. PNG, RGB, RGBA and zlib. Images over the limits are skipped. A replayed image is deduplicated by hash.
  - Storage: the entry limit evicts the oldest entry. The index reads and writes in a round trip.
- Simulator (on a Mac, `gorex-ui-testing`): use a temporary server and made-up images. In a session, print two image paths. Output one Kitty image with `kitty +icat` or an equivalent `printf`. Check these points: the "Gallery" menu item shows the new-image mark. The gallery lists 3 images. Full-screen swipe, zoom and the share sheet work. No duplicates appear after a disconnect and reconnect. Screenshots use made-up data.

## 7. Non-goals

- Collection on the server, or gallery sync across devices.
- Images that the user uploads to the agent.
- Sixel and iTerm2 OSC 1337 images.
- Video, PDF and other files.

## 8. Decisions

- The gallery belongs to a pane (a user, 2026-11-17).
- Each pane has at most 500 entries. The app removes a gallery that nobody opened for 30 days automatically (a user, 2026-11-17).

## 9. Differences between the build and the design

- "30 days without an open" counts from `used` in `index.json`. The app updates `used` when the user opens the terminal of the pane or opens the gallery. The cleanup runs when this run of the app creates its first gallery.
- The load of an original shows only a spinner and no percentage. `Download.fetch` has no progress callback.
- The name that the app shows for a Kitty image is `image-<first 8 characters of the hash>.png`. At share time, the app copies the image into a temporary file with this name. It removes the file afterward.
- The terminal keyboard hides when the gallery opens. It does not return when the gallery closes. This keeps the keyboard from covering the gallery.
- The build adds `NSPhotoLibraryAddUsageDescription`. Without it, the share sheet has no "Save Image".
- The scanner runs on one shared serial queue (`dev.gorex.gallery`, qos utility). The main thread posts to it once for each output that it receives.
- The shaders of `DESIGN.md` are for the desktop only. The gallery uses system SwiftUI and glass controls, the same as other iOS pages.
- Two views may show the same pane at the same time (iPad multi-window). The two output streams then share one scanner, and their states interrupt each other. The scanner may miss one image. This case is not common. The design does not handle it now.
- The 256 MiB of section 4 counts only Kitty PNG files. The thumbnails of path images count separately. They are at most 500 JPEG files of 512 px.
- The rule in section 3.3, "same image number within 2 seconds replaces", lives only in memory. It restarts from zero after the app restarts. A replaced entry gets a new id and a new time, and moves to the end.
- The hidden-text state (SGR 8) holds across lines until SGR 0, SGR 28 or an empty parameter.
