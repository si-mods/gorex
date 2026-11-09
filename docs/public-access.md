# Public access without a VPN

GoRex can expose its Rex API over HTTPS with device credentials, independently
of Tailscale. Rex Go and the built-in Web terminal can use this listener over
ordinary Wi-Fi or cellular data. A reverse tunnel for hosts behind NAT is not
included.

The listener is off by default. It reaches the same running sessions as the
existing gateway. Disconnecting or revoking a client does not stop its shells.
An authorized device has full access to this user's terminals, like an SSH key;
this is device authorization for a personal server, not a multi-user service.

## HTTPS entry point

For a host with a public address, choose either of the following:

- Terminate HTTPS in a reverse proxy on the same machine. Configure it to forward
  HTTP and WebSocket upgrades to `127.0.0.1:7298`, then run
  `gorex rexapi public on --url https://terminal.example.com`.
- Have GoRex terminate TLS itself:
  `gorex rexapi public on --url https://terminal.example.com --listen 0.0.0.0:7298 --cert /path/fullchain.pem --key /path/privkey.pem`.
  Use a certificate trusted by the client, and include the external port in the
  URL if it is not 443. Restart the gateway after renewing these certificate files.

The URL must be an HTTPS origin, without a path or credentials. A plaintext
listener can bind only to a literal loopback IP. Do not forward the existing
unauthenticated LAN listener as a substitute: the public listener checks device
credentials on API endpoints, including both WebSocket handshakes. Only the
pairing endpoint and the static browser shell are available before pairing.

The GoRex session server starts and supervises this gateway automatically, as
for LAN and built-in Tailscale. `gorex rexapi public status` shows its configuration
and last reported listener status. `gorex rexapi public off` closes public
connections and retains device authorizations for the next time it is enabled.
No existing LAN, desktop Tailscale, or tailcat configuration needs to change.

## Pair a browser

1. Enable the HTTPS entry point above, then run `gorex rexapi public pair`
   on the target computer.
2. Open its printed invitation URL in a desktop or phone browser. Alternatively,
   open the HTTPS address and paste the pairing code into the page.
3. Name the browser and choose **Pair this browser**. Choose a space and terminal
   tab; a tab with multiple panes offers a pane selector. **New terminal** adds a
   terminal to the current space, or creates a space if none exists.

The Web client is embedded in the Go binary, including xterm.js: it needs no
separate Web hosting, build step, or CDN at runtime. Serve it from the gateway's
own origin, with both `/api/control` and `/api/data` WebSocket upgrades forwarded
by the reverse proxy. The page removes an invitation's fragment immediately.

Browser authorization uses a Secure, HttpOnly, SameSite=Strict cookie. Device
credentials are not stored in JavaScript storage. Refreshing or closing the page
keeps the terminal process running; refreshing restores its screen and available
history. Brief output interruptions resume the stream, or take a fresh snapshot
when the retained output has expired. Unconfirmed keyboard input is never
automatically retried. Very fast producers may require a fresh snapshot; input
still uses its separate control socket, including the **Ctrl+C** button.

**Fit to window** takes the terminal's size for this browser. Another client can
take it back; click the terminal or **Fit to window** to claim it again. Use the
toolbar to copy selected text, paste, or interrupt a program. Hold Shift to select
text while a program captures the mouse. Clipboard access requires browser
permission; normal keyboard copy/paste remains available.

**Unpair browser** revokes this browser on the server, clears its cookie and
closes its connections. Other paired devices and running shells remain intact.
The server's `public revoke` command below has the same effect for a lost device.

This first Web client shows one terminal at a time on one host. It does not
reproduce the desktop's split geometry, browser/editor panes, uploads, or remote
host aggregation. It requires a modern browser with WebSocket and BigInt support;
real iPhone/Safari keyboard and IME behavior still needs device testing.

## Pair Rex Go

1. On the target computer, run `gorex rexapi public pair`. It prints a QR
   invitation and a pairing code, valid once for five minutes.
2. In Rex Go, open Remote Hosts, then Pair via QR Code and scan the invitation.
   Alternatively, add the HTTPS address manually and enter the pairing code.
3. Save the host. Rex Go exchanges the code over HTTPS and stores the resulting
   device credential in Keychain. The existing address-only Rex QR codes and
   Tailscale connections continue to work.

Keep the invitation private: possessing it authorizes a device until it is
redeemed or expires. Its code is in the URL fragment, which HTTP requests do not
send to the server. Rex Go strips it before saving the host's address and never
follows redirects when redeeming it.

`gorex rexapi public devices` lists authorized device IDs, names, and creation
times. `gorex rexapi public revoke DEVICE_ID` removes one authorization and closes
all of that device's public HTTP/WebSocket connections. Other devices and the
running terminal processes are unaffected. Removing a host from Rex Go deletes
its local Keychain credential; use the server's revoke command to invalidate it
on the server as well.

Device administration is accessible only through this user's local gateway
socket. The private `rexapi/devices.json` state file contains hashes of random
256-bit credentials and pairing codes, never their plaintext. Authorizations
survive gateway restarts; reconnect tokens and data tickets remain short-lived
and are bound to the authenticated device.

## Client interface

`POST /api/access/pair` accepts a JSON object with `code` and `name`, and returns
`device_id` and `token`. Native clients send `Authorization: Bearer TOKEN` on
control, data, uploads, and ordinary API requests. Tokens do not go in URLs.

The pairing response also sets `__Host-gorex-device`, a Secure, HttpOnly,
SameSite=Strict cookie, for browser clients hosted at the gateway's own origin.
Requests carrying an Origin header must exactly match the configured HTTPS
origin. A public Web client must be served at that same origin. This interface
does not enable cross-origin browser access.

The local-only administration endpoints are `POST /api/access/pairing`,
`GET /api/access/devices`, and `POST /api/access/revoke` with `{"id":"…"}`.
They are GoRex extensions outside the Rex control protocol.

`GET /api/web/session` returns the authenticated host and transport for the Web
shell. `POST /api/access/logout` revokes the current public device and expires
the browser cookie. Neither accepts anonymous callers. Static files under the
explicit `/web/` allowlist and `/` can be loaded before pairing; source tests,
dependency metadata, and arbitrary paths are not served.

## Web development

The runtime assets live under `internal/rexapi/web/`. Go builds embed the checked-in
assets. To update dependencies, use the pinned lockfile with `npm ci`, then
`npm run vendor` in that directory; commit the copied bundles and MIT license
files together. No bundler or runtime package manager is required.

Run `node --test internal/rexapi/web/core.test.js` for byte framing, Unicode and
selection checks, and `go test ./internal/rexapi` for authorization, VT attach,
resume and flow control. The opt-in `TestWebBrowserPreview` fixture can serve
disposable sessions on localhost for browser QA; its HTTP-only localhost setup
is confined to tests and does not relax the production HTTPS requirement.

## Scope

This supplies single-host browser access and the authenticated direct-connection
path for #330 and #331.
A self-hosted relay still needs an outbound connector on the target machine,
authenticated host routing, and an explicit transport trust model. The existing
`gcoord` is a device directory, not a terminal traffic relay.
