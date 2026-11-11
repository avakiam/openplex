"use client";
import { RotateCcw } from "lucide-react";
import { useEffect, useState } from "react";

import { Button, Card, CardBody, CardHeader, ErrorBox, Field, Input, Select } from "@/components/ui";
import { api } from "@/lib/api";
import { keys, useAction, usePHPSettings } from "@/lib/hooks";
import { tk, useT } from "@/lib/i18n";
import type { Domain, PHPSettings } from "@/lib/types";

const labels: Record<string, string> = {
  memory_limit: tk("Memory per script"),
  upload_max_filesize: tk("Largest file upload"),
  post_max_size: tk("Largest form submission"),
  max_execution_time: tk("Script time limit (seconds)"),
  max_input_time: tk("Time to read a request (seconds)"),
  max_input_vars: tk("Form fields per request"),
  "session.gc_maxlifetime": tk("Session lifetime (seconds)"),
  display_errors: tk("Show errors on the pages"),
  allow_url_fopen: tk("Open remote URLs as files"),
  "date.timezone": tk("Time zone"),
  disable_functions: tk("Disabled functions"),
};

/** PHP settings of a PHP website; empty fields keep the default. */
export function PHPSettingsCard({ d, readOnly }: { d: Domain; readOnly: boolean }) {
  const { t } = useT();
  const { data } = usePHPSettings(d.id);
  const [values, setValues] = useState<Record<string, string>>({});
  useEffect(() => {
    if (data) setValues(data.settings);
  }, [data]);
  const save = useAction(
    (settings: Record<string, string>) =>
      api.put<PHPSettings>(`/domains/${d.id}/php`, { settings: Object.fromEntries(Object.entries(settings).filter(([, v]) => v.trim() !== "")) }),
    { success: t("PHP settings saved — restarting the website"), invalidate: [keys.phpSettings(d.id), keys.domain(d.id)] },
  );
  if (!data) return null;
  const set = (k: string, v: string) => setValues((s) => ({ ...s, [k]: v }));

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(values);
      }}
    >
      <Card>
        <CardHeader
          title={t("PHP settings")}
          description={t("Empty fields keep the default shown in grey. Saving restarts the website's container (a few seconds).")}
          action={
            !readOnly && (
              <Button type="button" size="sm" variant="outline" onClick={() => setValues({})}>
                <RotateCcw /> {t("Defaults")}
              </Button>
            )
          }
        />
        <CardBody className="grid gap-3 sm:grid-cols-2">
          {data.options.map((o) => {
            const label = (
              <>
                {t(labels[o.key] ?? o.key)} <code className="ml-1 font-mono text-[11px] font-normal text-muted-foreground">{o.key}</code>
              </>
            );
            const v = values[o.key] ?? "";
            if (o.kind === "bool") {
              return (
                <Field key={o.key} label={label}>
                  <Select disabled={readOnly} value={v} onChange={(e) => set(o.key, e.target.value)}>
                    <option value="">{t("Default ({value})", { value: o.default === "On" ? t("On") : t("Off") })}</option>
                    <option value="On">{t("On")}</option>
                    <option value="Off">{t("Off")}</option>
                  </Select>
                </Field>
              );
            }
            const hint =
              o.key === "memory_limit"
                ? t("Up to {max}M, the website's memory.", { max: String(data.memory_mb) })
                : o.kind === "size"
                  ? t("Up to {max}M. Use M or G, e.g. 128M or 1G.", { max: String(o.max) })
                  : o.kind === "int"
                    ? t("Between {min} and {max}.", { min: String(o.min), max: String(o.max) })
                    : o.kind === "timezone"
                      ? t("For example Europe/Madrid.")
                      : o.kind === "list"
                        ? t("Comma-separated, e.g. exec,shell_exec,system.")
                        : undefined;
            return (
              <Field key={o.key} label={label} hint={hint} className={o.kind === "list" ? "sm:col-span-2" : undefined}>
                <Input
                  disabled={readOnly}
                  className="font-mono"
                  inputMode={o.kind === "int" ? "numeric" : undefined}
                  placeholder={o.default || t("none")}
                  value={v}
                  onChange={(e) => set(o.key, e.target.value)}
                />
              </Field>
            );
          })}
          <div className="sm:col-span-2">
            <ErrorBox error={save.error} />
          </div>
          {!readOnly && (
            <div className="flex justify-end sm:col-span-2">
              <Button type="submit" loading={save.isPending}>
                {t("Save PHP settings")}
              </Button>
            </div>
          )}
        </CardBody>
      </Card>
    </form>
  );
}
