# Public access without a VPN

GoRex can expose its Rex API over HTTPS with device credentials, independently
of Tailscale. Rex Go can use this listener over ordinary Wi-Fi or cellular data.
The same authentication boundary supports a future Web client; this change does
not add a browser terminal UI or a reverse tunnel for hosts behind NAT.

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
credentials on every endpoint, including both WebSocket handshakes.

The GoRex session server starts and supervises this gateway automatically, as
for LAN and built-in Tailscale. `gorex rexapi public status` shows its configuration
and last reported listener status. `gorex rexapi public off` closes public
connections and retains device authorizations for the next time it is enabled.
No existing LAN, desktop Tailscale, or tailcat configuration needs to change.

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

## Scope

This supplies the authenticated direct-connection foundation of #330 and #331.
A self-hosted relay still needs an outbound connector on the target machine,
authenticated host routing, and an explicit transport trust model. The existing
`gcoord` is a device directory, not a terminal traffic relay.
