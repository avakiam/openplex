// Copies Monaco's prebuilt AMD bundle into public/ so the editor loads from
// the panel itself (no CDN): the binary must work on air-gapped servers.
//
// What the panel never loads is left out (it is embedded in openplexd):
// - nls/: the editor's own UI translations, only fetched when a UI language
//   is configured (the panel keeps the editor in English);
// - language/*/*.worker.js: older copies of the workers, which the editor
//   loads from assets/ instead;
// - assets/ts.worker-*.js: the TypeScript/JavaScript language service (about
//   7 MB). code-editor.tsx turns its features off, so it is never requested;
//   JavaScript and TypeScript files keep their syntax highlighting.
import { cpSync, existsSync, readdirSync, rmSync, statSync } from "node:fs";
import { join } from "node:path";

const src = join(process.cwd(), "node_modules", "monaco-editor", "min", "vs");
const dst = join(process.cwd(), "public", "monaco", "vs");
if (!existsSync(src)) {
  console.error("monaco-editor/min/vs not found");
  process.exit(1);
}

const size = (p) => (statSync(p).isDirectory() ? readdirSync(p).reduce((n, e) => n + size(join(p, e)), 0) : statSync(p).size);

rmSync(dst, { recursive: true, force: true });
cpSync(src, dst, { recursive: true });
const before = size(dst);

const drop = [join(dst, "nls")];
for (const lang of readdirSync(join(dst, "language"))) {
  for (const f of readdirSync(join(dst, "language", lang))) {
    if (f.endsWith(".worker.js")) drop.push(join(dst, "language", lang, f));
  }
}
const tsWorkers = readdirSync(join(dst, "assets")).filter((f) => /^ts\.worker-.*\.js$/.test(f));
if (tsWorkers.length !== 1) {
  console.error(`expected one assets/ts.worker-*.js, found ${tsWorkers.length}: check scripts/copy-monaco.mjs against this Monaco version`);
  process.exit(1);
}
drop.push(join(dst, "assets", tsWorkers[0]));
for (const p of drop) rmSync(p, { recursive: true, force: true });

const mb = (n) => (n / 1048576).toFixed(1) + " MB";
console.log(`monaco copied to public/monaco/vs (${mb(size(dst))}, ${mb(before - size(dst))} left out)`);
