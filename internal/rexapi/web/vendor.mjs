import { readFile, writeFile, mkdir, copyFile } from "node:fs/promises";
await mkdir(new URL("./vendor/", import.meta.url), { recursive: true });
for (const [pkg, source, destination] of [
  ["xterm", "lib/xterm.js", "xterm.js"],
  ["xterm", "css/xterm.css", "xterm.css"],
  ["addon-fit", "lib/addon-fit.js", "addon-fit.js"],
  ["addon-unicode11", "lib/addon-unicode11.js", "addon-unicode11.js"],
]) {
  const contents = await readFile(
    new URL(`./node_modules/@xterm/${pkg}/${source}`, import.meta.url),
    "utf8",
  );
  await writeFile(
    new URL(`./vendor/${destination}`, import.meta.url),
    contents.replace(/\/\/# sourceMappingURL=.*$/m, ""),
  );
}
for (const pkg of ["xterm", "addon-fit", "addon-unicode11"]) {
  await copyFile(
    new URL(`./node_modules/@xterm/${pkg}/LICENSE`, import.meta.url),
    new URL(`./vendor/LICENSE.${pkg}.txt`, import.meta.url),
  );
}
