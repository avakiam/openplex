// Human-readable descriptions of cron schedules. Only common shapes are
// described; anything else is "Custom schedule" (the raw expression is shown
// next to it anyway).
import { getLocale, t, tk } from "./i18n";

/** Schedule presets offered in the task dialog; translate labels with t() at render time. */
export const CRON_PRESETS: { label: string; expr: string }[] = [
  { label: tk("Every minute"), expr: "* * * * *" },
  { label: tk("Every 5 minutes"), expr: "*/5 * * * *" },
  { label: tk("Every 15 minutes"), expr: "*/15 * * * *" },
  { label: tk("Hourly"), expr: "0 * * * *" },
  { label: tk("Daily at 03:00"), expr: "0 3 * * *" },
  { label: tk("Weekly on Monday at 03:00"), expr: "0 3 * * 1" },
  { label: tk("Monthly on the 1st at 03:00"), expr: "0 3 1 * *" },
];

const num = (s: string, min: number, max: number): number | null => {
  if (!/^\d{1,2}$/.test(s)) return null;
  const n = Number(s);
  return n >= min && n <= max ? n : null;
};

const hhmm = (h: number, m: number) => `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;

/** Localised weekday name; 0 and 7 are Sunday. */
export function weekdayName(d: number) {
  // 1 January 2023 was a Sunday.
  return new Intl.DateTimeFormat(getLocale(), { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + (d % 7))));
}

/** Describes a cron expression in the current language. */
export function describeSchedule(expr: string): string {
  const e = expr.trim().toLowerCase();
  switch (e) {
    case "@hourly":
      return t("Every hour, on the hour");
    case "@daily":
    case "@midnight":
      return t("Every day at {time}", { time: "00:00" });
    case "@weekly":
      return t("Every week on {weekday} at {time}", { weekday: weekdayName(0), time: "00:00" });
    case "@monthly":
      return t("Every month on day {day} at {time}", { day: 1, time: "00:00" });
    case "@yearly":
    case "@annually":
      return t("Every year on 1 January at {time}", { time: "00:00" });
  }
  const f = e.split(/\s+/);
  if (f.length !== 5) return t("Custom schedule");
  const [mi, ho, dom, mon, dow] = f;
  if (mon !== "*") return t("Custom schedule");

  // Minute-level shapes: "* * * * *", "*/N * * * *", "M * * * *".
  if (ho === "*" && dom === "*" && dow === "*") {
    if (mi === "*" || mi === "*/1") return t("Every minute");
    const step = /^\*\/(\d{1,2})$/.exec(mi);
    if (step) {
      const n = num(step[1], 1, 59);
      return n === null ? t("Custom schedule") : t("Every {count} minutes", { count: n });
    }
    const m = num(mi, 0, 59);
    if (m === null) return t("Custom schedule");
    return m === 0 ? t("Every hour, on the hour") : t("Every hour at minute {minute}", { minute: m });
  }

  const m = num(mi, 0, 59);
  const h = num(ho, 0, 23);
  if (m === null || h === null) return t("Custom schedule");
  const time = hhmm(h, m);

  if (dom === "*" && dow === "*") return t("Every day at {time}", { time });
  if (dom === "*") {
    if (dow === "1-5") return t("Every weekday at {time}", { time });
    const d = num(dow, 0, 7);
    if (d !== null) return t("Every week on {weekday} at {time}", { weekday: weekdayName(d), time });
    return t("Custom schedule");
  }
  if (dow === "*") {
    const day = num(dom, 1, 31);
    if (day !== null) return t("Every month on day {day} at {time}", { day, time });
  }
  return t("Custom schedule");
}
