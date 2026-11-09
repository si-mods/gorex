import {
  VT_PROTOCOL,
  VTParser,
  hello,
  resume,
  welcome,
  acknowledgement,
  blocks,
  choose,
  textChunk,
  unwrapEvent,
} from "./core.js";

const $ = (id) => document.getElementById(id);
const words = {
  en: {
    connecting: "Connecting…",
    connected: "Connected",
    reconnecting: "Reconnecting…",
    offline: "Disconnected",
    retry: "Retry",
    unpair: "Unpair browser",
    pairTitle: "Connect to your terminals.",
    pairDescription:
      "Pair this browser to continue the sessions running on your computer.",
    pairCode: "Pairing code",
    deviceName: "Browser name",
    pair: "Pair this browser",
    pairHelp: "On your computer, get a code with",
    pairExpiry: "Codes work once and expire after five minutes.",
    privateConnection: "A direct connection to your computer.",
    thisComputer: "This computer",
    spaces: "Spaces",
    sessionsStay: "Your sessions keep running when you close this page.",
    terminalWorkspace: "TERMINAL WORKSPACE",
    newTerminal: "New terminal",
    emptyTitle: "A little room to work.",
    emptyDescription: "Open a terminal to start, or choose another space.",
    fit: "Fit to window",
    copy: "Copy",
    paste: "Paste",
    interrupt: "Interrupt program (Ctrl+C)",
    workspace: "Workspace",
    terminal: "Terminal",
    ready: "Live session",
    ended: "Program exited",
    restored: "Session restored",
    selectToCopy:
      "Select terminal text first. Hold Shift while dragging inside a mouse-aware program.",
    copied: "Copied",
    manualCopy: "Use ⌘C or Ctrl+Shift+C to copy the selection.",
    manualPaste: "Use ⌘V or Ctrl+Shift+V to paste.",
    lostInput: "Connection interrupted. Unconfirmed input was not sent again.",
    largeInput: "This paste is too large. Paste a smaller section.",
    pairFailed:
      "Pairing failed. Check that the code is correct and has not expired.",
    revoked:
      "This browser is no longer paired. Get a new code from your computer.",
    unavailable: "Could not reach this computer.",
    updateRequired: "Update the GoRex server to use this browser client.",
    owning: "Fit to your window",
    following: "Following the terminal’s size",
    newFailed: "Could not create a terminal.",
    browser: "Browser",
    unknown: "Unknown error",
  },
  zh: {
    connecting: "正在连接…",
    connected: "已连接",
    reconnecting: "正在重连…",
    offline: "连接已断开",
    retry: "重试",
    unpair: "取消浏览器配对",
    pairTitle: "在浏览器里，继续工作。",
    pairDescription: "配对此浏览器，即可继续使用电脑上正在运行的终端会话。",
    pairCode: "配对码",
    deviceName: "浏览器名称",
    pair: "配对此浏览器",
    pairHelp: "在目标电脑上运行以下命令获取配对码",
    pairExpiry: "配对码仅可使用一次，五分钟后过期。",
    privateConnection: "直接连接到你的电脑。",
    thisComputer: "当前设备",
    spaces: "空间",
    sessionsStay: "关闭网页后，终端会话仍会继续运行。",
    terminalWorkspace: "终端工作空间",
    newTerminal: "新建终端",
    emptyTitle: "从这里，开始工作。",
    emptyDescription: "新建一个终端，或选择其他空间。",
    fit: "适配窗口",
    copy: "复制",
    paste: "粘贴",
    interrupt: "中断程序（Ctrl+C）",
    workspace: "工作空间",
    terminal: "终端",
    ready: "会话运行中",
    ended: "程序已退出",
    restored: "会话已恢复",
    selectToCopy: "请先选择终端文字。程序接管鼠标时，可按住 Shift 拖选。",
    copied: "已复制",
    manualCopy: "请按 ⌘C 或 Ctrl+Shift+C 复制选区。",
    manualPaste: "请按 ⌘V 或 Ctrl+Shift+V 粘贴。",
    lostInput: "连接中断，未确认的输入没有重复发送。",
    largeInput: "粘贴内容过长，请分段粘贴。",
    pairFailed: "配对失败，请检查配对码是否正确、是否已过期。",
    revoked: "此浏览器的配对已失效，请在电脑上获取新的配对码。",
    unavailable: "暂时无法连接到这台电脑。",
    updateRequired: "请更新 GoRex 服务端以使用浏览器客户端。",
    owning: "适配当前窗口",
    following: "跟随终端尺寸",
    newFailed: "未能创建终端。",
    browser: "浏览器",
    unknown: "未知错误",
  },
};
const saved = (key, fallback) => {
  try {
    return JSON.parse(localStorage.getItem(key)) ?? fallback;
  } catch {
    return fallback;
  }
};
const save = (key, value) => {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {}
};
let lang = saved(
  "gorex.language",
  navigator.language.startsWith("zh") ? "zh" : "en",
);
if (!words[lang]) lang = "en";
const t = (key) => words[lang][key] || words.en[key] || key;
let control, terminal, fitAddon, dataStream, metadata;
let views = [],
  selected = saved("gorex.selection", {}),
  selectionGeneration = 0;
let connecting = false,
  authenticated = false,
  stopped = false,
  refreshing = false,
  refreshAgain = false;
let reconnectToken,
  reconnectTimer,
  refreshTimer,
  toastTimer,
  resizeTimer,
  retries = 0,
  state = "connecting";
let sending = false,
  inputQueue = [],
  inputBytes = 0;
let fontSize = Math.min(
  24,
  Math.max(10, Number(saved("gorex.fontSize", 14)) || 14),
);

function toast(text) {
  $("toast").textContent = text;
  $("toast").hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => ($("toast").hidden = true), 4500);
}
function connection(next) {
  state = next;
  $("connection").className =
    `connection ${next === "connected" ? "online" : next === "offline" ? "offline" : ""}`;
  $("connection").lastElementChild.textContent = t(next);
  $("reconnect").hidden = next !== "offline";
  if (terminal)
    terminal.options.disableStdin =
      next !== "connected" || !dataStream?.started;
}
function translate() {
  document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
  for (const element of document.querySelectorAll("[data-i18n]"))
    element.textContent = t(element.dataset.i18n);
  $("language").textContent = lang === "zh" ? "EN" : "中文";
  $("sidebar").setAttribute("aria-label", t("spaces"));
  $("spaces").setAttribute("aria-label", t("spaces"));
  $("interrupt").title = t("interrupt");
  $("interrupt").setAttribute("aria-label", t("interrupt"));
  if (terminal)
    terminal.textarea.setAttribute(
      "aria-label",
      lang === "zh" ? "终端输入" : "Terminal input",
    );
  connection(state);
  if (dataStream?.ready) dataStream.showState();
  renderNavigation();
}
function wsURL(path) {
  const url = new URL(path, location.href);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  return url;
}
async function api(path, body) {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
  });
  const answer = await response.json().catch(() => ({}));
  if (!response.ok)
    throw Object.assign(new Error(answer.error || t("unavailable")), {
      status: response.status,
    });
  return answer;
}

class Control {
  constructor(token) {
    this.token = token;
    this.pending = new Map();
    this.next = 0;
    this.ready = false;
  }
  open() {
    return new Promise((resolve, reject) => {
      const ws = (this.ws = new WebSocket(
        wsURL("/api/control"),
        "rex.control.v1",
      ));
      const timer = setTimeout(() => {
        reject(new Error(t("unavailable")));
        ws.close();
      }, 10000);
      ws.onopen = () =>
        ws.send(
          JSON.stringify({
            type: "hello",
            api_revision: 11,
            reconnect_token: this.token,
            client: {
              kind: "app",
              platform: "web",
              name: "GoRex Web",
              label: $("device-name").value || t("browser"),
            },
          }),
        );
      ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          if (message.type === "welcome") {
            clearTimeout(timer);
            this.ready = true;
            this.id = message.client.id;
            this.token = message.client.reconnect_token;
            this.features = message.features || [];
            resolve(message);
            return;
          }
          if (message.type === "response") {
            const pending = this.pending.get(message.id);
            if (!pending) return;
            this.pending.delete(message.id);
            clearTimeout(pending.timer);
            if (message.error)
              pending.reject(
                Object.assign(new Error(message.error.message), message.error),
              );
            else pending.resolve(message.payload || {});
          } else this.onEvent?.(message);
        } catch (error) {
          reject(error);
          ws.close(1002);
        }
      };
      ws.onerror = () => reject(new Error(t("unavailable")));
      ws.onclose = () => {
        clearTimeout(timer);
        this.ready = false;
        reject(new Error(t("unavailable")));
        for (const p of this.pending.values()) {
          clearTimeout(p.timer);
          p.reject(new Error(t("offline")));
        }
        this.pending.clear();
        this.onClose?.();
      };
    });
  }
  call(method, payload = {}) {
    if (!this.ready || this.ws.readyState !== WebSocket.OPEN)
      return Promise.reject(new Error(t("offline")));
    return new Promise((resolve, reject) => {
      const id = String(++this.next);
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(t("unavailable")));
      }, 15000);
      this.pending.set(id, { resolve, reject, timer });
      this.ws.send(JSON.stringify({ type: "request", id, method, payload }));
    });
  }
  block(method, target, args) {
    return this.call(`com.superlogical.terminal.${method}`, {
      session_id: target.space,
      block_id: target.block,
      args,
    });
  }
  close() {
    this.onClose = null;
    this.ws?.close(1000);
    this.ready = false;
  }
}

// The terminal survives a dropped socket. Rendering is serial, and every data
// frame is acknowledged only after xterm processed it, bounding server output.
class TerminalData {
  constructor(target) {
    this.target = { ...target };
    this.received = 0n;
    this.queue = Promise.resolve();
    this.ready = false;
    this.owned = false;
    this.stopped = false;
    this.generation = 0;
    this.failures = 0;
  }
  async open() {
    if (this.stopped || !control?.ready || this.opening) return;
    if (this.ws && this.ws.readyState <= WebSocket.OPEN) return;
    this.opening = true;
    const generation = ++this.generation;
    try {
      await this.queue.catch(() => {});
      let first,
        isResume = !!this.token;
      if (isResume) first = resume(this.token, this.received);
      else {
        const result = await control.call("data.connect", {
          session_id: this.target.space,
          block_id: this.target.block,
          data_ref_id: "terminal",
          subprotocol: VT_PROTOCOL,
        });
        first = hello(result.ticket);
      }
      if (this.stopped || generation !== this.generation) return;
      const ws = (this.ws = new WebSocket(wsURL("/api/data"), VT_PROTOCOL));
      ws.binaryType = "arraybuffer";
      let greeted = false,
        rendered = 0n;
      ws.onopen = () => ws.send(first);
      ws.onmessage = (event) => {
        if (this.stopped || ws !== this.ws) return;
        const bytes = new Uint8Array(event.data);
        if (!greeted) {
          try {
            const result = welcome(bytes);
            if (isResume && result.nonce !== this.nonce)
              throw new Error("Changed stream nonce");
            this.token = result.token;
            this.nonce = result.nonce;
            if (!isResume) {
              this.parser = new VTParser(this.nonce);
              this.received = 0n;
            } else {
              this.live();
              this.takeControl();
            }
            greeted = true;
            this.opening = false;
          } catch {
            this.token = null;
            this.parser = null;
            this.opening = false;
            ws.close(1000);
          }
          return;
        }
        this.queue = this.queue
          .then(async () => {
            if (this.stopped) return;
            for (const item of this.parser.feed(bytes)) {
              if (item.bytes)
                await new Promise((resolve) =>
                  terminal.write(item.bytes, resolve),
                );
              else this.message(item.message);
            }
            this.received += BigInt(bytes.length);
            rendered += BigInt(bytes.length);
            if (ws.readyState === WebSocket.OPEN && this.ws === ws)
              ws.send(acknowledgement(rendered));
          })
          .catch(() => {
            this.token = null;
            this.parser = null;
            ws.close(1002);
          });
      };
      ws.onerror = () => {};
      ws.onclose = (event) => {
        if (ws !== this.ws || this.stopped) return;
        this.ws = null;
        this.opening = false;
        this.ready = false;
        if (event.code === 4000) {
          this.stopped = true;
          scheduleRefresh();
          return;
        }
        if ([1002, 1008, 1013].includes(event.code)) this.token = null;
        // Input has a separate control socket. Keep it usable while a slow
        // output viewer reattaches, especially Ctrl+C for a fast producer.
        terminal.options.disableStdin = !control?.ready || !this.started;
        $("recovering").hidden = false;
        $("terminal-state").textContent = t("reconnecting");
        this.retry();
      };
    } catch (error) {
      this.opening = false;
      if (!this.stopped) {
        if (["not_found", "session_not_found"].includes(error.code))
          scheduleRefresh();
        this.retry();
      }
    }
  }
  retry() {
    clearTimeout(this.timer);
    if (this.stopped) return;
    this.timer = setTimeout(
      () => this.open(),
      Math.min(8000, 500 * 2 ** Math.min(this.failures++, 4)),
    );
  }
  live() {
    this.ready = true;
    this.started = true;
    this.failures = 0;
    $("recovering").hidden = true;
    terminal.options.disableStdin = !control?.ready;
    this.showState();
  }
  showState() {
    $("terminal-state").textContent =
      this.exitCode === undefined
        ? t("ready")
        : `${t("ended")} · ${this.exitCode}`;
  }
  message(message) {
    if (message.type === "snapshot") {
      terminal.reset();
      terminal.resize(message.cols, message.rows);
      this.ready = false;
      return;
    }
    if (message.type === "resize") {
      this.owned = message.owner === control?.id;
      terminal.resize(message.cols, message.rows);
      updateGeometry();
      if (!this.ready) {
        this.live();
        this.takeControl();
      }
    } else if (message.type === "child_exited") {
      this.exitCode = message.exit_code;
      this.showState();
    } else if (message.type === "theme") {
      const theme = { ...terminal.options.theme };
      for (const [key, target] of [
        ["background", "background"],
        ["foreground", "foreground"],
        ["cursor", "cursor"],
      ]) {
        if (/^[\da-f]{6}$/i.test(message[key]))
          theme[target] = "#" + message[key];
      }
      terminal.options.theme = theme;
    }
  }
  async takeControl() {
    if (!this.ready || this.stopped || !control?.ready || this.claiming) return;
    this.claiming = true;
    try {
      await control.call("session.own_block", {
        session_id: this.target.space,
        block_ids: [this.target.block],
        mode: "take",
      });
      if (this.stopped) return;
      this.owned = true;
      await fitTerminal();
    } catch {
      this.owned = false;
    } finally {
      this.claiming = false;
    }
  }
  async stop() {
    this.stopped = true;
    this.generation++;
    clearTimeout(this.timer);
    this.ws?.close(1000);
    await this.queue.catch(() => {});
    if (control?.ready)
      control
        .call("session.disown_block", {
          session_id: this.target.space,
          block_ids: [this.target.block],
        })
        .catch(() => {});
  }
}

function setupTerminal() {
  if (terminal) return;
  terminal = new window.Terminal({
    fontFamily: '"SFMono-Regular", Menlo, Consolas, monospace',
    fontSize,
    scrollback: 10000,
    allowProposedApi: true,
    allowTransparency: false,
    cursorBlink: true,
    convertEol: false,
    disableStdin: true,
    theme: {
      background: "#141621",
      foreground: "#dce0ee",
      cursor: "#c2b8ff",
      selectionBackground: "#c2b8ff40",
      black: "#252936",
      red: "#eaa4a0",
      green: "#9bc9b1",
      yellow: "#ddc28f",
      blue: "#a5bce8",
      magenta: "#c4aee5",
      cyan: "#91c6ce",
      white: "#dce0ee",
      brightBlack: "#70798c",
      brightRed: "#f2b7b0",
      brightGreen: "#b7d8be",
      brightYellow: "#e5d1a6",
      brightBlue: "#bed0f0",
      brightMagenta: "#dac5ef",
      brightCyan: "#b4d9dd",
      brightWhite: "#f2f2f7",
    },
  });
  fitAddon = new window.FitAddon.FitAddon();
  terminal.loadAddon(fitAddon);
  terminal.loadAddon(new window.Unicode11Addon.Unicode11Addon());
  terminal.unicode.activeVersion = "11";
  terminal.open($("terminal"));
  terminal.textarea.setAttribute(
    "aria-label",
    lang === "zh" ? "终端输入" : "Terminal input",
  );
  terminal.onData(queueInput);
  terminal.attachCustomKeyEventHandler((event) => {
    if (event.type !== "keydown") return true;
    const key = event.key.toLowerCase();
    if (
      (event.metaKey || (event.ctrlKey && event.shiftKey)) &&
      key === "c" &&
      terminal.hasSelection()
    ) {
      event.preventDefault();
      copySelection();
      return false;
    }
    if ((event.metaKey || (event.ctrlKey && event.shiftKey)) && key === "v")
      return false;
    if (event.metaKey && key === "a") {
      event.preventDefault();
      terminal.selectAll();
      return false;
    }
    return true;
  });
  $("terminal").addEventListener("pointerdown", () =>
    dataStream?.takeControl(),
  );
  new ResizeObserver(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => fitTerminal(), 120);
  }).observe($("terminal"));
}
function updateGeometry() {
  if (terminal)
    $("geometry").textContent = `${terminal.cols} × ${terminal.rows}`;
}
async function fitTerminal() {
  if (
    !terminal ||
    $("workspace").hidden ||
    !dataStream?.ready ||
    !dataStream.owned
  )
    return;
  const dims = fitAddon.proposeDimensions();
  if (!dims || !Number.isFinite(dims.cols) || !Number.isFinite(dims.rows))
    return;
  const cols = Math.min(1000, Math.max(2, dims.cols)),
    rows = Math.min(1000, Math.max(1, dims.rows));
  terminal.resize(cols, rows);
  updateGeometry();
  if (dataStream.lastSize === `${cols}:${rows}`) return;
  const stream = dataStream;
  try {
    await control.block("resize", stream.target, { columns: cols, rows });
    stream.lastSize = `${cols}:${rows}`;
  } catch {
    stream.lastSize = null;
  }
}

function queueInput(text) {
  if (!text || !control?.ready || !dataStream?.started || dataStream.stopped)
    return;
  if (inputBytes + text.length > 262144) {
    toast(t("largeInput"));
    return;
  }
  inputQueue.push({ text, target: { ...selected }, control });
  inputBytes += text.length;
  sendInput();
}
async function sendInput() {
  if (sending) return;
  sending = true;
  while (inputQueue.length) {
    const item = inputQueue.shift();
    inputBytes -= item.text.length;
    let rest = item.text;
    try {
      while (rest) {
        if (!item.control.ready) throw new Error("offline");
        const [chunk, next] = textChunk(rest);
        rest = next;
        await item.control.block("write", item.target, { data: chunk });
      }
    } catch {
      toast(t("lostInput"));
      inputQueue = [];
      inputBytes = 0;
      break;
    }
  }
  sending = false;
}

function button(text, className, action) {
  const node = document.createElement("button");
  node.type = "button";
  node.className = className;
  node.textContent = text;
  node.onclick = action;
  return node;
}
function renderNavigation() {
  $("spaces").replaceChildren();
  $("tabs").replaceChildren();
  $("panes").replaceChildren();
  $("space-count").textContent = views.length;
  for (const view of views) {
    const node = button(
      "",
      `space-button ${view.session_id === selected.space ? "active" : ""}`,
      () => select({ space: view.session_id }),
    );
    node.title = view.label || t("workspace");
    const dot = document.createElement("i");
    dot.className = "space-dot";
    dot.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.className = "space-name";
    label.textContent = view.label || t("workspace");
    const count = document.createElement("small");
    count.textContent = view.windows?.length || 0;
    node.append(dot, label, count);
    node.setAttribute(
      "aria-current",
      String(view.session_id === selected.space),
    );
    $("spaces").append(node);
  }
  const view = views.find((s) => s.session_id === selected.space);
  $("space-title").textContent = view?.label || t("workspace");
  document.title = view?.label ? `${view.label} — GoRex Web` : "GoRex Web";
  for (const [index, window] of (view?.windows || []).entries()) {
    const node = button(
      window.label || `${t("terminal")} ${index + 1}`,
      `tab-button ${window.window_id === selected.window ? "active" : ""}`,
      () => select({ space: selected.space, window: window.window_id }),
    );
    node.setAttribute("role", "tab");
    node.setAttribute(
      "aria-selected",
      String(window.window_id === selected.window),
    );
    node.title = node.textContent;
    $("tabs").append(node);
  }
  const window = view?.windows?.find((w) => w.window_id === selected.window);
  const panes = blocks(window);
  $("panes").hidden = panes.length < 2;
  for (const [index, pane] of panes.entries()) {
    const node = button(
      pane.label || `${t("terminal")} ${index + 1}`,
      pane.block_id === selected.block ? "active" : "",
      () =>
        select({
          space: selected.space,
          window: selected.window,
          block: pane.block_id,
        }),
    );
    node.setAttribute("aria-pressed", String(pane.block_id === selected.block));
    $("panes").append(node);
  }
}
async function select(preference) {
  const next = choose(views, preference),
    previous = selected;
  selected = next;
  save("gorex.selection", selected);
  renderNavigation();
  document.body.classList.remove("sidebar-open");
  $("sidebar-toggle").setAttribute("aria-expanded", "false");
  if (
    previous.space === next.space &&
    previous.block === next.block &&
    dataStream &&
    !dataStream.stopped
  ) {
    dataStream.open();
    return;
  }
  const generation = ++selectionGeneration;
  const old = dataStream;
  dataStream = null;
  if (old) await old.stop();
  if (generation !== selectionGeneration || !authenticated) return;
  $("empty").hidden = !!next.block;
  $("terminal").hidden = !next.block;
  $("recovering").hidden = !next.block;
  $("terminal-state").textContent = next.block ? t("connecting") : "";
  if (!next.block) {
    updateGeometry();
    return;
  }
  setupTerminal();
  terminal.options.disableStdin = true;
  dataStream = new TerminalData(next);
  dataStream.open();
}
function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 100);
}
async function refresh() {
  if (!control?.ready || !authenticated) return;
  if (refreshing) {
    refreshAgain = true;
    return;
  }
  refreshing = true;
  try {
    do {
      refreshAgain = false;
      const c = control,
        list = await c.call("session.list");
      const snapshot = await c.call("session.bootstrap", {
        event_scope: "session",
        attach_session_ids: list.sessions.map((s) => s.session_id),
      });
      if (control !== c || !c.ready) break;
      views = (snapshot.attached_sessions || []).map((s) => s.view);
      await select(selected);
    } while (refreshAgain);
  } catch {
  } finally {
    refreshing = false;
  }
}
function event(message) {
  const inner = unwrapEvent(message);
  if (!inner) return;
  if (
    inner.type === "block_owner_changed" &&
    dataStream &&
    inner.payload?.block_id === selected.block
  ) {
    dataStream.owned = inner.payload.owner === control.id;
    $("take-control").title = t(dataStream.owned ? "owning" : "following");
    if (dataStream.owned) fitTerminal();
  }
  if (
    [
      "session_created",
      "session_destroyed",
      "session_label_changed",
      "session_view_changed",
      "window_created",
      "window_closed",
      "window_label_changed",
      "block_created",
      "block_closed",
    ].includes(inner.type)
  )
    scheduleRefresh();
}

function retry() {
  if (stopped || !authenticated) return;
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(
    connect,
    Math.min(8000, 500 * 2 ** Math.min(retries++, 4)),
  );
}
async function connect() {
  if (stopped || connecting || control?.ready) return;
  connecting = true;
  connection(authenticated ? "reconnecting" : "connecting");
  try {
    metadata = await api("/api/web/session");
    authenticated = true;
    $("pair-panel").hidden = true;
    $("workspace").hidden = false;
    $("host-name").textContent = metadata.host || t("thisComputer");
    $("logout").hidden = metadata.transport !== "public";
    setupTerminal();
    const c = new Control(reconnectToken);
    control = c;
    c.onEvent = event;
    c.onClose = () => {
      if (control !== c || stopped) return;
      control = null;
      connection("offline");
      $("recovering").hidden = !selected.block;
      retry();
    };
    await c.open();
    reconnectToken = c.token;
    if (!c.features.includes("gorex.web")) throw new Error(t("updateRequired"));
    retries = 0;
    connection("connected");
    if (dataStream) {
      dataStream.lastSize = null;
      dataStream.takeControl();
    }
    await refresh();
  } catch (error) {
    if (error.status === 401 || error.status === 403) {
      await showPair(authenticated ? t("revoked") : "");
    } else {
      control?.close();
      control = null;
      connection("offline");
      retry();
    }
  } finally {
    connecting = false;
  }
}
async function showPair(error = "") {
  authenticated = false;
  reconnectToken = null;
  clearTimeout(reconnectTimer);
  control?.close();
  control = null;
  const old = dataStream;
  dataStream = null;
  selectionGeneration++;
  if (old) await old.stop();
  $("workspace").hidden = true;
  $("pair-panel").hidden = false;
  $("logout").hidden = true;
  $("pair-error").textContent = error;
  $("pair-error").hidden = !error;
  connection("offline");
}

async function newTerminal() {
  if (!control?.ready) return;
  $("new-terminal").disabled = $("empty-new").disabled = true;
  try {
    const layout = { block: { flavor: "com.superlogical.terminal.shell" } };
    if (selected.space) {
      const label = `${t("terminal")} ${(views.find((v) => v.session_id === selected.space)?.windows?.length || 0) + 1}`;
      const result = await control.call("session.new_window", {
        session_id: selected.space,
        window_label: label,
        layout,
        focus: true,
      });
      selected = { space: selected.space, window: result.window_id };
    } else {
      const result = await control.call("session.create", {
        label: t("workspace"),
        initial_windows: [{ window_label: `${t("terminal")} 1`, layout }],
      });
      selected = { space: result.session_id };
    }
    await refresh();
    terminal?.focus();
  } catch (error) {
    toast(`${t("newFailed")} ${error.message}`);
  } finally {
    $("new-terminal").disabled = $("empty-new").disabled = false;
  }
}
async function copySelection() {
  const text = terminal?.getSelection();
  if (!text) {
    toast(t("selectToCopy"));
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    toast(t("copied"));
  } catch {
    toast(t("manualCopy"));
  }
}

$("pair-form").onsubmit = async (event) => {
  event.preventDefault();
  $("pair-submit").disabled = true;
  $("pair-error").hidden = true;
  try {
    await api("/api/access/pair", {
      code: $("pair-code").value.trim(),
      name: $("device-name").value.trim(),
    });
    $("pair-code").value = "";
    stopped = false;
    await connect();
  } catch {
    $("pair-error").textContent = t("pairFailed");
    $("pair-error").hidden = false;
  } finally {
    $("pair-submit").disabled = false;
  }
};
$("logout").onclick = async () => {
  stopped = true;
  control?.close();
  clearTimeout(reconnectTimer);
  try {
    await api("/api/access/logout", {});
    await showPair();
  } catch (error) {
    if (error.status === 401) await showPair();
    else {
      stopped = false;
      toast(t("unavailable"));
      control = null;
      connect();
    }
  }
};
$("language").onclick = () => {
  lang = lang === "zh" ? "en" : "zh";
  save("gorex.language", lang);
  translate();
};
$("sidebar-toggle").onclick = () => {
  const open = document.body.classList.toggle("sidebar-open");
  $("sidebar-toggle").setAttribute("aria-expanded", String(open));
};
$("reconnect").onclick = () => {
  stopped = false;
  clearTimeout(reconnectTimer);
  connect();
};
$("new-terminal").onclick = $("empty-new").onclick = newTerminal;
$("take-control").onclick = () => {
  dataStream?.takeControl();
  terminal?.focus();
};
$("copy").onclick = copySelection;
$("interrupt").onclick = () => {
  queueInput("\x03");
  terminal?.focus();
};
$("paste").onclick = async () => {
  try {
    const text = await navigator.clipboard.readText();
    if (dataStream?.ready) {
      terminal.paste(text);
      terminal.focus();
    }
  } catch {
    terminal?.focus();
    toast(t("manualPaste"));
  }
};
$("bottom").onclick = () => {
  terminal?.scrollToBottom();
  terminal?.focus();
};
function setFont(change) {
  fontSize = Math.min(24, Math.max(10, fontSize + change));
  save("gorex.fontSize", fontSize);
  if (terminal) {
    terminal.options.fontSize = fontSize;
    fitTerminal();
  }
}
$("font-minus").onclick = () => setFont(-1);
$("font-plus").onclick = () => setFont(1);
window.addEventListener("online", () => {
  clearTimeout(reconnectTimer);
  connect();
  dataStream?.open();
});
window.addEventListener("pagehide", () => {
  stopped = true;
  clearTimeout(reconnectTimer);
  dataStream?.stop();
  control?.close();
});
window.addEventListener("pageshow", (event) => {
  if (event.persisted) {
    stopped = false;
    connect();
  }
});
window.addEventListener("keydown", (event) => {
  if (
    event.key === "Escape" &&
    document.body.classList.contains("sidebar-open")
  ) {
    document.body.classList.remove("sidebar-open");
    $("sidebar-toggle").setAttribute("aria-expanded", "false");
  }
});
$("device-name").value = t("browser");
if (location.hash.startsWith("#gorex-pair=")) {
  $("pair-code").value = location.hash.slice("#gorex-pair=".length);
  history.replaceState(null, "", location.pathname + location.search);
}
translate();
connect();
