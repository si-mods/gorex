// Built-in Tailscale's engine (tailnet.ts, docs/WEB-002-tailscale.md): the
// code that runs only once the user turns Tailscale on, a chunk of its own
// (docs/REF-007-web-client.md, Loading on demand).
import { t } from "./i18n";
import type { Transport } from "./rex";
import { EngineNode, NoEngine } from "./tailnet";
import { loadWasmEngine, WasmSocket, wasmTransport } from "./wasmengine";

export function loadEngine(progress: (received: number, total: number) => void, starting: () => void): Promise<void> {
  return loadWasmEngine(
    {
      engineAsset: "tailnet/engine",
      execAsset: "tailnet/exec",
      globalFactory: "gorexTailnet",
      logName: "tailnet",
      missingError: () => new NoEngine(),
    },
    progress,
    starting,
  );
}

/** A WebSocket over the Tailscale engine, as SocketLike. */
export class TailnetSocket extends WasmSocket {
  constructor(node: EngineNode | undefined, host: string, path: string, protocol: string) {
    super(
      node ? (onEvent) => node.socket(host, path, protocol, location.origin, onEvent) : undefined,
      () => t("tsIsOff"),
    );
  }
}

/** HTTPS and WebSockets to a host's MagicDNS name through the engine, with this page's Origin. */
export function tailnetTransport(node: () => EngineNode | undefined, host: string): Transport {
  return wasmTransport({
    kind: "tailnet",
    node,
    offReason: () => t("tsIsOff"),
    openSocket: (n, path, protocol, onEvent) => n.socket(host, path, protocol, location.origin, onEvent),
    request: (n, method, path, headers, body, timeoutMs) => n.request(host, method, path, headers, body, location.origin, timeoutMs),
  });
}
