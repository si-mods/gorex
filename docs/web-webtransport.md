# Web client: direct LAN and self-signed connections with WebTransport

Status: built (#629, #828).

This doc describes how the hosted web client connects to a GoRex host over the local network with WebTransport. #629 built the host listener, the CLI QR code and the web client. #828 built the Web (LAN) tab of the desktop pair dialog. #833 updated this doc.

## Problem

The hosted web client (`https://web.gorex.uk`, `docs/web-client.md`) runs in a secure HTTPS context. That context blocks plain `http://` and `ws://` connections to the LAN listener of a host (`internal/rexapi/lan.go`, port `7297`) as Mixed Content.

Four other options do not solve this:

- **Hosting the web client on the GoRex host** is out of scope for the standalone web deployment.
- **Standard `WebSocket` (`wss://`)** in browsers does not support certificate pinning. Browsers reject the self-signed certificates that `ownRelay` pins. They accept only certificates from a public CA.
- **Public CA certificates (ACME / Let's Encrypt)** need either a public domain with DNS-01 automation or a `plex.direct`-style DNS service. Both add infrastructure and credential management.
- **Built-in Web Tailscale** (`web/tailnet`) always routes through the DERP relays of Tailscale when the device has no native Tailscale client.

## Goal

A phone or laptop that runs the hosted web client (`https://web.gorex.uk`) can connect directly to a GoRex host. The network can be the local network or any UDP-reachable address. The user needs **one QR scan or pairing link**. The connection uses a self-signed certificate. The client pins it with **WebTransport `serverCertificateHashes`**:

- No public CA, no domain or DNS setup, no web server on the GoRex machine, and no external relay.
- The design reuses the existing `Transport` abstraction of GoRex (`web/src/rex.ts`), the origin allowlist (`internal/rexapi/web.go`) and the desktop and CLI QR pairing (`hostqr.go`).

## Non-goals

- **NAT traversal and public internet hole-punching.** WebTransport has no built-in ICE, STUN or TURN. Connections outside the LAN continue to use Tailscale (`docs/web-tailscale.md`) or `tailcat`.
- **Support for browsers without `WebTransport` or `serverCertificateHashes`.** The feature needs Chromium, Firefox, or Safari 26.4+ (iOS and macOS). Older Safari versions fall back to Tailscale or native Rex Go.
- **Replacement of the existing HTTP and WebSocket LAN listener (`7297`).** Native apps and `http://` local development continue to use port `7297` without change.

## Design

### Browser constraints for `serverCertificateHashes`

Browsers enforce strict W3C WebTransport rules when they authenticate a self-signed certificate by hash:

1. **Key algorithm:** The public key of the certificate **must be ECDSA on the P-256 curve** (`elliptic.P256()`). Browsers reject RSA and Ed25519 keys.
2. **Maximum lifetime:** The validity period of the certificate (`NotAfter - NotBefore`) **must be 14 days or less**.
3. **Dedicated connection:** `allowPooling` must be `false` (the default).
4. **Hash format:** The hash is the raw SHA-256 digest (32 bytes) of the DER-encoded X.509 certificate (`x509.Certificate.Raw`).

Because of rule 2, the host must **rotate** its WebTransport certificate automatically before the certificate expires.

### What the user sees

1. The user opens the Pair dialog of the local network listener on the desktop app, or runs `gorex rexapi lan qr` in a terminal.
2. The user opens the link in a browser, or scans the QR code with a phone on the same network.
3. The web client stores the host, strips the pairing data from the address bar and connects with WebTransport.
4. If the connection fails, the page names the likely cause. See "Error handling and re-pairing" below.

### Host side (`internal/rexapi`)

#### Listener and port

When `LANConfig.Enabled` turns on LAN access, the host starts a WebTransport listener (HTTP/3 over QUIC and UDP) beside the LAN HTTP listener:

- **Port:** The WebTransport port is `LANPort + 1` (`7298`/udp). The host address is the same as for `LANConfig.Listen`. `wtListenAddr` in `internal/rexapi/webtransport.go` derives the address. Port `0` stays `0`. The code has no separate setting for this port. An earlier version of this doc named `LANConfig.WebTransportListen`. That field does not exist.
- **Library:** `github.com/quic-go/quic-go/http3` and `github.com/quic-go/webtransport-go`.
- **Access control:**
  - **Network check:** The listener enforces the same `LANConfig.Allow` CIDR filter on the remote UDP address as the HTTP LAN listener.
  - **Origin check:** The listener enforces `WebConfig.Origins` (`gorex rexapi web allow https://web.gorex.uk`) on the `Origin` header of the HTTP/3 `CONNECT` request. This matches `internal/rexapi/web.go`.

#### Certificate lifecycle and rotation

The host stores the WebTransport keypair and certificate in the gateway state directory (for example `wt-cert.pem` and `wt-key.pem`). They are separate from the long-lived certificate of `ownRelay`. The code is in `internal/rexapi/wtcert.go`.

- **Generation:** The host makes an ECDSA P-256 keypair. It makes a self-signed X.509 certificate with `NotBefore = now - 1h` (clock skew margin) and `NotAfter = now + 10 days` (under the 14-day browser limit).
- **Rotation:** When the active certificate has less than 3 days remaining, the host generates a new certificate automatically.
- **Hash exposure:** The host exposes the base64url-encoded SHA-256 fingerprint of the current certificate through the `rexapi` status (`server.status`, `gorex rexapi lan status` and `hostqr.go`). The desktop app and the CLI embed it in pairing QR codes.

#### Multiplexed streams over WebTransport

One `WebTransport` session carries all Rex API traffic over QUIC bidirectional streams. This avoids repeated handshakes.

- The client opens one session at `/api/wt` (`CONNECT https://<ip>:7298/api/wt`).
- `WebTransport` `createBidirectionalStream()` does not carry an HTTP path for each stream. Each bidirectional stream that the client opens therefore starts with a short header frame. The first byte is the stream type:
  - Stream type `0x01`: socket (control and data). The code names it `wtStreamSocket`.
  - Stream type `0x03`: HTTP-style request and response (for `Transport.fetch`: file downloads, uploads and `/api/health`). The code names it `wtStreamHTTP`.
  - The code does not define stream type `0x02`. An earlier version of this doc planned `0x02` for a data stream.
- On socket streams, messages use length-prefixed framing: `[1B kind: text=1 | binary=2 | close=3][4B uint32BE length][payload]`. `SocketLike` maps 1:1 with no protocol change to `Control` or `DataStream`.
- On HTTP streams (`0x03`), the code reuses the lightweight request and response framing of `web/tailnet`. `Transport.fetch` works the same way for uploads and downloads.

### Pairing URL and QR code

#### URL format

The link of the page reuses the quick-link mechanism of `web/src/main.ts`. The **fragment** (`#`) holds all the data. A browser never sends the fragment to Cloudflare, and an HTTP access log never sees it. The fragment holds the address of the host, the hash of the certificate and, since 0.43, the pairing token that the gateway demands.

```text
https://web.gorex.uk/#host=https%3A%2F%2F192.0.2.50%3A7298&wt=sha256-<base64url>&k=<base64url>
```

- `host=` is the WebTransport address of the gateway, `https://<lan-ip>:7298`. `main.ts` reads it from the fragment. Older links had it in the query, and `main.ts` reads it from there too.
- `wt=sha256-<base64url>` is the 32-byte DER SHA-256 of the certificate of the gateway, for `serverCertificateHashes`.
- `k=<base64url>` is the pairing token of the listener. The page sends it as `?k=` on each connection (`internal/rexapi/lan.go`). Only the fragment carries it. A query would go to the host of the page.
- `main.ts` stores the three values on the `Host` object in `localStorage` (`web/src/hosts.ts`). It strips them from the address bar with `history.replaceState`.

`rexapi.LANConfig.WebPairURL` builds this link from the state of the listener. `gorex rexapi lan qr` and the pair dialog of the desktop app both use it. Because of this, the two cannot drift apart.

#### Host UI and CLI (`hostqr.go`, `internal/rexapi/cli.go`)

- **`gorex rexapi lan qr` (or `gorex rexapi web qr`):** The command prints the same link as a terminal QR code and as text. The link has the active LAN address, the hash of the certificate and the pairing token. The command takes `--origin` for a page that another host serves.
- **Desktop app (`hostqr.go`):** The Pair dialog of the local network listener has two tabs. **This address** shows the native code. **Web (LAN)** shows the web client link. The Web (LAN) tab exists in `main` (#828).
  - The web tab leads with **Open in Browser**. This button gives the link to the default browser (`mygo.Shell.OpenExternal`). The dialog names a failure. The dialog runs on the computer that opens the page.
  - Under "Or scan this from a phone on the same network:" the tab shows the link as a code. This is for a phone when no terminal is at hand.
  - The link holds the pairing token. The tab therefore shows the image only. It never shows the link and has no Copy button. The Rex Go code works the same way.
  - When `https://web.gorex.uk` is not yet in `WebConfig.Origins`, the tab says so. It shows a button that adds it (`rexapi.AllowWebOrigin`). This is the same write that `gorex rexapi web allow` makes.

### Web client (`web/src/`)

#### `Host` model (`web/src/hosts.ts`)

The `Host` interface has an optional `certHash`:

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

- The manual "Add Host" form can also accept an advanced "Certificate SHA-256" field, or the full pairing URL. Users without a camera can paste the link.

#### `webTransport()` transport (`web/src/webtransport.ts`)

`webTransport()` is a `WebTransport`-backed `Transport` implementation (`kind: "direct"`). An earlier version of this doc placed it in `web/src/rex.ts`.

- The client selects it automatically when `host.certHash` is present.
- It checks `typeof WebTransport === "function"`. If the browser has no `WebTransport`, it shows a localized error (`"This browser does not support WebTransport; use Safari 26.4+, Chrome, or Tailscale."`).
- It opens one `new WebTransport(joinHostURL(url, "/api/wt"), { serverCertificateHashes: [{ algorithm: "sha-256", value: bytes.buffer }] })` session for each `HostConn` lifecycle. It multiplexes the `socket()` and `fetch()` streams over that session.
- `Transport.url` returns `undefined` for WebTransport hosts. The certificate is self-signed. A plain browser `<a href>` or `<iframe>` download cannot bypass TLS on `https://<ip>:7298`. File downloads therefore use `Transport.fetch()` and `Blob` URLs. The `tailnet` hosts already use the same path.

#### Error handling and re-pairing

- **No WebTransport in the browser:** `webTransportAvailable()` (`web/src/webtransport.ts`) checks `typeof WebTransport` before the client retries the connection of a `certHash` host. `HostConn` then blocks the host, so there is no backoff loop. The page shows *"This browser can't reach a host directly"* and names the browsers that work. It does not show the generic unreachable card.
- **The local network permission names the failure:** `localNetworkAccess()` asks `navigator.permissions.query({ name: "local-network-access" })`.
  - If the answer is `denied`, the page shows *"This site isn't allowed on your local network"* and where to change it.
  - If the answer is `prompt`, the page shows *"Let this page reach your local network"*.
  - If the user allowed the permission (`granted`), or the browser has no such permission, the page shows the causes in the next item.
- **Certificate rotated or host down:** A browser reports both failures with one opaque error. The error reads `WebTransportError: Opening handshake failed.` The client therefore does not probe the gateway of a `certHash` host to diagnose a failure. The probe would also fail, because of the pinned self-signed certificate and the local network check. The card names the two causes that remain when the user allows the permission. They are `gorex rexapi lan status`, and a new scan of the pairing QR code after a rotation. The card puts the words of the browser behind *Learn more* (`webTransportProblem`).
- **Chrome Local Network Access (LNA):** When `https://web.gorex.uk` dials a private IP (`192.168.x.x`), Chrome 142+ blocks the request until the user allows Local Network Access for the page. Chrome otherwise reports `net::ERR_BLOCKED_BY_LOCAL_NETWORK_ACCESS_CHECKS`.
- **CSP (`web/_headers`):** `connect-src` already includes `https:`. This permits `new WebTransport("https://...")`.

## Security

1. **Fragment-only data:** The pairing link puts the hash (`#wt=sha256-...`), the host address and the pairing token in the URL fragment. Cloudflare (`web.gorex.uk`) never receives them. This keeps even the LAN IP of the user out of the request logs of Cloudflare. A `?host=` query parameter stays supported for old links.
2. **Origin and CIDR enforcement:** The WebTransport `CONNECT` handshake enforces both `WebConfig.Origins` and `LANConfig.Allow`. Arbitrary websites cannot probe the WebTransport port of the host on the LAN, even if they guess the IP.
3. **Short-lived ECDSA keys:** The 10-day rotation limits the exposure of any one certificate and hash pair.

## Tests

- The Web (LAN) tab of the pair dialog: `TestLocalPairQRWebTab` in `hostqr_web_test.go`.

## Phases

1. Core transport (#629):
   - `internal/rexapi`: a rotating ECDSA P-256 certificate manager and a `webtransport-go` `/api/wt` listener behind `LANConfig.Enabled`.
   - `web/src`: `certHash` on `Host`, the `WebTransport` `Transport`, and the parsing of `#host=` and `#wt=` in `main.ts`.
2. Pairing UX:
   - CLI: `gorex rexapi lan qr` prints the pairing URL and a terminal QR code (#629).
   - Desktop (`hostqr.go`): the Web (LAN) QR tab with a one-click `web allow` check (#828).
