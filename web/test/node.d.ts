// Minimal ambient declarations for Node built-in modules used by web/test/*.test.ts,
// keeping `tsc --noEmit` dependency-free without @types/node.

declare const process: {
  cwd(): string;
  env: Record<string, string | undefined>;
};

declare const Buffer: {
  from(data: ArrayLike<number> | ArrayBufferLike | string, encoding?: string): Uint8Array & { toString(encoding?: string): string };
  alloc(size: number, fill?: number): Uint8Array & { toString(encoding?: string): string };
};

declare module "node:test" {
  export interface TestContext {
    mock: {
      timers: {
        enable(options?: { apis?: string[] }): void;
        tick(ms: number): void;
        reset(): void;
      };
    };
  }
  export function test(name: string, fn: (t: TestContext) => void | Promise<void>): Promise<void>;
  export default test;
}

declare module "node:assert/strict" {
  const assert: {
    (value: unknown, message?: string | Error): asserts value;
    ok(value: unknown, message?: string | Error): asserts value;
    equal<T>(actual: T, expected: T, message?: string | Error): void;
    strictEqual<T>(actual: T, expected: T, message?: string | Error): void;
    notEqual(actual: unknown, expected: unknown, message?: string | Error): void;
    deepEqual<T>(actual: T, expected: T, message?: string | Error): void;
    deepStrictEqual<T>(actual: T, expected: T, message?: string | Error): void;
    match(value: string, regexp: RegExp, message?: string | Error): void;
    doesNotMatch(value: string, regexp: RegExp, message?: string | Error): void;
    throws(block: () => unknown, error?: RegExp | ((err: unknown) => boolean) | Error, message?: string): void;
    doesNotThrow(block: () => unknown, message?: string): void;
    rejects(
      block: Promise<unknown> | (() => Promise<unknown>),
      error?: RegExp | ((err: unknown) => boolean) | Error,
      message?: string,
    ): Promise<void>;
    fail(message?: string | Error): never;
  };
  export default assert;
}

declare module "node:fs" {
  export function readFileSync(path: string, encoding: "utf8" | "utf-8"): string;
  export function readFileSync(path: string): Uint8Array;
  export function readdirSync(path: string): string[];
  export function mkdtempSync(prefix: string): string;
  export function mkdirSync(path: string, options?: { recursive?: boolean }): void;
  export function writeFileSync(path: string, data: string): void;
  export function rmSync(path: string, options?: { recursive?: boolean; force?: boolean }): void;
}

declare module "node:os" {
  export function tmpdir(): string;
}

declare module "node:path" {
  export function join(...paths: string[]): string;
  export function dirname(path: string): string;
}

declare module "node:url" {
  export function fileURLToPath(url: string | URL): string;
  export function pathToFileURL(path: string): URL;
}
