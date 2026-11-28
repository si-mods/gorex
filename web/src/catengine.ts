// The tailcat engine's page half (tailcat.ts, docs/WEB-006-tailcat.md): the
// code that runs only once a tailcat host needs it, a chunk of its own.
import { t } from "./i18n";
import type { Transport } from "./rex";
import { EngineCat, NoCatEngine } from "./tailcat";
import { loadWasmEngine, WasmSocket, wasmTransport } from "./wasmengine";

export function loadEngine(progress: (received: number, total: number) => void, starting: () => void): Promise<void> {
  return loadWasmEngine(
    {
      engineAsset: "tailcat/engine",
      execAsset: "tailcat/exec",
      globalFactory: "gorexTailcat",
      logName: "tailcat",
      missingError: () => new NoCatEngine(),
    },
    progress,
    starting,
  );
}

/** A WebSocket over the tailcat engine, as SocketLike. */
export class TailcatSocket extends WasmSocket {
  constructor(node: EngineCat | undefined, address: string, path: string, protocol: string) {
    super(
      node ? (onEvent) => node.socket(address, path, protocol, onEvent) : undefined,
      () => t("tcIsOff"),
    );
  }
}

/** HTTP and WebSockets to a host's tailcat address through the engine. */
export function tailcatTransport(node: () => EngineCat | undefined, address: string): Transport {
  return wasmTransport({
    kind: "tailcat",
    node,
    offReason: () => t("tcIsOff"),
    openSocket: (n, path, protocol, onEvent) => n.socket(address, path, protocol, onEvent),
    request: (n, method, path, headers, body, timeoutMs) => n.request(address, method, path, headers, body, timeoutMs),
  });
}
