# Opening a host's files from the web client

Status: revised after review. The gateway's and the server's half is
built (`internal/rexapi/download.go`, `host.go`, `internal/rex`
`openToSend`, `Client.GetFile`); the web client's is not yet. Where the
build differs from this text, see "As built" at the end.

The desktop opens a file path printed in a pane with Command+click
(Control elsewhere). In a pane of a host it first downloads the file over
the peer protocol into the Downloads folder, then opens it with the
system: an image in the system's viewer, any other file in its app
(`upload.go` `download`, `find.go` `openLink`). The web client opens only
URLs (docs/web-client.md, Links): the Rex API gateway gives a browser no way
to read a host's file.

This design adds that way, and with it the desktop's behaviour in the
browser: a file path is a link, an image opens in a viewer on the page, and
any other file downloads.

## What there is now

- The GoRex server sends a file on a `file.get` connection
  (`internal/rex/transfer.go` `fileGet`, `proto.go` `FileGet`): a regular
  file only (a folder is refused with `isFolder`, which `rex.Folder`
  tells), its path absolute, from `~/`, or relative to the directory of the
  session's program in the foreground, else the session's starting
  directory (`session.go`). It answers the size, the name and the
  modification time, then the bytes from an offset, and emits `transfer`
  events, which the desktop shows. The client side is `Client.openFile`,
  which `DownloadFile` uses; the server has `CapabilityFileTransfer`.
  Errors other than a folder arrive as text only.
- `fileGet` opens the path before it checks that it is a regular file
  (`transfer.go`: `os.Open`, then `Stat`), so a FIFO blocks the open and a
  device may act on it.
- The gateway takes files the other way: `POST /api/upload?block_id=…&name=…`
  (`internal/rexapi/upload.go`), listed in the welcome as `gorex.upload`
  when the server has `CapabilityFileTemp`. It reads a directory
  (`list_dir`, `blocks.go`), never a file. Uploads are not logged; tracing
  records request URLs (`trace.go`).
- Order of the checks: the listener's transport admits a request first
  (`HandlerFor`, `origin.go`: the local network's allowed networks,
  `lan.admit`; the tailnet's identity, `tailnet.identify`), then the origin
  (`checkOrigin`, `web.go`: no `Origin` passes, a trusted one passes with
  CORS headers, any other gets 403), then the access rule (`http.go`). The
  preflight answer allows only the `Content-Type` header (`web.go`
  `preflight`). The gateway does not check `Host`.
- The web client finds links as the desktop does (`web/src/links.ts`), and
  already ports `pathAt`, the desktop's finder of printed file paths, for
  web addresses without a scheme. It does not treat a path, or a `file://`
  hyperlink, as a link.

## Why not a plain GET with a header

A first draft read the file with `GET /api/download?block_id=…&path=…` and
a custom header, relying on the preflight that a custom header causes. That
is not safe. A page that rebinds its own name to the gateway's address (DNS
rebinding) is same-origin with the gateway: its GET carries no `Origin`, no
preflight, any header it likes, and on a plain-http listener no Fetch
Metadata, as browsers send `Sec-Fetch-*` only to secure contexts. Every
check the gateway has would let it through. The WebSocket and the upload's
POST are safe today only because a browser sends `Origin` with both, even
same-origin.

## The gateway

### A ticket from the control connection

```
→ {"method": "com.superlogical.terminal.download_begin",
   "params": {"block_id": "<block>", "path": "<path>"}}
← {"ticket": "<base64url>", "name": "report.png", "size": 48213,
   "mtime": "2026-11-12T09:30:00Z", "type": "image/png"}
```

- The page asks for a ticket on its control WebSocket, which the origin
  check has passed (a browser always sends `Origin` on a WebSocket). The
  method opens the file with `file.get` for the block's session, so it
  fails early with a clear error (no file, a folder, not a regular file,
  denied), and keeps the open connection for the download.
- `path` is the path as the pane printed it, `:line:col` trimmed and a
  `file://` URL turned into its path by the client, as `download` does. The
  gateway passes it to `file.get` unchanged, so the server resolves it:
  absolute, `~/`, or relative to the foreground program's directory, else
  the session's. The desktop makes a relative path absolute first with the
  pane's directory (`hostPath`); the result is the same unless the
  foreground program changes directory in between.
- A ticket is random (as the data tickets, `issueTicket`), names one
  file of one block, is good for one download, and lapses after 60 s or
  when the control connection closes; a lapsed ticket closes its
  `file.get`. At most 4 tickets are open per control connection, as
  uploads are bounded per block.
- `type` is the gateway's guess from the name's extension, for the page
  to choose between the viewer and a download; the page does not trust it
  for what it shows (see the web client).
- The welcome lists the feature `gorex.download` when the server has
  `CapabilityFileTransfer`.

### GET /api/download?ticket=…

- Streams the file of a valid ticket: `200`, `Content-Type:
  application/octet-stream` always, `Content-Length`, `Last-Modified`,
  `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, and
  `Content-Disposition: attachment` with an ASCII `filename=` and an
  `filename*=UTF-8''…`, made with `mime.FormatMediaType`. A file is never
  served inline, so it never runs as a page of the gateway's origin.
- A trusted origin gets `Access-Control-Allow-Origin` and
  `Access-Control-Expose-Headers: Content-Disposition, Content-Length`. The
  request needs no custom header, so no preflight.
- A request without `Origin` is a navigation or a native client. It is
  served: a ticket can only come from the control connection, so a page
  that rebinds a name has none to use.
- A file that ends short of its size while it is sent aborts the response
  (`panic(http.ErrAbortHandler)`), so a browser never takes a cut-off file
  for the whole one.
- An unknown, used or lapsed ticket answers `404`. One file is at most
  `maxDownloadFileBytes`, 1 GiB, the upload's limit, checked when the
  ticket is issued; a test replaces the variable.
- `Range` is not taken in the first version.

### Errors of download_begin

`file.get` reports errors as text. The gateway maps them with errors the
server can name exactly, as it runs on the same machine:

| Cause | Error code |
|---|---|
| no block, or the block's session ended | `not_found` |
| `rex.Folder` | `is_folder` |
| `is not a regular file` | `not_regular` |
| `syscall.ENOENT`'s text | `no_file` |
| `syscall.EACCES`'s, `ERROR_ACCESS_DENIED`'s text | `denied` |
| past `maxDownloadFileBytes` | `too_large` |
| no `CapabilityFileTransfer` | `unsupported` |
| anything else | `internal`, with the text |

A later revision may give `FileAnswer` a stable error code; the text match
stays for older servers.

### Host names

As defence in depth against rebinding, the gateway answers a request whose
`Host` is not one of its own: `localhost`, an IP literal, the listener's
own address, the tailnet name, or a name added with `gorex rexapi web host
NAME`. Any other `Host` gets 421. This covers every path, not only
downloads, so `/api/spaces` and the like stop answering rebound pages too.
Whether a deployed name in front of a gateway (a reverse proxy) needs the
new command is an open question.

### The server: open regular files only

`fileGet` stats the path first and refuses what is not a regular file
before it opens it; on Unix it opens with `O_NONBLOCK` and checks the
open file again with `Fstat`, so a FIFO or a device swapped in between is
refused, never waited on. This is a fix of the server, useful to the
desktop too.

### Logging

The gateway logs each download, as `rexapi: download <client address>
<block> <path> <bytes>`, and each refusal of `download_begin` with its
code. Uploads are not logged today; this does not change them.

## The web client

- On a host whose welcome lists `gorex.download`, a file path is a link,
  found with the ported `pathAt`, and so is an OSC 8 hyperlink to
  `file://host/path` (as `ls --hyperlink` prints), its host ignored and its
  path percent-decoded, as the desktop's `download` reads `u.Path`. As the
  desktop does in a host's pane (`isLink`: no stat on the host), any path
  that is not a folder (no trailing `/`) is a link; a missing one answers
  `no_file` when it is opened. A web address without a scheme stays a web
  link. A Windows path written with backslashes (`C:\dir\x`) is not a link,
  as on the desktop; `file:///C:/dir/x` is.
- Ctrl+click (Cmd+click on macOS) on a path asks for a ticket with
  `download_begin`. A second click on the same path while one is under way
  does nothing, as the desktop's.
  - When `type` names an image (`png`, `jpg`, `jpeg`, `gif`, `webp`,
    `avif`, `bmp`, `ico`, `svg`) and `size` is at most 50 MiB, the page
    fetches `/api/download?ticket=…`, checks the first bytes against the
    image's signature, and builds a blob of that image type only for the
    viewer. Anything else is not shown.
  - Any other file, a larger image, or one whose bytes do not match:
    the page navigates a hidden link to `/api/download?ticket=…`. The
    browser downloads it itself, with its own progress and cancel, written
    to disk as it arrives, so no size limit of the page's memory applies
    and no blob of the host's data ever exists on the page's origin.
  - An error shows in the status line: `No such file`, `A folder is not
    downloaded`, `Not a regular file`, `Permission denied`, `Too large`.
- The viewer is a dialog over the window, as the Settings dialog: the
  page dimmed and frosted behind it as DESIGN.md's `paintFrost` and
  `dimmed` rules translate to CSS. It shows the image fitted to the window
  at first, its name and size, and buttons with lucide icons: zoom in
  (`zoom-in`), zoom out (`zoom-out`), actual size (`scan`), download
  (`download`), close (`x`); `zoom-in`, `zoom-out`, `scan` and `download`
  are added to `assets/icons` from lucide. Its download button asks for a
  new ticket and navigates, as above; the page never navigates to a blob.
  Ctrl+wheel and a trackpad's pinch (which arrives as Ctrl+wheel) zoom,
  with a listener that is not passive, so the browser does not zoom the
  page; a drag pans when the image is larger than the window; Escape or a
  click outside closes, and the keyboard returns to the pane. On a phone,
  two fingers zoom. An image larger than 100 megapixels once decoded is
  not shown; the viewer offers the download. The blob URL is revoked when
  the viewer closes.
- The page itself gets a Content-Security-Policy where it is hosted
  (`web/_headers` already sets one); `blob:` is allowed for images only.
- A desktop connected to the same host sees the web client's downloads
  in its transfer list, as `file.get` emits `transfer` events.

## Differences from the desktop

- The desktop resumes a download and keeps the file in the Downloads
  folder, then opens it; the web client hands the file to the browser's
  download, and opens only images itself.
- The desktop shows transfers in a list at the pane's lower right, kept
  until clicked; the web client shows only errors, in the status line,
  and leaves progress to the browser.
- Local panes do not exist in the web client, so there is no "open the
  file in place".

## Tests

Gateway (Go, `internal/rexapi`):

- `download_begin` issues a ticket for a file; relative, `~/` and absolute
  paths resolve as `file.get` does; a folder, a FIFO, a missing file and a
  denied file answer `is_folder`, `not_regular`, `no_file` and `denied`
  (the denied case skipped when the test runs as root); anything else maps
  to `internal`; past a test limit, `too_large`.
- `GET /api/download` streams the bytes once with the headers above,
  including `Cache-Control: no-store`, `Content-Type:
  application/octet-stream` and the exposed headers for a trusted origin;
  a second use, a lapsed ticket and a ticket of a closed control
  connection answer `404` and close the `file.get`.
- A file cut short while it is sent aborts the response.
- A request with a `Host` not the gateway's gets 421, on `/api/download`
  and on `/api/spaces`; an IP literal, `localhost` and the tailnet name
  pass.
- The welcome lists `gorex.download` only with `CapabilityFileTransfer`.

Server (`internal/rex`): `fileGet` refuses a FIFO and a device without
blocking.

Web client (ego lite against a trial gateway):

- Ctrl over a path and over an OSC 8 `file://` hyperlink underlines it
  only on a host with `gorex.download`.
- Ctrl+click on a PNG opens the viewer with the right image; zoom, pan,
  pinch, Escape and focus return; the viewer's download navigates.
- A text file and an image whose bytes do not match download with their
  names, with no blob created.
- A missing file and a folder show their errors; a double click asks for
  one ticket.
- Ctrl+click under mouse tracking opens the file and reports nothing.

## Open questions

- Whether the viewer also opens PDFs (the browser's own viewer in an
  `<iframe>` of the blob, which would need care with its type) and plain
  text. Not in the first version.
- Whether an image the terminal draws inline (kitty graphics, sixel)
  opens in the same viewer on a click. That needs no download; it is a
  separate change.
- Whether the `Host` check needs names for a gateway behind a proxy, and
  whether it ships with this change or on its own first, as it touches
  every path.

## As built

- `download_begin` is a block method, so its parameters follow
  `upload_begin`'s: `{"session_id", "block_id", "args": {"path"}}`, not
  `{"block_id", "path"}`.
- A server without `CapabilityFileTransfer` answers `not_supported`,
  Rex's code that `upload_begin` uses too, not `unsupported`.
- A fifth ticket on one control connection answers `conflict`.
  `bad_request` covers a missing path or an unknown argument.
- The status of each code: `is_folder` and `not_regular` 409, `no_file`
  404, `denied` 403, `too_large` 413.
- `mtime` is RFC 3339 in UTC, to the second, left out when the server
  sends none. `type` takes a fixed table first (the images the web
  client shows, PDF, text, JSON, HTML), then the system's, so that it
  is the same on every system.
- A ticket that has been used is no longer the control connection's:
  closing the connection does not stop a download under way.
- The `Host` check runs on the local network, the tailnet and
  `--listen`, before the transport's own check. The local socket and
  tailcat are not checked: no browser reaches either, and Rex Go names a
  tailcat host `<id>.tailcat.invalid`. The listener's own address needs
  no rule of its own, as every listener is bound to an IP address.
  Built-in Tailscale's names are its MagicDNS name and that name's first
  label. The command is `gorex rexapi web host add NAME` / `host remove
  NAME`, and `web status` lists the names.
- On Windows, the server refuses a `\\.\`, `\\?\pipe\` or
  `\\?\GLOBALROOT` path before it looks, then stats, opens and stats
  the open file again. The denied case is tested on Unix only.
