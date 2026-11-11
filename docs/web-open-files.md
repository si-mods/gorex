# Opening a host's files from the web client

Status: proposed. Nothing here is built yet.

The desktop opens a file path printed in a pane with Command+click
(Control elsewhere). In a pane of a host it first downloads the file over
the peer protocol, then opens it with the system: an image in the system's
viewer, any other file in its app (`upload.go` `download`, `find.go`
`openLink`). The web client opens only URLs (docs/web-client.md, Links):
the Rex API gateway gives a browser no way to read a host's file.

This design adds that way, and with it the desktop's behaviour in the
browser: a file path is a link, an image opens in a viewer on the page, and
any other file downloads.

## What there is now

- The GoRex server sends a file on a `file.get` connection
  (`internal/rex/transfer.go` `fileGet`, `proto.go` `FileGet`): a regular
  file only (a folder is refused with `isFolder`), its path absolute, from
  `~/`, or relative to the directory of the session's program in the
  foreground. It answers the size, the name and the modification time,
  then the bytes from an offset. The client side is `Client.openFile`,
  which `DownloadFile` uses; the server has `CapabilityFileTransfer`.
- The gateway takes files the other way: `POST /api/upload?block_id=…&name=…`
  (`internal/rexapi/upload.go`), listed in the welcome as `gorex.upload`.
  It reads a directory (`list_dir`, `blocks.go`), never a file.
- The gateway answers a browser only from a trusted origin
  (`internal/rexapi/web.go`, docs/web-client.md, Trusting a page). A
  request without `Origin` passes that check, as native clients send none.
- The web client finds links as the desktop does (`web/src/links.ts`), and
  already ports `pathAt`, the desktop's finder of printed file paths, for
  web addresses without a scheme. It does not treat a path as a link.

## The gateway: GET /api/download

```
GET /api/download?block_id=<block>&path=<path>
X-GoRex-Download: 1
```

- `path` is the path as the pane printed it, `:line:col` trimmed by the
  client. The gateway does not resolve it: it passes it to `file.get` with
  the block's session, so it resolves exactly as the desktop's download
  does (absolute, `~/`, or relative to the foreground program's
  directory). A `file:///C:/dir/name` URL a Windows host prints is turned
  into a path by the client, as `download` does.
- The answer streams the file: `200`, `Content-Type` from the name's
  extension (`application/octet-stream` when unknown), `Content-Length`,
  `Last-Modified`, `Content-Disposition: attachment; filename*=UTF-8''<name>`,
  and `X-Content-Type-Options: nosniff`. It is never served inline, so a
  file is never a page of the gateway's origin.
- Errors are JSON, as the upload's (`writeError`): `404` for no block or no
  file, `409` for a folder (`rex.Folder`), `403` when the server user may
  not read it, `413` past the size limit, `400` when the server has no
  `CapabilityFileTransfer`.
- Limits: one file up to `maxDownloadFileBytes`, 1 GiB, the upload's
  limit. A client that goes away cancels the `file.get`.
- `Range` is not taken in the first version. The desktop resumes a
  download from a partial file; a browser download is in memory and starts
  again.
- The welcome lists the feature `gorex.download` when the server has
  `CapabilityFileTransfer`. The web client treats paths as links only on a
  host that lists it.

To stream without a temporary file, `internal/rex` exports the reader that
`DownloadFile` already opens (`openFile` with `FileGet`), as
`Client.GetFile(ctx, sid, path) (io.ReadCloser, FileAnswer, error)`.

### Who may download

The endpoint reads any file the GoRex server's user can read. A terminal
of that host can already `cat` the same files, so it gives nothing a
client of the gateway does not have; but it is a new way to read them, and
a web page must not reach it unless the host trusts the page.

- The request must carry `X-GoRex-Download: 1`. A browser sends a header
  of its own only after a preflight, which the gateway answers only for a
  trusted origin. Without the header the gateway answers `400`. This
  closes the gap that `Origin` alone leaves: a browser sends no `Origin`
  with a plain GET such as `<img src>` or a link, which the "no Origin"
  rule would let through.
- A request with `Sec-Fetch-Site: cross-site` or `same-site` and no
  trusted `Origin` is refused (`403`), as a second guard for browsers that
  send Fetch Metadata.
- After that, the access rules of the listener apply as for every other
  request: the local network's allowed networks, or the tailnet's.
- Native clients (no `Origin`, no Fetch Metadata) send the header and are
  served as before. The gateway logs each download with the client's
  address and the path, as it logs uploads.

## The web client

- On a host whose welcome lists `gorex.download`, a file path is a link,
  found with the ported `pathAt`. As the desktop does in a host's pane
  (`isLink`: no stat on the host), any path that is not a folder (no
  trailing `/`) is a link; a path that turns out to be missing answers
  `404` when it is opened. A web address without a scheme stays a web
  link, as on the desktop.
- Ctrl+click (Cmd+click on macOS) on a path fetches it with
  `X-GoRex-Download: 1` and the block of the pane:
  - an image (`png`, `jpg`, `jpeg`, `gif`, `webp`, `avif`, `bmp`, `ico`,
    `svg`) opens in a viewer on the page. The blob is checked by its first
    bytes before it is shown; an SVG is shown with `<img>`, which runs no
    script in it.
  - any other file downloads: a link to the blob with `download` set to
    the file's name.
  - the pane shows the download's progress on its header as the desktop
    shows a transfer, and an error in the status line (`No such file`,
    `A folder is not downloaded`, `Too large`).
- The viewer is a dialog over the window, as the Settings dialog: the
  page dimmed and frosted behind it as DESIGN.md's `paintFrost` and
  `dimmed` rules translate to CSS. It shows the image fitted to the window
  at first, its name and size, and buttons with lucide icons: zoom in
  (`zoom-in`), zoom out (`zoom-out`), actual size (`scan`), download
  (`download`), close (`x`); `zoom-in`, `zoom-out`, `scan` and `download`
  are added to `assets/icons` from lucide, which has them. The wheel with Ctrl zooms, a drag pans when
  the image is larger than the window, Escape or a click outside closes,
  and the keyboard returns to the pane. On a phone, two fingers zoom. The
  blob URL is revoked when the viewer closes.
- Images up to 50 MiB open in the viewer; a larger image downloads. The
  web client holds a file in memory, so it refuses a download past
  256 MiB in the browser with a message, below the gateway's 1 GiB.

## Differences from the desktop

- The desktop resumes a download and keeps the file in the Downloads
  folder; the web client fetches it whole each time, and only the
  browser's download saves it.
- The desktop opens any file in its app; the web client shows only images
  itself and downloads everything else.
- Local panes do not exist in the web client, so there is no "open the
  file in place".

## Tests

- Gateway (Go, `internal/rexapi`): a download of a file streams its bytes
  and headers; relative, `~/` and absolute paths resolve as `file.get`
  does; a folder answers `409`; a missing file `404`; a request without
  `X-GoRex-Download` `400`; a cross-site request without a trusted origin
  `403`; an untrusted origin `403` before any read; the welcome lists
  `gorex.download` only with `CapabilityFileTransfer`; a client that goes
  away ends the server's transfer.
- Web client (ego lite against a trial gateway): Ctrl over a path
  underlines it only on a host with `gorex.download`; Ctrl+click on a PNG
  opens the viewer with the right image; zoom, pan, Escape and focus
  return; a text file downloads with its name; a missing file and a folder
  show their errors; Ctrl+click under mouse tracking opens the file and
  reports nothing.

## Open questions

- Whether the viewer also opens PDFs (the browser's own viewer in an
  `<iframe>` of the blob) and plain text. Not in the first version.
- Whether an image the terminal draws inline (kitty graphics, sixel)
  opens in the same viewer on a click. That needs no download, as the page
  has the image; it is a separate change.
