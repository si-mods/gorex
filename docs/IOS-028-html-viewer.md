---
name: IOS-028
description: "Rex Go opens tapped HTML files in an in-app WKWebView and fetches relative assets from the host on demand. Use when you change HTML file viewing or file scheme loading on iOS."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-028: Rex Go iOS: in-app HTML viewer

Status: accepted. Not implemented.

## Problem

When a user taps an HTML file path in a terminal on iOS, `TerminalView.openFile` downloads the file and opens it in `QLPreviewController` (`ios/GoRex/Terminal/TerminalView+Open.swift`, [`WEB-001`](WEB-001-open-files.md)). Quick Look does not run interactive scripts well and cannot load sibling CSS, script, image or linked HTML files from the host.

Agents such as Claude Code and Codex often generate self-contained or multi-file HTML pages (comparison views, charts, reports) and print their path in the terminal. Opening those files on iPhone or iPad requires an in-app web view that runs the page and loads its relative assets from the same host.

## Goal

- Tapping a `.html` or `.htm` file path in a terminal opens an in-app `WKWebView` viewer instead of Quick Look.
- Relative assets and links inside the page (`./style.css`, `app.js`, `img.png`, `other.html`) load on demand from the host over the existing `com.superlogical.terminal.download_begin` transfer, with no server protocol change.
- Asset loading stays confined to the tapped HTML file's directory and its subdirectories.
- The viewer supports back navigation, reload from the host, sharing the downloaded HTML file, and closing with keyboard restoration.

## Non-goals

- Collecting a history list of printed HTML files across scrollback (as [`IOS-007`](IOS-007-pane-gallery.md) does for images).
- Matching bare filenames without a slash (such as `index.html`). Link matching stays in `FileLink` and Ghostty.
- Opening arbitrary `http://` or `https://` terminal links inside the app; those stay in Safari (`WebLink`).
- Running a local HTTP server on the phone or changing the GoRex server protocol.

## Design

### Entry and routing

`TerminalView.openFile` downloads the tapped file as before. After `OpenedFiles.save` writes the file into its temporary folder, `TerminalView.preview` checks `HTMLResourcePath.isHTML(url.lastPathComponent)`.

- `.html` and `.htm` (case-insensitive) open `HTMLViewerController` in a full-screen `UINavigationController`.
- All other file types continue to open `OpenedFiles.Preview` (`QLPreviewController`).

### Custom scheme and relative asset loading

The viewer registers a `WKURLSchemeHandler` (`HTMLSchemeHandler`) on scheme `gorex-file`.

- `HTMLResourcePath` splits the tapped host path (for example `~/.gemini/brain/id/scratch/compare.html`) into its base directory (`~/.gemini/brain/id/scratch`) and entry filename (`compare.html`).
- The web view loads `gorex-file://local/<percent-encoded entryName>`. Using a synthetic `gorex-file://local` origin gives relative URLs, `fetch()`, `<script type="module">`, and sibling links same-origin behavior.
- The initial load of the entry URL serves the already-downloaded temporary file without a second network transfer.
- Tapping Reload clears the cached entry file, downloads the entry path from the host again, updates the temporary file (so Share exports the fresh file), and reloads the web view.
- Every other request to `gorex-file://local/<relative>` maps to `<baseDir>/<relative>` and downloads the file via `host.download(_:block:session:maxBytes:)`.
- Downloads run through `BoundedTasks(limit: 3)` so a page with many assets stays below the server's 4-ticket-per-connection limit (`conflict`).
- Each `WKURLSchemeTask` is tracked by `ObjectIdentifier`. When WebKit calls `webView(_:stop:)`, the handler cancels the in-flight download and does not send further callbacks to the stopped task.
- Missing files (`no_file`) answer HTTP 404 so the page renders without hanging.
- Responses set `Content-Type` from a UTF-8 web MIME table (falling back to `UTType`), `Content-Length`, and `Cache-Control: no-store`.

### Security and isolation

- The `WKWebView` uses `WKWebsiteDataStore.nonPersistent()` so cookies, `localStorage` and caches do not persist across views or leak between untrusted pages.
- `HTMLResourcePath.hostPath(for:)` accepts only scheme `gorex-file` and host `local`. It rejects empty paths, trailing slashes, backslashes, null bytes, and any `..` segment (raw or percent-decoded). A page in `/tmp/untrusted/` cannot climb out to read `~/.ssh/id_ed25519` or `/etc/passwd`.
- Tapped `http://`, `https://`, `mailto:` and `tel:` links (`navigationType == .linkActivated` or `target="_blank"`) open via `UIApplication.shared.open(_:)` instead of navigating inside the viewer.
- Script-initiated navigation to arbitrary app schemes (such as `rexgo://` or `file://`) is blocked.

### Interface

- Full-screen modal `UINavigationController` over the terminal.
- Navigation bar title shows the document's `<title>` when non-empty, otherwise the filename.
- Leading bar shows Close, and adds Back when `webView.canGoBack` is true.
- Trailing bar shows Reload and Share (`UIActivityViewController` on the local downloaded HTML file).
- A thin `UIProgressView` below the navigation bar tracks `webView.estimatedProgress` while loading.
- On dismiss, the viewer cancels all in-flight asset downloads, removes the temporary folder, and restores the terminal keyboard if it was visible before the tap.

## Alternatives

**`loadFileURL(_:allowingReadAccessTo:)`.** Simpler for single-file HTML, because WebKit loads the downloaded file directly from disk. It cannot fetch sibling CSS, JS, images or linked HTML files from the remote host without downloading the whole directory upfront. `WKURLSchemeHandler` fetches only what the page requests and needs no server changes.

**Local HTTP server on the phone.** Would avoid custom-scheme quirks, but opens a listening socket in the app and requires lifecycle management across backgrounding. `WKURLSchemeHandler` runs in-process with no socket.

## Tests

- `ios/GoRexTests/HTMLViewerTests.swift`:
  - `HTMLResourcePath.isHTML` accepts `.html` and `.htm` (any case) and rejects other extensions and directories.
  - `HTMLResourcePath` splits absolute, `~/`, relative, and root paths into base directory and entry URL.
  - `HTMLResourcePath.hostPath(for:)` resolves sibling, nested, percent-encoded, and query/fragment URLs within `baseDir`.
  - `HTMLResourcePath.hostPath(for:)` rejects `..` traversal (raw and percent-encoded), backslashes, wrong scheme or host, empty paths, and directory paths.
  - `HTMLResourcePath.mimeType(for:)` returns UTF-8 MIME types for web text formats and binary types for images and fonts.
  - `HTMLSchemeHandler` serves the cached entry file first, fetches relative assets through the loader, returns 404 on `no_file`, respects concurrency limits, and stops safely when cancelled.
- Simulator check: open an HTML file with inline JS and a relative CSS/JS asset printed in a terminal, verify it renders interactively in `WKWebView`, reload updates it, and closing returns to the terminal.
