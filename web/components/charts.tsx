"use client";
// Time-series line/area chart in plain SVG. Follows the dataviz method:
// thin 2px lines, recessive grid, one y-axis, legend for >= 2 series with
// direct end labels, crosshair + tooltip on hover, table fallback via <details>.
import { useEffect, useMemo, useRef, useState } from "react";

import { getLocale, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

export interface Series {
  key: string;
  label: string;
  color: string; // CSS color, e.g. "var(--chart-1)"
  values: number[];
}

interface Props {
  times: number[]; // epoch ms, same length as each series' values
  series: Series[];
  format: (v: number) => string;
  max?: number; // fixed y max (e.g. 100 for %); otherwise auto
  height?: number;
  title: string;
  className?: string;
}

const PAD = { l: 44, r: 12, t: 10, b: 22 };

function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

export function TimeChart({ times, series, format, max, height = 180, title, className }: Props) {
  const { t } = useT();
  const ref = useRef<SVGSVGElement>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  // Draw in real pixels so text and strokes keep their size at any card width.
  const [W, setW] = useState(600);
  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(240, Math.round(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const H = height;
  const n = times.length;

  const yMax = useMemo(() => {
    if (max !== undefined) return max;
    const m = Math.max(0, ...series.flatMap((s) => s.values));
    return niceMax(m * 1.1);
  }, [series, max]);

  const x = (i: number) => PAD.l + (n <= 1 ? 0 : (i / (n - 1)) * (W - PAD.l - PAD.r));
  const y = (v: number) => PAD.t + (1 - Math.min(v, yMax) / yMax) * (H - PAD.t - PAD.b);
  const ticks = [0, 0.5, 1].map((f) => f * yMax);

  const onMove = (e: React.PointerEvent) => {
    const svg = ref.current;
    if (!svg || n === 0) return;
    const r = svg.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    const i = Math.round(((px - PAD.l) / (W - PAD.l - PAD.r)) * (n - 1));
    setHover(Math.max(0, Math.min(n - 1, i)));
  };

  const fmtTime = (t: number) => new Date(t).toLocaleTimeString(getLocale(), { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  const hi = hover ?? n - 1;

  return (
    <div ref={wrap} className={cn("relative", className)}>
      {series.length > 1 && (
        <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-hidden>
          {series.map((s) => (
            <span key={s.key} className="inline-flex items-center gap-1.5">
              <span className="h-0.5 w-3 rounded" style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
        </div>
      )}
      <svg
        ref={ref}
        viewBox={`0 0 ${W} ${H}`}
        width={W}
        height={H}
        className="block max-w-full touch-none select-none"
        role="img"
        aria-label={`${title}: ${series.map((s) => `${s.label} ${format(s.values[n - 1] ?? 0)}`).join(", ")}`}
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
      >
        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.l} x2={W - PAD.r} y1={y(t)} y2={y(t)} stroke="var(--chart-grid)" strokeWidth={1} />
            <text x={PAD.l - 6} y={y(t)} dy="0.32em" textAnchor="end" fontSize={10} fill="var(--muted-foreground)">
              {format(t)}
            </text>
          </g>
        ))}
        {n > 1 && (
          <>
            <text x={PAD.l} y={H - 6} fontSize={10} fill="var(--muted-foreground)">
              {fmtTime(times[0])}
            </text>
            <text x={W - PAD.r} y={H - 6} fontSize={10} textAnchor="end" fill="var(--muted-foreground)">
              {fmtTime(times[n - 1])}
            </text>
          </>
        )}
        {n > 1 &&
          series.map((s) => {
            const pts = s.values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
            return (
              <g key={s.key}>
                {series.length === 1 && (
                  <polygon
                    points={`${x(0)},${y(0)} ${pts} ${x(n - 1)},${y(0)}`}
                    fill={s.color}
                    opacity={0.1}
                  />
                )}
                <polyline points={pts} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
              </g>
            );
          })}
        {hover !== null && n > 0 && (
          <g pointerEvents="none">
            <line x1={x(hover)} x2={x(hover)} y1={PAD.t} y2={H - PAD.b} stroke="var(--muted-foreground)" strokeWidth={1} strokeDasharray="3 3" />
            {series.map((s) => (
              <circle key={s.key} cx={x(hover)} cy={y(s.values[hover] ?? 0)} r={4} fill={s.color} stroke="var(--chart-surface)" strokeWidth={2} />
            ))}
          </g>
        )}
      </svg>
      {n > 0 && (
        <div
          className={cn(
            "pointer-events-none absolute top-0 rounded-md border bg-card px-2.5 py-1.5 text-xs shadow-md transition-opacity",
            hover === null ? "opacity-0" : "opacity-100",
          )}
          style={{ left: `clamp(0px, calc(${(x(hi) / W) * 100}% - 70px), calc(100% - 150px))` }}
        >
          <div className="mb-0.5 text-muted-foreground">{fmtTime(times[hi])}</div>
          {series.map((s) => (
            <div key={s.key} className="flex items-center gap-2">
              <span className="size-2 rounded-full" style={{ background: s.color }} />
              <span className="text-muted-foreground">{s.label}</span>
              <span className="tabular ml-auto font-medium">{format(s.values[hi] ?? 0)}</span>
            </div>
          ))}
        </div>
      )}
      <details className="mt-1 text-xs text-muted-foreground">
        <summary className="cursor-pointer select-none">{t("Show data table")}</summary>
        <div className="mt-2 max-h-48 overflow-y-auto">
          <table className="w-full">
            <thead>
              <tr>
                <th className="py-1 text-left font-medium">{t("Time")}</th>
                {series.map((s) => (
                  <th key={s.key} className="py-1 text-right font-medium">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="tabular">
              {times
                .map((t, i) => ({ t, i }))
                .reverse()
                .map(({ t, i }) => (
                  <tr key={t}>
                    <td className="py-0.5">{fmtTime(t)}</td>
                    {series.map((s) => (
                      <td key={s.key} className="py-0.5 text-right">
                        {format(s.values[i] ?? 0)}
                      </td>
                    ))}
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

/** Horizontal usage meter (used/total) for stat tiles. */
export function Meter({ value, max, className }: { value: number; max: number; className?: string }) {
  const p = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)} role="meter" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full" style={{ width: `${p}%`, background: "var(--chart-1)" }} />
    </div>
  );
}
