# Web Client: Direct LAN & Self-Signed Connections via WebTransport

Status: built in #629.

## Problem

The hosted web client (`https://web.gorex.uk`, `docs/web-client.md`) runs in a secure HTTPS context, which blocks plain `http://` and `ws://` connections to a host's LAN listener (`internal/rexapi/lan.go`, port `7297`) as Mixed Content.

At the same time:
- **Hosting the web client on the GoRex host** is out of scope for the standalone web deployment.
- **Standard `WebSocket` (`wss://`)** in browsers does not support certificate pinning (`ownRelay`'s pinned self-signed certs are rejected by browsers without a public CA).
- **Public CA certificates (ACME / Let's Encrypt)** require either a public domain + DNS-01 automation or a `plex.direct`-style DNS service, adding infrastructure and credential management.
- **Built-in Web Tailscale** (`web/tailnet`) always routes through Tailscale's DERP relays when no native Tailscale client is installed on the device.

## Goal

Allow a phone or laptop running the hosted web client (`https://web.gorex.uk`) to connect directly to a GoRex host over the local network (or any UDP-reachable address) with **one QR scan or pairing link**, using a self-signed certificate pinned via **WebTransport `serverCertificateHashes`**:
- No public CA, no domain/DNS setup, no web server hosted on the GoRex machine, and no external relay.
- Reuses GoRex's existing `Transport` abstraction (`web/src/rex.ts`), origin allowlist (`internal/rexapi/web.go`), and desktop/CLI QR pairing (`hostqr.go`).

## Non-goals

- **NAT traversal / public internet hole-punching.** WebTransport has no built-in ICE/STUN/TURN; off-LAN connections continue to use Tailscale (`docs/web-tailscale.md`) or `tailcat`.
- **Supporting browsers without `WebTransport` / `serverCertificateHashes`.** Requires Chromium, Firefox, or Safari 26.4+ (iOS/macOS). Older Safari versions fall back to Tailscale or native Rex Go.
- **Replacing the existing HTTP/WebSocket LAN listener (`7297`).** Native apps and `http://` local dev continue to use port `7297` unchanged.

## Browser Constraints for `serverCertificateHashes`

Browsers enforce strict W3C WebTransport rules when authenticating a self-signed certificate by hash:
1. **Key algorithm:** The certificate's public key **must be ECDSA on the P-256 curve** (`elliptic.P256()`). RSA and Ed25519 keys are rejected.
2. **Maximum lifetime:** The certificate validity period (`NotAfter - NotBefore`) **must be ≤ 14 days**.
3. **Dedicated connection:** `allowPooling` must be `false` (default).
4. **Hash format:** Raw SHA-256 digest (32 bytes) of the DER-encoded X.509 certificate (`x509.Certificate.Raw`).

Because of rule (2), the host must **automatically rotate** its WebTransport certificate before it expires.

## Design

### 1. Host Side (`internal/rexapi`)

#### Listener & Port
Add a WebTransport (HTTP/3 over QUIC/UDP) listener alongside the existing LAN HTTP listener when LAN access is enabled (`LANConfig.Enabled`):
- **Default port:** `LANPort + 1` (`7298`/udp), or configurable via `LANConfig.WebTransportListen` (defaulting to the same host interface as `LANConfig.Listen` on UDP `7298`).
- **Library:** `github.com/quic-go/quic-go/http3` + `github.com/quic-go/webtransport-go`.
- **Access control:**
  - **Network check:** Enforces the same `LANConfig.Allow` CIDR filter on the remote UDP address as the HTTP LAN listener.
  - **Origin check:** Enforces `WebConfig.Origins` (`gorex rexapi web allow https://web.gorex.uk`) on the HTTP/3 `CONNECT` request's `Origin` header, matching `internal/rexapi/web.go`.

#### Certificate Lifecycle & Rotation
Store the WebTransport keypair and certificate in the gateway state directory (e.g. `wt-cert.pem` / `wt-key.pem`), separate from `ownRelay`'s long-lived cert:
- **Generation:** ECDSA P-256 keypair, self-signed X.509 certificate with `NotBefore = now - 1h` (clock skew margin) and `NotAfter = now + 10 days` (safely under the 14-day browser cap).
- **Rotation:** When the active certificate has `< 3 days` remaining, generate a new certificate automatically.
- **Hash exposure:** Expose the current certificate's base64url-encoded SHA-256 fingerprint via `rexapi` status (`server.status` / `gorex rexapi lan status` / `hostqr.go`) so the desktop app and CLI can embed it in pairing QR codes.

#### Multiplexed Streams over WebTransport
A single `WebTransport` session multiplexes all Rex API traffic over QUIC bidirectional streams, avoiding repeated handshakes:
- **Control stream:** Client opens a bidirectional stream to `/api/control` (or sends a tiny 1-line JSON/binary header identifying the route and subprotocol `rex.control.v1`, since WebTransport `createBidirectionalStream()` does not carry per-stream HTTP paths).
  - *Recommended framing:* Route all streams through a single WebTransport session established at `/api/wt` (`CONNECT https://<ip>:7298/api/wt`). Each bidirectional stream opened by the client begins with a short header frame:
    - Stream type `0x01`: Control (`rex.control.v1`)
    - Stream type `0x02`: Data (`rex.data.terminal.v1`, etc.)
    - Stream type `0x03`: HTTP-style request/response (for `Transport.fetch`: file downloads, uploads, `/api/health`)
  - On Control and Data streams, messages use length-prefixed framing (`[1B kind: text=1 | binary=2 | close=3][4B uint32BE length][payload]`) so `SocketLike` maps 1:1 with zero protocol changes to `Control` or `DataStream`.
  - On HTTP streams (`0x03`), reuse the lightweight request/response framing pattern already used by `web/tailnet` so `Transport.fetch` works identically for uploads and downloads.

### 2. Pairing URL & QR Code

#### URL Format
Reuse `web/src/main.ts`'s `?host=` quick-link mechanism, passing the certificate hash in the **URL fragment** (`#`) so it is never sent to Cloudflare or logged in HTTP access logs:

```text
https://web.gorex.uk/?host=https%3A%2F%2F192.168.1.50%3A7298#wt=sha256-<base64url>
```

- `?host=...` identifies the host URL (`https://<lan-ip>:7298`).
- `#wt=sha256-<base64url>` carries the 32-byte DER SHA-256 hash.
- When `main.ts` reads `?host=`, it also parses `location.hash` for `wt=sha256-...`, stores `certHash` on the `Host` object in `localStorage` (`web/src/hosts.ts`), and strips both query and hash from the address bar via `history.replaceState`.

#### Host UI & CLI (`hostqr.go`, `internal/rexapi/cli.go`)
- **`gorex rexapi lan qr` (or `gorex rexapi web qr`):** Prints a terminal QR code + pairing URL for the active LAN IP(s) and current `wt` cert hash.
- **Desktop app (`hostqr.go`):** In the existing Pair QR dialog, offer a **Web (LAN)** tab/toggle alongside the native Rex Go QR when `LANConfig.Enabled` is true, showing the `https://web.gorex.uk/?host=...#wt=...` QR code and a reminder if `https://web.gorex.uk` is not yet in `WebConfig.Origins` (with a one-click button to allow it).

### 3. Web Client (`web/src/`)

#### `Host` Model (`web/src/hosts.ts`)
Extend `Host`:
```ts
export interface Host {
  id: string;
  name: string;
  url: string;
  icon: HostIcon;
  kind?: "direct" | "tailnet" | "share";
  /** Base64url SHA-256 of the host's self-signed ECDSA cert for WebTransport. */
  certHash?: string;
}
```
- The manual "Add Host" form can also optionally accept an advanced "Certificate SHA-256" field (or paste the full pairing URL) so users without a camera can paste the link.

#### `webTransport()` Transport (`web/src/rex.ts`)
Add a `WebTransport`-backed `Transport` implementation (`kind: "direct"`):
- Selected automatically when `host.certHash` is present (or `url` uses the WebTransport path).
- Checks `typeof WebTransport === "function"`; if missing, surfaces a localized error (`"This browser does not support WebTransport; use Safari 26.4+, Chrome, or Tailscale."`).
- Opens one `new WebTransport(joinHostURL(url, "/api/wt"), { serverCertificateHashes: [{ algorithm: "sha-256", value: bytes.buffer }] })` session per `HostConn` lifecycle and multiplexes `socket()` and `fetch()` streams over it.
- Note on `Transport.url`: Because `certHash` is self-signed, plain browser `<a href>` / `<iframe>` downloads cannot bypass TLS on `https://<ip>:7298`; `Transport.url` returns `undefined` for WebTransport hosts so file downloads use `Transport.fetch()` + `Blob` URLs (the same path `tailnet` hosts already use).

#### Error Handling & Re-pairing UX
- **No WebTransport in the browser:** `webTransportAvailable()` (`web/src/webtransport.ts`) checks `typeof WebTransport` before a `certHash` host's connection is retried; `HostConn` then blocks the host (no backoff loop), and the page shows *"This browser can't reach a host directly"* naming the browsers that work, instead of the generic unreachable card.
- **The local network permission says which failure it is:** `localNetworkAccess()` asks `navigator.permissions.query({ name: "local-network-access" })`. Denied gets *"This site isn't allowed on your local network"* with where to change it, `prompt` gets *"Let this page reach your local network"*, and `granted` (or a browser without the permission) gets the causes below.
- **Certificate rotated or host down:** A browser reports both as the same opaque `WebTransportError: Opening handshake failed.`, so a `certHash` host's failure is not diagnosed by probing its gateway (a pinned self-signed certificate and the local network check would fail that probe too). The card names the two causes left once the permission is granted — `gorex rexapi lan status`, and re-scanning the pairing QR after a rotation — and puts the browser's own words behind *Learn more* (`webTransportProblem`).
- **Chrome Local Network Access (LNA):** When `https://web.gorex.uk` dials a private IP (`192.168.x.x`), Chrome 142+ blocks the request until the user allows Local Network Access for the page (`net::ERR_BLOCKED_BY_LOCAL_NETWORK_ACCESS_CHECKS` otherwise).
- **CSP (`web/_headers`):** `connect-src` already includes `https:`, which permits `new WebTransport("https://...")`.

## Security Considerations

1. **Fragment-only hash:** Placing `#wt=sha256-...` in the URL fragment ensures Cloudflare (`web.gorex.uk`) never receives the host's LAN IP? Note: `?host=` is a query param unless moved to the fragment too. Moving **both** into the fragment (e.g. `https://web.gorex.uk/#host=...&wt=sha256-...`) prevents even the user's LAN IP from appearing in Cloudflare request logs, while keeping `?host=` supported for backwards compatibility.
2. **Origin + CIDR enforcement:** Both `WebConfig.Origins` and `LANConfig.Allow` are enforced on the WebTransport `CONNECT` handshake, preventing arbitrary websites from probing the host's LAN WebTransport port even if they guess the IP.
3. **Short-lived ECDSA keys:** 10-day rotation limits exposure of any single certificate/hash pair.

## Rollout Plan

1. **Phase 1 — Core transport:**
   - `internal/rexapi`: Add rotating ECDSA P-256 cert manager + `webtransport-go` `/api/wt` listener behind `LANConfig.Enabled`.
   - `web/src`: Add `certHash` to `Host`, implement `WebTransport` `Transport` in `rex.ts`, and parse `#host=` / `#wt=` in `main.ts`.
2. **Phase 2 — Pairing UX:**
   - CLI: `gorex rexapi lan qr` printing pairing URL + terminal QR.
   - Desktop (`hostqr.go`): Add Web LAN QR view with one-click `web allow` check.
