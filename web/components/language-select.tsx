"use client";
import { Check, Languages } from "lucide-react";

import { LOCALES, browserLocale, useT, type LocalePref } from "@/lib/i18n";

import { Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger } from "./ui";

/** Compact language switcher; "Automatic" follows the browser's language. */
export function LanguageSelect() {
  const { t, locale, pref, setPref } = useT();
  const auto = LOCALES.find((l) => l.code === browserLocale())!.name;
  const item = (p: LocalePref, label: string) => (
    <DropdownItem key={p || "auto"} onSelect={() => setPref(p)}>
      <Check className={pref === p ? "opacity-100" : "opacity-0"} /> {label}
    </DropdownItem>
  );
  return (
    <Dropdown>
      <DropdownTrigger
        className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label={t("Language")}
        title={t("Language")}
      >
        <Languages className="size-4" />
        <span className="uppercase">{locale}</span>
      </DropdownTrigger>
      <DropdownContent align="end">
        {item("", t("Automatic ({language})", { language: auto }))}
        <DropdownSeparator />
        {LOCALES.map((l) => item(l.code, l.name))}
      </DropdownContent>
    </Dropdown>
  );
}
