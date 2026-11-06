// Copies Monaco's prebuilt AMD bundle into public/ so the editor loads from
// the panel itself (no CDN): the binary must work on air-gapped servers.
import { cpSync, existsSync, rmSync } from "node:fs";
import { join } from "node:path";



const src = join(process.cwd(), "node_modules", "monaco-editor", "min", "vs");
const dst = join(process.cwd(), "public", "monaco", "vs");
if (!existsSync(src)) {
  console.error("monaco-editor/min/vs not found");
  process.exit(1);
}
rmSync(dst, { recursive: true, force: true });
cpSync(src, dst, { recursive: true });
console.log("monaco copied to public/monaco/vs");
