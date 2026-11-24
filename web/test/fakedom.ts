// A small stand-in for the DOM, for the toast host's tests (the web tests run in Node, with no DOM).
type Handler = (e: any) => void;

export class FakeEl {
  children: FakeEl[] = [];
  parent: FakeEl | undefined;
  attrs = new Map<string, string>();
  props: Record<string, unknown> = {};
  listeners = new Map<string, Handler[]>();
  className = "";
  hidden = false;
  title = "";
  type = "";
  textContent = "";
  style = { setProperty: (_k: string, _v: string) => {} };
  constructor(readonly tag: string) {}
  append(...n: FakeEl[]) {
    for (const c of n) {
      c.parent = this;
      this.children.push(c);
    }
  }
  replaceWith(n: FakeEl) {
    const p = this.parent!;
    p.children[p.children.indexOf(this)] = n;
    n.parent = p;
    this.parent = undefined;
  }
  contains(n: FakeEl | null): boolean {
    for (let e: FakeEl | undefined = n ?? undefined; e; e = e.parent) if (e === this) return true;
    return false;
  }
  setAttribute(k: string, v: string) {
    this.attrs.set(k, v);
  }
  removeAttribute(k: string) {
    this.attrs.delete(k);
  }
  getAttribute(k: string) {
    return this.attrs.get(k) ?? null;
  }
  addEventListener(type: string, f: Handler) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), f]);
  }
  fire(type: string, e: object = {}) {
    for (const f of this.listeners.get(type) ?? []) f({ type, stopPropagation() {}, ...e });
  }
  find(cls: string): FakeEl | undefined {
    if (this.className.split(" ").includes(cls)) return this;
    for (const c of this.children) {
      const r = c.find(cls);
      if (r) return r;
    }
    return undefined;
  }
}

export class FakeDoc {
  body = new FakeEl("body");
  visibilityState = "visible";
  activeElement: FakeEl | null = null;
  listeners = new Map<string, Handler[]>();
  createElement(tag: string) {
    return new FakeEl(tag);
  }
  addEventListener(type: string, f: Handler) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), f]);
  }
  fire(type: string) {
    for (const f of this.listeners.get(type) ?? []) f({ type });
  }
}
