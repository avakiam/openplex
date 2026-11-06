"use client";
import Editor, { loader } from "@monaco-editor/react";
import { useEffect, useState } from "react";

import { Spinner } from "./ui";

// Serve Monaco from the panel itself (copied to /monaco by scripts/copy-monaco.mjs).
loader.config({ paths: { vs: "/monaco/vs" } });

const langByExt: Record<string, string> = {
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", tsx: "typescript", json: "json", php: "php", py: "python", go: "go",
  html: "html", htm: "html", css: "css", scss: "scss", less: "less", md: "markdown",
  yml: "yaml", yaml: "yaml", xml: "xml", svg: "xml", sh: "shell", bash: "shell",
  sql: "sql", ini: "ini", conf: "ini", env: "ini", toml: "ini", dockerfile: "dockerfile",
  rb: "ruby", java: "java", rs: "rust", vue: "html", twig: "twig",
};

export function languageFor(path: string) {
  const base = path.split("/").pop()?.toLowerCase() ?? "";
  if (base === "dockerfile") return "dockerfile";
  if (base === ".htaccess" || base.startsWith(".env")) return "ini";
  return langByExt[base.split(".").pop() ?? ""] ?? "plaintext";
}

function useDarkClass() {
  const [dark, setDark] = useState(false);
  useEffect(() => {
    const el = document.documentElement;
    const update = () => setDark(el.classList.contains("dark"));
    update();
    const mo = new MutationObserver(update);
    mo.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => mo.disconnect();
  }, []);
  return dark;
}

export function CodeEditor({
  path,
  value,
  onChange,
  onSave,
}: {
  path: string;
  value: string;
  onChange: (v: string) => void;
  onSave: () => void;
}) {
  const dark = useDarkClass();
  return (
    <Editor
      height="100%"
      path={path}
      language={languageFor(path)}
      value={value}
      theme={dark ? "vs-dark" : "light"}
      loading={<Spinner />}
      onChange={(v) => onChange(v ?? "")}
      onMount={(editor, monaco) => {
        editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, onSave);
        editor.focus();
      }}
      options={{
        fontSize: 13,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        automaticLayout: true,
        tabSize: 2,
        renderWhitespace: "selection",
      }}
    />
  );
}
