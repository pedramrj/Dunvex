import { useId, useMemo, useRef, useState } from "react";
import { Download, RotateCcw } from "lucide-react";
import { bytes, cx, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";

type Pt = { t: number; up: number; down: number };

/** Smooth path through points. */
function smooth(pts: [number, number][]) {
  if (pts.length < 2) return "";
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 1; i < pts.length; i++) {
    const [x0, y0] = pts[i - 1];
    const [x1, y1] = pts[i];
    const cx2 = (x0 + x1) / 2;
    d += ` C${cx2},${y0} ${cx2},${y1} ${x1},${y1}`;
  }
  return d;
}

export interface Series {
  key: string;
  label: string;
  color: string;
  dashed?: boolean;
}

/**
 * Stacked area chart: hover read-out, series toggles in the legend,
 * drag to zoom (double-click or ↺ to reset), CSV export.
 */
export function AreaChart({
  data,
  series,
  height = 220,
  format,
  label,
  showAxis = true,
  exportName,
  legend = true,
}: {
  data: { x: number; v: number[] }[];
  series: Series[];
  height?: number;
  format: (v: number) => string;
  label: (x: number) => string;
  showAxis?: boolean;
  exportName?: string;
  legend?: boolean;
}) {
  const { t } = useI18n();
  const id = useId().replace(/:/g, "");
  const ref = useRef<SVGSVGElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const [off, setOff] = useState<Set<number>>(new Set());
  const [zoom, setZoom] = useState<[number, number] | null>(null);
  const [drag, setDrag] = useState<[number, number] | null>(null);

  const view = zoom ? data.slice(zoom[0], zoom[1] + 1) : data;
  const W = 800;
  const H = height;
  const top = 12;
  const bottom = showAxis ? 22 : 4;
  const vis = series.map((_, i) => !off.has(i));
  const stackAt = (d: { v: number[] }, upto: number) => d.v.reduce((s, v, i) => (i <= upto && vis[i] ? s + v : s), 0);
  const max = Math.max(1, ...view.map((d) => stackAt(d, series.length - 1))) * 1.12;
  const x = (i: number) => (view.length <= 1 ? W / 2 : (i / (view.length - 1)) * W);
  const y = (v: number) => top + (H - top - bottom) * (1 - v / max);
  const base = H - bottom;
  const idx = (clientX: number) => {
    const r = ref.current?.getBoundingClientRect();
    if (!r || view.length === 0) return 0;
    return Math.max(0, Math.min(view.length - 1, Math.round(((clientX - r.left) / r.width) * (view.length - 1))));
  };
  const h = hover !== null ? view[hover] : null;

  const exportCSV = () => {
    const rows = [["time", ...series.map((s) => s.label)].join(","), ...data.map((d) => [new Date(d.x * 1000).toISOString(), ...d.v].join(","))];
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([rows.join("\n")], { type: "text/csv" }));
    a.download = (exportName ?? "dunvex") + ".csv";
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };

  return (
    <div className="relative">
      {legend && (
        <div className="flex flex-wrap items-center gap-2 mb-3">
          {series.map((s, i) => (
            <button key={s.key} onClick={() => setOff((o) => { const n = new Set(o); n.has(i) ? n.delete(i) : n.add(i); return n; })} aria-pressed={!off.has(i)}
              className={cx("inline-flex items-center gap-2 h-7 px-2.5 rounded-lg text-[12px] border transition-[opacity,background-color] duration-150", off.has(i) ? "opacity-45 border-hair" : "border-hair bg-fg/[.03]")}>
              <i className="h-0.5 w-3.5 rounded-full" style={{ background: s.dashed ? `repeating-linear-gradient(90deg, ${s.color} 0 4px, transparent 4px 7px)` : s.color }} />
              {s.label}
            </button>
          ))}
          <span className="ms-auto flex items-center gap-1">
            {zoom && <button onClick={() => setZoom(null)} className="inline-flex items-center gap-1 h-7 px-2 rounded-lg text-[12px] text-ink2 hover:bg-fg/[.05]"><RotateCcw className="size-3.5" />{t("resetZoom")}</button>}
            {exportName && <button onClick={exportCSV} className="inline-flex items-center gap-1 h-7 px-2 rounded-lg text-[12px] text-ink2 hover:bg-fg/[.05]" aria-label={t("export")}><Download className="size-3.5" />CSV</button>}
          </span>
        </div>
      )}
      <div className="relative" dir="ltr">
        <svg ref={ref} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="w-full block touch-none select-none" style={{ height }}
          onPointerMove={(e) => { const i = idx(e.clientX); setHover(i); if (drag) setDrag([drag[0], i]); }}
          onPointerLeave={() => { setHover(null); setDrag(null); }}
          onPointerDown={(e) => setDrag([idx(e.clientX), idx(e.clientX)])}
          onPointerUp={() => { if (drag && Math.abs(drag[1] - drag[0]) >= 2) { const a = Math.min(...drag), b = Math.max(...drag); const o = zoom?.[0] ?? 0; setZoom([o + a, o + b]); } setDrag(null); }}
          onDoubleClick={() => setZoom(null)} role="img" aria-label="chart">
          <defs>
            {series.map((s, i) => (
              <linearGradient key={s.key} id={`g${id}${i}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.42} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          {[0.25, 0.5, 0.75, 1].map((g) => (
            <line key={g} x1={0} x2={W} y1={y((max / 1.12) * g)} y2={y((max / 1.12) * g)} stroke="var(--border)" strokeDasharray="3 5" vectorEffect="non-scaling-stroke" />
          ))}
          {series.map((s, si) => {
            if (!vis[si]) return null;
            const pts = view.map((d, i) => [x(i), y(stackAt(d, si))] as [number, number]);
            const line = smooth(pts);
            if (!line) return null;
            return (
              <g key={s.key}>
                <path d={`${line} L${W},${base} L0,${base} Z`} fill={`url(#g${id}${si})`} />
                <path d={line} fill="none" stroke={s.color} strokeWidth={s.dashed ? 1.6 : 2.1} strokeDasharray={s.dashed ? "5 4" : undefined} vectorEffect="non-scaling-stroke" className={s.dashed ? "" : "draw"} style={{ ["--len" as any]: 2400 }} />
              </g>
            );
          })}
          {drag && Math.abs(drag[1] - drag[0]) >= 1 && (
            <rect x={x(Math.min(...drag))} y={top} width={Math.abs(x(drag[1]) - x(drag[0]))} height={base - top} fill="color-mix(in oklab, var(--primary) 14%, transparent)" stroke="var(--primary)" strokeOpacity={0.4} vectorEffect="non-scaling-stroke" />
          )}
          {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={top} y2={base} stroke="var(--border2)" vectorEffect="non-scaling-stroke" />}
        </svg>
        {hover !== null && h && (
          <>
            <span className="pointer-events-none absolute size-2.5 rounded-full -translate-x-1/2 -translate-y-1/2 ring-4" style={{ left: `${(x(hover) / W) * 100}%`, top: `${(y(stackAt(h, series.length - 1)) / H) * 100}%`, background: series[0].color, ["--tw-ring-color" as any]: `color-mix(in oklab, ${series[0].color} 30%, transparent)` }} />
            <div className="pointer-events-none absolute top-1 z-10 rounded-xl glass-strong px-3 py-2 text-[12px] min-w-36" style={{ left: `${Math.min(76, Math.max(2, (x(hover) / W) * 100 - 8))}%` }}>
              <div className="text-ink3 mb-1">{label(h.x)}</div>
              {series.map((s, i) => vis[i] && (
                <div key={s.key} className="flex items-center gap-2 num"><i className="size-2 rounded-full" style={{ background: s.color }} /><span className="text-ink2">{s.label}</span><span className="ms-auto ps-3">{format(h.v[i])}</span></div>
              ))}
            </div>
          </>
        )}
        {showAxis && view.length > 1 && (
          <div className="flex justify-between text-[11px] text-ink3 -mt-4 px-0.5 num pointer-events-none">
            <span>{label(view[0].x)}</span>
            <span>{label(view[Math.floor(view.length / 2)].x)}</span>
            <span>{label(view[view.length - 1].x)}</span>
          </div>
        )}
      </div>
    </div>
  );
}

/** Group hourly points into daily buckets (or keep hourly for 1 day). */
export function bucket(series: Pt[], days: number) {
  const out: { x: number; v: number[] }[] = [];
  if (days <= 2) {
    const nowH = Math.floor(Date.now() / 3600000) * 3600;
    const start = nowH - (days * 24 - 1) * 3600;
    const m = new Map(series.map((p) => [p.t, p]));
    for (let t = start; t <= nowH; t += 3600) {
      const p = m.get(t);
      out.push({ x: t, v: [p?.down ?? 0, p?.up ?? 0] });
    }
    return out;
  }
  const now = new Date();
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() / 1000 - (days - 1) * 86400;
  for (let i = 0; i < days; i++) out.push({ x: start + i * 86400, v: [0, 0] });
  for (const p of series) {
    const d = new Date(p.t * 1000);
    const key = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / 1000;
    const idx = Math.round((key - start) / 86400);
    if (idx >= 0 && idx < days) {
      out[idx].v[0] += p.down;
      out[idx].v[1] += p.up;
    }
  }
  return out;
}

/** Usage over a period with summary figures. */
export function UsageChart({ series, days = 30, height = 230, exportName }: { series: Pt[]; days?: number; height?: number; exportName?: string }) {
  const { lang, t } = useI18n();
  const data = useMemo(() => bucket(series, days), [series, days]);
  const fmt = useMemo(
    () => new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", days <= 2 ? { hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric" }),
    [lang, days],
  );
  const total = data.reduce((s, d) => s + d.v[0] + d.v[1], 0);
  const peak = data.reduce((m, d) => Math.max(m, d.v[0] + d.v[1]), 0);
  const active = data.filter((d) => d.v[0] + d.v[1] > 0).length || 1;
  return (
    <div>
      <div className="flex flex-wrap gap-x-7 gap-y-2 mb-4">
        <Fig label={t("total")} value={bytes(total, lang)} />
        <Fig label={days <= 2 ? t("hourlyAvg") : t("dailyAvg")} value={bytes(total / active, lang)} />
        <Fig label={days <= 2 ? t("peakHour") : t("peakDay")} value={bytes(peak, lang)} />
      </div>
      <AreaChart data={data} height={height} exportName={exportName} format={(v) => bytes(v, lang)} label={(x) => fmt.format(new Date(x * 1000))}
        series={[{ key: "down", label: t("download"), color: "var(--primary2)" }, { key: "up", label: t("upload"), color: "var(--cyan)", dashed: true }]} />
    </div>
  );
}

function Fig({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[11.5px] text-ink3">{label}</div>
      <div className="text-[15px] font-semibold num">{value}</div>
    </div>
  );
}

/** Live throughput: the last few minutes, sampled every 5 s. */
export function LiveChart({ live, height = 150 }: { live: Pt[]; height?: number }) {
  const { lang, t } = useI18n();
  const data = live.map((p) => ({ x: p.t, v: [p.down, p.up] }));
  const fmt = useMemo(() => new Intl.DateTimeFormat(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" }), [lang]);
  if (data.length < 2) return <div style={{ height }} className="shimmer rounded-xl" />;
  return (
    <AreaChart data={data} height={height} legend={false} format={(v) => speed(v, lang)} label={(x) => fmt.format(new Date(x * 1000))}
      series={[{ key: "down", label: t("download"), color: "var(--primary2)" }, { key: "up", label: t("upload"), color: "var(--cyan)", dashed: true }]} />
  );
}

/** Tiny area sparkline that draws itself in. */
export function Spark({ values, color = "var(--primary2)", height = 34 }: { values: number[]; color?: string; height?: number }) {
  const id = useId().replace(/:/g, "");
  if (values.length < 2) return <div style={{ height }} />;
  const W = 120;
  const max = Math.max(1, ...values) * 1.1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * W, height - 2 - (v / max) * (height - 4)] as [number, number]);
  const line = smooth(pts);
  return (
    <svg viewBox={`0 0 ${W} ${height}`} className="w-full block" style={{ height }} preserveAspectRatio="none" aria-hidden>
      <defs>
        <linearGradient id={`s${id}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={0.38} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={`${line} L${W},${height} L0,${height} Z`} fill={`url(#s${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth={1.8} vectorEffect="non-scaling-stroke" className="draw" style={{ ["--len" as any]: 400, filter: `drop-shadow(0 0 4px ${color})` }} />
    </svg>
  );
}

/** Horizontal stacked bar of parts (status breakdown). */
export function StackBar({ parts, height = 8 }: { parts: { value: number; color: string; label: string }[]; height?: number }) {
  const total = parts.reduce((s, p) => s + p.value, 0);
  return (
    <div className="flex rounded-full overflow-hidden bg-fg/[.07] gap-[2px]" style={{ height }}>
      {total > 0 && parts.filter((p) => p.value > 0).map((p) => <div key={p.label} title={p.label} style={{ width: `${(p.value / total) * 100}%`, background: p.color }} className="h-full transition-[width] duration-700" />)}
    </div>
  );
}

/** Donut chart for a distribution. */
export function Donut({ parts, size = 150, stroke = 18, center }: { parts: { value: number; color: string; label: string }[]; size?: number; stroke?: number; center?: React.ReactNode }) {
  const total = parts.reduce((s, p) => s + p.value, 0);
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  let acc = 0;
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="-rotate-90" aria-hidden>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="color-mix(in oklab, var(--fg) 7%, transparent)" strokeWidth={stroke} />
        {total > 0 && parts.filter((p) => p.value > 0).map((p) => {
          const len = (p.value / total) * c;
          const el = <circle key={p.label} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={p.color} strokeWidth={stroke} strokeDasharray={`${Math.max(0, len - 2)} ${c}`} strokeDashoffset={-acc} style={{ transition: "stroke-dasharray .8s" }}><title>{p.label}</title></circle>;
          acc += len;
          return el;
        })}
      </svg>
      <div className="absolute inset-0 grid place-items-center text-center">{center}</div>
    </div>
  );
}

export function LiveLabel({ up, down }: { up: number; down: number }) {
  const { lang } = useI18n();
  return (
    <span className="num">
      ↓ {speed(down, lang)} <span className="text-ink3 ms-2">↑ {speed(up, lang)}</span>
    </span>
  );
}
