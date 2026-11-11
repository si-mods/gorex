# Web client

The Rex API gateway (`internal/rexapi`) serves a browser client at `/`.
It runs on the same origin as `/api/control` and `/api/data`. It lists
the sessions the gateway shows, opens one terminal live and sends what you
type. The terminal emulator is libghostty-vt, compiled to WebAssembly from
the Ghostty commit that GoRex's terminal binding uses. The client draws
the emulator's render state on a canvas.

This is phase 1, a prototype: one terminal at a time. It has no settings,
no uploads, no paste, no selection, no scrollback and no mouse.

## Build

You need Node 22 or later, Git and Go. The script gets Zig itself.

```sh
node web/build.mjs      # Windows: the same, from PowerShell or Git Bash
go build .              # embeds what the script wrote
```

`web/build.mjs` does these steps:

1. It gets Zig 0.17.0 for the host into the cache, if `ZIG` is not set.
2. It clones Ghostty and checks out the pinned commit. This is the same
   commit as `ios/scripts/build-ghostty-vt.sh`. Pass
   `--ghostty-source <path>` to use a checkout you already have.
3. It runs
   `zig build -Demit-lib-vt -Dtarget=wasm32-freestanding -Doptimize=ReleaseSmall`.
   It keeps `ghostty-vt.wasm` (about 850 KB) in the cache, so the next
   build skips this step. `--rebuild-wasm` builds it again.
4. It runs `npm ci` in `web/` if `web/node_modules` is missing, then type-checks
   the client (`tsc --noEmit`) and bundles it with esbuild.
5. It writes `index.html`, `app.js` and `ghostty-vt.wasm` into
   `internal/webui/dist`.

The cache is `$GOREX_WEB_CACHE`, else `$XDG_CACHE_HOME/gorex-web`, else
`~/.cache/gorex-web`.

`internal/webui/dist` is in `.gitignore`, except for a README. With that
README, `go:embed` has something to embed on a checkout that never ran the
script. Such a build serves a short page at `/` that says how to build the
client. `go build ./...` and the tests need no web build.

## Open it

Every listener of the gateway serves the client: the local network
listener, built-in Tailscale (HTTPS) and `--listen`. For development:

```sh
GoRex rexapi --listen http://127.0.0.1:7391
```

Then open `http://127.0.0.1:7391/`. The gateway applies its usual access
rules to the page. The local network listener lets in only the networks it
allows. Built-in Tailscale lets in only your own user.

## Origins

Native clients send no `Origin` header. They connect as before. A request
that has an `Origin` gets through only if its scheme, host and port are
those of the gateway that the request came to. Any other web page gets
403 on every path (`internal/rexapi/sameorigin.go`). Without this check,
any web page open in a browser on an allowed network could use the API
as that browser.

## How it works

- `web/src/rex.ts`: the control connection (`rex.control.v1`, hello at
  revision 10) and the data stream (`rex.data.terminal.v1`: HELLO with the
  ticket from `data.connect`, then WELCOME). It splits the stream into the
  GHOSTSNP snapshot, the program's output and the `rex1` APC messages, as
  the iOS app does.
- `web/src/ghostty.ts`: loads the module. It reads `ghostty_type_json` and
  checks that every struct field, enum value and cell bit the client uses
  is there, with the right width, before it uses them. All offsets and
  enum values come from that manifest. The `GHOSTTY_MODS_*` bits are
  `#define`s that the manifest does not have, so they are hard-coded.
- `web/src/terminal.ts`: decodes the snapshot into a terminal, then feeds
  it the output. It draws only the dirty rows and the cursor, and encodes
  keys with the libghostty-vt key encoder, set from the terminal's modes.
- `web/src/main.ts`: the list and the view. Typed bytes go to
  `com.superlogical.terminal.write`, one write at a time. The view takes the
  block (`session.own_block`) and sizes it to the canvas
  (`com.superlogical.terminal.resize`).
