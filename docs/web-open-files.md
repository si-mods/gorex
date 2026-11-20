# Opening a host's files from the web and iOS clients

Status: built (#410, #599, #721, #729, #732).

The gateway, the server, the web client and the iOS client all have their halves of this design. The code is in `internal/rexapi/download.go`, `host.go`, `internal/rex` (`openToSend`, `Client.GetFile`), `web/src/files.ts` and `web/src/viewer.ts`. The iOS client is in `ios/GoRex/Terminal/FileLink.swift` and `TerminalView+Open.swift`, as "The iOS client" says. Where the build differs from this text, see "As built" at the end.

## Problem

The desktop opens a file path that a pane prints with Command+click (Control elsewhere). In a pane of a host, the desktop first downloads the file over the peer protocol into the Downloads folder. Then it opens the file with the system. An image opens in the viewer of the system. Any other file opens in its app (`upload.go` `download`, `find.go` `openLink`). The web client opens only URLs (docs/web-client.md, Links). The Rex API gateway gives a browser no way to read a file of a host.

### What the code did before this design

- The GoRex server sends a file on a `file.get` connection (`internal/rex/transfer.go` `fileGet`, `proto.go` `FileGet`).
  - It sends a regular file only. It refuses a folder with `isFolder`, which `rex.Folder` tells.
  - The path is absolute, or from `~/`, or relative to the directory of the program in the foreground of the session. Otherwise the path is relative to the starting directory of the session (`session.go`).
  - The server answers the size, the name and the modification time. Then it sends the bytes from an offset. It emits `transfer` events, which the desktop shows.
  - The client side is `Client.openFile`, which `DownloadFile` uses. The server has `CapabilityFileTransfer`.
  - Errors other than a folder arrive as text only.
- `fileGet` opened the path before it checked that the path is a regular file (`transfer.go`: `os.Open`, then `Stat`). A FIFO blocks the open, and a device may act on it.
- The gateway takes files the other way: `POST /api/upload?block_id=…&name=…` (`internal/rexapi/upload.go`).
  - The welcome lists it as `gorex.upload` when the server has `CapabilityFileTemp`.
  - With it, the welcome lists `gorex.upload.deflate-raw`. A body of `Content-Encoding: deflate-raw` inflates as it streams, to exactly the `size` of the query. The file limit holds on this size.
  - The gateway reads a directory (`list_dir`, `blocks.go`). It never reads a file.
  - The gateway does not log uploads. Tracing records request URLs (`trace.go`).
- The order of the checks is this:
  1. The transport of the listener admits a request first (`HandlerFor`, `origin.go`). The local network uses the allowed networks (`lan.admit`). The tailnet uses the identity (`tailnet.identify`).
  2. The origin check comes next (`checkOrigin`, `web.go`). No `Origin` passes. A trusted `Origin` passes with CORS headers. Any other `Origin` gets 403.
  3. The access rule comes last (`http.go`).
  - The preflight answer allows only the `Content-Type` header (`web.go` `preflight`). The gateway does not check `Host`.
- The web client finds links as the desktop does (`web/src/links.ts`). It already ports `pathAt`, the finder of printed file paths of the desktop, for web addresses without a scheme. It does not treat a path, or a `file://` hyperlink, as a link.

## Goal

This design adds a way to read a file of a host from a browser. With it, the browser gets the behavior of the desktop. A file path is a link. An image opens in a viewer on the page. Any other file downloads.

## Design

### The gateway

#### A ticket from the control connection

```
→ {"method": "com.superlogical.terminal.download_begin",
   "params": {"block_id": "<block>", "path": "<path>"}}
← {"ticket": "<base64url>", "name": "report.png", "size": 48213,
   "mtime": "2026-11-12T09:30:00Z", "type": "image/png"}
```

- The page asks for a ticket on its control WebSocket. The origin check has passed this WebSocket (a browser always sends `Origin` on a WebSocket). The method opens the file with `file.get` for the session of the block. It fails early with a clear error (no file, a folder, not a regular file, denied). It keeps the open connection for the download.
- `path` is the path as the pane printed it. The client trims `:line:col` and turns a `file://` URL into its path, as `download` does. The gateway passes the path to `file.get` unchanged. The server resolves it: absolute, `~/`, or relative to the directory of the foreground program, else to the directory of the session.
  - The desktop first makes a relative path absolute with the directory of the pane (`hostPath`). The result is the same, unless the foreground program changes directory in between.
- A ticket is random (as the data tickets, `issueTicket`). It names one file of one block. It is good for one download. It lapses after 60 s or when the control connection closes. A lapsed ticket closes its `file.get`. At most 4 tickets are open for each control connection, as uploads are bounded for each block.
- `type` is the guess of the gateway from the extension of the name. The page uses it to choose between the viewer and a download. The page does not trust it for what it shows (see the web client).
- The welcome lists the feature `gorex.download` when the server has `CapabilityFileTransfer`.

#### GET /api/download?ticket=…

- The request streams the file of a valid ticket. The answer is `200`. It has `Content-Type: application/octet-stream` always, `Content-Length`, `Last-Modified`, `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`. It has `Content-Disposition: attachment` with an ASCII `filename=` and a `filename*=UTF-8''…`. `mime.FormatMediaType` makes them. The gateway never serves a file inline, so a file never runs as a page of the origin of the gateway.
- A trusted origin gets `Access-Control-Allow-Origin` and `Access-Control-Expose-Headers: Content-Disposition, Content-Length`. The request needs no custom header, so it needs no preflight.
- A request without `Origin` is a navigation or a native client. The gateway serves it. A ticket can only come from the control connection, so a page that rebinds a name has no ticket to use.
- If a file ends short of its size while the gateway sends it, the gateway aborts the response (`panic(http.ErrAbortHandler)`). A browser then never takes a cut-off file for the whole file.
- An unknown, used or lapsed ticket answers `404`. One file is at most `maxDownloadFileBytes`, 1 GiB, which is the limit of uploads. The gateway checks this when it issues the ticket. A test replaces the variable.
- The first version does not take `Range`.

#### Errors of download_begin

`file.get` reports errors as text. The gateway maps them with errors that the server can name exactly, because it runs on the same machine:

| Cause | Error code |
|---|---|
| no block, or the block's session ended | `not_found` |
| `rex.Folder` | `is_folder` |
| `is not a regular file` | `not_regular` |
| `syscall.ENOENT`'s text | `no_file` |
| `syscall.EACCES`'s, `ERROR_ACCESS_DENIED`'s text | `denied` |
| `links outside its folder` (`rex.LinksOutside`, #732) | `denied` |
| past `maxDownloadFileBytes` | `too_large` |
| no `CapabilityFileTransfer` | `unsupported` |
| anything else | `internal`, with the text |

A later revision may give `FileAnswer` a stable error code. The text match stays for older servers.

#### Host names

This is defence in depth against rebinding. The gateway answers a request whose `Host` is not one of its own names. Its own names are `localhost`, an IP literal, the own address of the listener, the tailnet name, and a name that the user adds with `gorex rexapi web host NAME`. Any other `Host` gets 421. This covers every path, not only downloads. So `/api/spaces` and the like also stop answering rebound pages. Whether a deployed name in front of a gateway (a reverse proxy) needs the new command is an open question.

#### The server: open regular files only

`fileGet` stats the path first. It refuses what is not a regular file before it opens it. On Unix, it opens with `O_NONBLOCK` and checks the open file again with `Fstat`. So the server refuses a FIFO or a device that someone swaps in between, and never waits on it. This is a fix of the server. It is useful to the desktop too.

#### Logging

The gateway logs each download as `rexapi: download <client address> <block> <path> <bytes>`. It logs each refusal of `download_begin` with its code. The gateway does not log uploads today. This design does not change that.

### The web client

- On a host whose welcome lists `gorex.download`, a file path is a link. The ported `pathAt` finds it. An OSC 8 hyperlink to `file://host/path` is also a link (as `ls --hyperlink` prints). The client ignores its host and percent-decodes its path, as `download` of the desktop reads `u.Path`.
  - The desktop does not stat the file in a pane of a host (`isLink`). The web client does the same. Any path that is not a folder (no trailing `/`) is a link. A missing file answers `no_file` when the user opens it.
  - A web address without a scheme stays a web link.
  - A Windows path written with backslashes (`C:\dir\x`) is not a link, as on the desktop. `file:///C:/dir/x` is a link.
- The path of another machine is not a link, printed or in a `file://` URL. This covers a UNC path (`//server/share`, `\\server\share`), a path of a device (`\\.\`, `\\?\`) and an NT path (`\??\`), with either slash (`links.ts` `remotePath`). A Windows host would look such a path up over SMB. It would then hand the NTLM hash of the user to the server that a program named.
- An OSC 8 `file://` hyperlink asks first when the cells of the link print other text. The text can be the path, its URL or the last parts of the path. The link asks first if the text is none of these. `ls --hyperlink` prints a name over its absolute path. A program can print `chart.png` over `file:///home/u/.ssh/id_ed25519`. A `confirm` names the path when the click ends. Cancel opens nothing.
- The client cleans the path of a `file://` URL as the `filepath.Clean` of the host does (`cleanPath`). The clean path is what the page shows and asks for. `%2F` decodes after the client resolves the own dot segments of the URL. So `a.png%2F..%2F.ssh%2Fid` still climbs, and a long run of them could push the real file out of the dialog.
  - A path that cleaning changes asks first, printed or not.
  - Printed text with a `.` or `..` segment shows no part of the path.
  - A decoded backslash is not a link, because it is a separator to a Windows host.
- Any path with a `..` segment, printed or in a `file://` URL, asks first and shows its clean path (`climbs`). A program can print `/tmp/chart.png` and, unseen right after it, `/../../home/u/.ssh/id`. A `..` after the `C:` of a drive counts, because cleaning keeps it (`C:../x`).
- Ctrl+click (Cmd+click on macOS) on a path asks for a ticket with `download_begin`. A second click on the same path while one is under way does nothing, as on the desktop.
  - When `type` names an image (`png`, `jpg`, `jpeg`, `gif`, `webp`, `avif`, `bmp`, `ico`, `svg`) and `size` is at most 50 MiB, the page requests `/api/download?ticket=…`. It checks the first bytes against the signature of the image. It builds a blob of that image type only for the viewer. The page shows nothing else.
  - The page navigates a hidden link to `/api/download?ticket=…` for any other file, a larger image, or an image whose bytes do not match. The browser downloads the file itself, with its own progress and cancel. It writes the file to disk as the bytes arrive. So no size limit of the memory of the page applies. No blob of the data of the host ever exists on the origin of the page.
  - An error shows in the status line: `No such file`, `A folder is not downloaded`, `Not a regular file`, `Permission denied`, `Too large`.
- The viewer is a dialog over the window, as the Settings dialog is. The page is dimmed and frosted behind it, as the `paintFrost` and `dimmed` rules of DESIGN.md translate to CSS.
  - The viewer first shows the image fitted to the window. It shows the name and the size of the image. It has buttons with lucide icons: zoom in (`zoom-in`), zoom out (`zoom-out`), actual size (`scan`), download (`download`) and close (`x`). The design adds `zoom-in`, `zoom-out`, `scan` and `download` to `assets/icons` from lucide.
  - The download button of the viewer asks for a new ticket and navigates, as above. The page never navigates to a blob.
  - Ctrl+wheel and the pinch of a trackpad (which arrives as Ctrl+wheel) zoom. The listener is not passive, so the browser does not zoom the page.
  - A drag pans when the image is larger than the window.
  - Escape or a click outside closes the viewer, and the keyboard returns to the pane.
  - On a phone, two fingers zoom.
  - The viewer does not show an image larger than 100 megapixels once decoded. It offers the download. The viewer revokes the blob URL when it closes.
- The page itself gets a Content-Security-Policy where it is hosted (`web/_headers` already sets one). `blob:` is allowed for images only.
- A desktop that is connected to the same host sees the downloads of the web client in its transfer list, because `file.get` emits `transfer` events.

### Differences from the desktop

- The desktop resumes a download and keeps the file in the Downloads folder, then opens it. The web client hands the file to the download of the browser. It opens only images itself.
- The desktop shows transfers in a list at the lower right of the pane. The list stays until the user clicks it. The web client shows only errors, in the status line. It leaves progress to the browser.
- Local panes do not exist in the web client, so there is no "open the file in place".

### The iOS client

An agent such as Claude Code shows the user an image that it made only as a printed path. Rex Go draws inline images (Kitty graphics, which the server rewrites from file to in-band, `internal/rex/graphics.go`). A program that draws its images, as omp does, already shows them. A printed path opened nothing. A tap opened only web and mail links (`Terminal/WebLink.swift`, `Ghostty.swift` `open`). Rex Go already has the client half of the download (`Rex/Download.swift`, `Host.download`, Rex API revision 13 `download_begin` with a `rex.data.bytes.v1` stream). This design adds its caller.

- On a host whose control connection is revision 13 or later (`Host.canDownload`), a path is a link by the rules of the web client above. The finder of printed paths is the same, ported to Swift. An OSC 8 `file://host/path` link is also a link. The client ignores its host and percent-decodes its path. A web address without a scheme stays a web link. On an older host, a path is plain text, as before.
- A tap opens a link, as a tap opens a web link now. A touch screen needs no modifier. When a program tracks the mouse, the tap goes to the program, as now. A tap on text that is not a link shows the keyboard, as now. A second tap on the same path while it downloads does nothing.
- The tap downloads the file with `Host.download` for the block and the session of the tapped pane. A relative path then resolves on the server against the directory of the program in the foreground. The bytes go to a file under the temporary folder of the app. The file has the name that the server gives it. The app empties the folder when it starts.
- The file opens in QuickLook (`QLPreviewController`), over the terminal. This covers an image, a PDF, text and anything else that QuickLook reads. QuickLook gives zoom, pan and its share button. The share button saves the image to Photos or sends any file to another app. When the user closes QuickLook, the keyboard returns to the pane as it was.
- After 0.3 s without the file, the pane shows that it downloads. A tap outside the progress cancels the download.
- An error shows as an alert with the words of the web client: `No such file`, `A folder is not downloaded`, `Not a regular file`, `Permission denied`, `Too large`. The words go in `Localizable.xcstrings` in every locale of the app.

Differences from the web client:

- QuickLook opens every file type that it reads, not only images. There is no check of the bytes against the image type, because the bytes never reach the origin of a web page.
- The app holds the file in memory once (`Download.fetch` returns `Data`). So the limit of the app, 256 MiB (`OpenedFiles.maxBytes`), bounds it. This limit is below the `maxDownloadFileBytes` of the server. The web client streams a large file to the download of the browser instead.

As built on iOS:

- Ghostty finds the links itself. Its default link pattern matches a path with a slash (`./`, `../`, `~/`, `/abs`, `dir/x.ext`) and hands over the text as printed. `FileLink` then applies the rules of the web client to it. A place in the file, the `--name=` of an option, and closing punctuation and brackets go. A folder, a backslash and a `:` are not a file. So a bare name such as `notes.md` and a quoted path with a space are not links.
  - A `(12,3)` place goes with the others.
  - Prose in full-width punctuation after a path (`/tmp/x.png，请查看`) is cut off, as the web client does.
  - A path that starts with `$` is not a link either, because the host does not expand variables.
  - The path of another machine is not a file, printed or in a `file://` URL, by the `remotePath` of the web client (`FileLink.remote`).
- Ghostty hands over the URL of an OSC 8 link without the text that it prints. So a tap on any OSC 8 `file://` link asks first. An alert names the host and the full path, with Cancel and Open.
  - A path or `file://` URL that the terminal prints as text opens with one tap. It asks first in two cases. In the first case, the text that Ghostty matched goes on past the word of the path (a path printed unseen, then a name). In the second case, cleaning changes the path of the URL (`FileLink.clean`, as the `cleanPath` of the web client). The app shows and downloads the clean path.
  - A path with a `..` segment, printed or not, asks too (`FileLink.climbs`).
- On an older host, a tap on a path does nothing, so `Server does not accept downloads` never shows. The `not_supported` of the server shows the `The host does not send files` of the web client.
- The app holds a file in memory once, up to the 256 MiB of the web client (`maxBlobBytes`). A larger file answers `Too large` before the app reads its bytes. The server keeps the ticket of such a file until it lapses (60 s). The ticket counts toward the 4 open tickets of the connection.
- When the user cancels a download (a tap beside the progress), the app closes its data stream. The app drops the bytes that it read so far.
- The app deletes each file when QuickLook closes, and when the app starts.
- QuickLook on iOS 26 shows an image without its bar. A tap shows its close and share buttons.

## Alternatives

**A plain GET with a header.** A first draft read the file with `GET /api/download?block_id=…&path=…` and a custom header. It relied on the preflight that a custom header causes. That is not safe. A page that rebinds its own name to the address of the gateway (DNS rebinding) is same-origin with the gateway. Its GET carries no `Origin`, no preflight and any header it likes. On a plain-http listener it also carries no Fetch Metadata, because browsers send `Sec-Fetch-*` only to secure contexts. Every check that the gateway has would let it through. The WebSocket and the POST of the upload are safe today only because a browser sends `Origin` with both, even same-origin.

## Tests

iOS (`ios/GoRexTests`, XCTest):

- The Swift finder of printed paths gives the web client's answers. The lines cover these cases:
  - an absolute path, `~/` and a relative path
  - a line and column suffix, brackets and quotes
  - a folder, a web address without a scheme, and a Windows path
- `file://` links decode to their path.
- A web link still opens as before.
- On the simulator, against a server with a made-up PNG: a tap on its printed path opens QuickLook with the image. A tap on a missing file shows `No such file`.

Gateway (Go, `internal/rexapi`):

- `download_begin` issues a ticket for a file. Relative, `~/` and absolute paths resolve as `file.get` does.
- A folder, a FIFO, a missing file and a denied file answer `is_folder`, `not_regular`, `no_file` and `denied`. The denied case is skipped when the test runs as root. Anything else maps to `internal`. Past a test limit, the answer is `too_large`.
- `GET /api/download` streams the bytes once with the headers above. These include `Cache-Control: no-store`, `Content-Type: application/octet-stream` and the exposed headers for a trusted origin.
- A second use, a lapsed ticket and a ticket of a closed control connection answer `404` and close the `file.get`.
- A file cut short while it is sent aborts the response.
- A request with a `Host` that is not the gateway's gets 421, on `/api/download` and on `/api/spaces`. An IP literal, `localhost` and the tailnet name pass.
- The welcome lists `gorex.download` only with `CapabilityFileTransfer`.

Server (`internal/rex`): `fileGet` refuses a FIFO and a device without blocking.

Web client (ego lite against a trial gateway):

- Ctrl over a path and over an OSC 8 `file://` hyperlink underlines it only on a host with `gorex.download`.
- Ctrl+click on a PNG opens the viewer with the right image. Zoom, pan, pinch, Escape and focus return work. The download of the viewer navigates.
- A text file and an image whose bytes do not match download with their names, with no blob created.
- A missing file and a folder show their errors. A double click asks for one ticket.
- Ctrl+click under mouse tracking opens the file and reports nothing.

## Open questions

- Does the viewer also open PDFs and plain text? A PDF would use the viewer of the browser in an `<iframe>` of the blob, which would need care with its type. Not in the first version.
- Does an image that the terminal draws inline (kitty graphics, sixel) open in the same viewer on a click? That needs no download. It is a separate change.
- Does the `Host` check need names for a gateway behind a proxy? Does it ship with this change, or on its own first, as it touches every path?

## As built

- `download_begin` is a block method, so its parameters follow the parameters of `upload_begin`: `{"session_id", "block_id", "args": {"path"}}`, not `{"block_id", "path"}`.
- A server without `CapabilityFileTransfer` answers `not_supported`, the Rex code that `upload_begin` also uses, not `unsupported`.
- A fifth ticket on one control connection answers `conflict`. `bad_request` covers a missing path or an unknown argument.
- The status of each code: `is_folder` and `not_regular` 409, `no_file` 404, `denied` 403, `too_large` 413.
- `mtime` is RFC 3339 in UTC, to the second. The gateway leaves it out when the server sends none. `type` takes a fixed table first (the images that the web client shows, PDF, text, JSON, HTML). Then it takes the table of the system. In this way `type` is the same on every system.
- A ticket that someone used is no longer the ticket of the control connection. If the connection closes, a download under way does not stop.
- The `Host` check runs on the local network, the tailnet and `--listen`, before the own check of the transport.
  - The local socket and tailcat are not checked. No browser reaches either, and Rex Go names a tailcat host `<id>.tailcat.invalid`.
  - The own address of the listener needs no rule of its own, because every listener is bound to an IP address.
  - The names of the built-in Tailscale are its MagicDNS name and the first label of that name.
  - The command is `gorex rexapi web host add NAME` / `host remove NAME`, and `web status` lists the names.
- On Windows, the server refuses a `\\.\`, `\\?\pipe\` or `\\?\GLOBALROOT` path before it looks. Then it stats, opens and stats the open file again. The tests cover the denied case on Unix only.
