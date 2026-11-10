// Fails when a string passed to t("…") has no Spanish or Catalan translation,
// when a catalogue entry leaves out a {placeholder}, or when two area files
// translate the same key differently. Runs before every build.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const dirs = ["app", "components", "lib"];
const call = /\btk?\(\s*(["'`])((?:\\.|(?!\1)[^\\])*)\1/g;

const files = [];
const walk = (d) => {
  for (const n of readdirSync(d)) {
    const p = join(d, n);
    if (statSync(p).isDirectory()) walk(p);
    else if (/\.(tsx?|mjs)$/.test(n)) files.push(p);
  }
};
dirs.forEach((d) => walk(join(root, d)));

const used = new Map(); // key -> first file
const problems = [];
for (const f of files) {
  const src = readFileSync(f, "utf8");
  for (const m of src.matchAll(call)) {
    if (m[1] === "`" && m[2].includes("${")) {
      problems.push(`${f.slice(root.length)}: t() with an interpolated template literal — use {placeholders}: ${m[2]}`);
      continue;
    }
    const key = m[2].replace(/\\(["'`\\])/g, "$1");
    if (!used.has(key)) used.set(key, f.slice(root.length));
  }
}

const locales = ["es", "ca"];
const placeholders = (s) => new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]));
for (const l of locales) {
  const dir = join(root, "locales", l);
  const merged = {};
  for (const n of readdirSync(dir)) {
    const cat = JSON.parse(readFileSync(join(dir, n), "utf8"));
    for (const [k, v] of Object.entries(cat)) {
      if (k in merged && JSON.stringify(merged[k]) !== JSON.stringify(v)) problems.push(`${l}/${n}: "${k}" conflicts with another area file`);
      merged[k] = v;
      const want = placeholders(k);
      for (const form of typeof v === "string" ? [v] : Object.values(v)) {
        for (const p of want) if (p !== "count" && !placeholders(form).has(p)) problems.push(`${l}/${n}: "${k}" is missing {${p}}`);
      }
    }
  }
  for (const [k, f] of used) if (!(k in merged)) problems.push(`${l}: missing translation for "${k}" (${f})`);
}

if (problems.length) {
  console.error(problems.join("\n"));
  console.error(`\ni18n check failed: ${problems.length} problem(s).`);
  process.exit(1);
}
console.log(`i18n check passed: ${used.size} strings × ${locales.length} languages.`);
