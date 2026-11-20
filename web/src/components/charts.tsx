import { useId, useMemo, useRef, useState } from "react";
import { bytes, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";

type Pt = { t: number; up: number; down: number };

/** Smooth path through points (monotone-ish cubic). */
function smooth(pts: [number, number][]) {
  if (pts.length < 2) return "";
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 1; i < pts.length; i++) {
    const [x0, y0] = pts[i - 1];
    const [x1, y1] = pts[i];
    const cx = (x0 + x1) / 2;
    d += ` C${cx},${y0} ${cx},${y1} ${x1},${y1}`;
  }
  return d;
}

/** Two-series area chart (download over upload) with a hover read-out. */
export function AreaChart({
  data,
  height = 220,
  format,
  label,
  showAxis = true,
}: {
  data: { x: number; a: number; b: number }[];
  height?: number;
  format: (v: number) => string;
  label: (x: number) => string;
  showAxis?: boolean;
}) {
  const { lang } = useI18n();
  const id = useId().replace(/:/g, "");
  const ref = useRef<SVGSVGElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const W = 800;
  const H = height;
  const top = 12;
  const bottom = showAxis ? 22 : 4;
  const max = Math.max(1, ...data.map((d) => d.a + d.b)) * 1.12;
  const x = (i: number) => (data.length <= 1 ? W / 2 : (i / (data.length - 1)) * W);
  const y = (v: number) => top + (H - top - bottom) * (1 - v / max);
  const pa = data.map((d, i) => [x(i), y(d.a + d.b)] as [number, number]);
  const pb = data.map((d, i) => [x(i), y(d.b)] as [number, number]);
  const lineA = smooth(pa);
  const lineB = smooth(pb);
  const base = H - bottom;
  const areaA = lineA && `${lineA} L${W},${base} L0,${base} Z`;
  const areaB = lineB && `${lineB} L${W},${base} L0,${base} Z`;
  const h = hover !== null ? data[hover] : null;
  const ticks = [0.25, 0.5, 0.75, 1];

  const onMove = (e: React.PointerEvent) => {
    const r = ref.current?.getBoundingClientRect();
    if (!r || data.length === 0) return;
    const px = (e.clientX - r.left) / r.width; // the chart is always drawn left to right
    setHover(Math.max(0, Math.min(data.length - 1, Math.round(px * (data.length - 1)))));
  };

  return (
    <div className="relative" dir="ltr">
      <svg ref={ref} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="w-full block touch-none" style={{ height }} onPointerMove={onMove} onPointerLeave={() => setHover(null)} role="img" aria-label="chart">
        <defs>
          <linearGradient id={`ga${id}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#4c8dff" stopOpacity={0.45} />
            <stop offset="100%" stopColor="#4c8dff" stopOpacity={0} />
          </linearGradient>
          <linearGradient id={`gb${id}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#9b7bff" stopOpacity={0.5} />
            <stop offset="100%" stopColor="#9b7bff" stopOpacity={0} />
          </linearGradient>
        </defs>
        {ticks.map((g) => (
          <line key={g} x1={0} x2={W} y1={y(max * g / 1.12)} y2={y(max * g / 1.12)} stroke="rgba(140,165,220,.09)" strokeDasharray="3 5" vectorEffect="non-scaling-stroke" />
        ))}
        {areaA && <path d={areaA} fill={`url(#ga${id})`} />}
        {areaB && <path d={areaB} fill={`url(#gb${id})`} />}
        {lineA && <path d={lineA} fill="none" stroke="#6ea4ff" strokeWidth={2} vectorEffect="non-scaling-stroke" />}
        {lineB && <path d={lineB} fill="none" stroke="#a68cff" strokeWidth={1.6} strokeDasharray="5 4" vectorEffect="non-scaling-stroke" />}
        {hover !== null && h && (
          <>
            <line x1={x(hover)} x2={x(hover)} y1={top} y2={base} stroke="rgba(255,255,255,.25)" vectorEffect="non-scaling-stroke" />
          </>
        )}
      </svg>
      {hover !== null && h && (
        <>
          <span className="pointer-events-none absolute size-2.5 rounded-full bg-[#6ea4ff] ring-4 ring-[#6ea4ff]/25 -translate-x-1/2 -translate-y-1/2" style={{ left: `${(x(hover) / W) * 100}%`, top: `${(y(h.a + h.b) / H) * 100}%` }} />
          <div
            className="pointer-events-none absolute top-1 z-10 rounded-xl glass-strong px-3 py-2 text-[12px] shadow-[0_14px_30px_-12px_rgba(0,0,0,.8)] min-w-36"
            style={{ left: `${Math.min(78, Math.max(2, (x(hover) / W) * 100 - 8))}%` }}
          >
            <div className="text-ink3 mb-1" dir={lang === "fa" ? "rtl" : "ltr"}>{label(h.x)}</div>
            <div className="flex items-center gap-2 num"><i className="size-2 rounded-full bg-[#6ea4ff]" />{format(h.a)}</div>
            <div className="flex items-center gap-2 num text-ink2"><i className="size-2 rounded-full bg-[#a68cff]" />{format(h.b)}</div>
          </div>
        </>
      )}
      {showAxis && data.length > 1 && (
        <div className="flex justify-between text-[11px] text-ink3 -mt-4 px-0.5 num pointer-events-none">
          <span>{label(data[0].x)}</span>
          <span>{label(data[Math.floor(data.length / 2)].x)}</span>
          <span>{label(data[data.length - 1].x)}</span>
        </div>
      )}
    </div>
  );
}

/** Daily usage (download + upload) for the last `days` days. */
export function UsageChart({ series, days = 30, height = 230 }: { series: Pt[]; days?: number; height?: number }) {
  const { lang, n, t } = useI18n();
  const data = useMemo(() => {
    const out: { x: number; a: number; b: number }[] = [];
    const now = new Date();
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() / 1000 - (days - 1) * 86400;
    for (let i = 0; i < days; i++) out.push({ x: start + i * 86400, a: 0, b: 0 });
    for (const p of series) {
      const d = new Date(p.t * 1000);
      const key = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / 1000;
      const idx = Math.round((key - start) / 86400);
      if (idx >= 0 && idx < days) {
        out[idx].a += p.down;
        out[idx].b += p.up;
      }
    }
    return out;
  }, [series, days]);
  const fmtDay = useMemo(() => new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", { month: "short", day: "numeric" }), [lang]);
  const total = data.reduce((s, d) => s + d.a + d.b, 0);
  const peak = data.reduce((m, d) => Math.max(m, d.a + d.b), 0);
  const avg = total / Math.max(1, data.filter((d) => d.a + d.b > 0).length || 1);
  return (
    <div>
      <div className="flex flex-wrap gap-x-6 gap-y-2 mb-4">
        <Legend color="#6ea4ff" label={t("download")} value={bytes(data.reduce((s, d) => s + d.a, 0), lang)} />
        <Legend color="#a68cff" label={t("upload")} value={bytes(data.reduce((s, d) => s + d.b, 0), lang)} dashed />
        <Legend label={t("dailyAvg")} value={bytes(avg, lang)} />
        <Legend label={t("peakDay")} value={bytes(peak, lang)} />
      </div>
      <AreaChart data={data} height={height} format={(v) => bytes(v, lang)} label={(x) => fmtDay.format(new Date(x * 1000))} />
      <span className="sr-only">{n(days)}</span>
    </div>
  );
}

function Legend({ color, label, value, dashed }: { color?: string; label: string; value: string; dashed?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-1.5 text-[11.5px] text-ink3">
        {color && <i className="h-0.5 w-3.5 rounded-full" style={{ background: dashed ? `repeating-linear-gradient(90deg, ${color} 0 4px, transparent 4px 7px)` : color }} />}
        {label}
      </div>
      <div className="text-[15px] font-semibold num">{value}</div>
    </div>
  );
}

/** Live throughput: the last few minutes, sampled every 5 s. */
export function LiveChart({ live, height = 150 }: { live: Pt[]; height?: number }) {
  const { lang } = useI18n();
  const data = live.map((p) => ({ x: p.t, a: p.down, b: p.up }));
  const fmt = useMemo(() => new Intl.DateTimeFormat(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" }), [lang]);
  if (data.length < 2) return <div style={{ height }} className="grid place-items-center text-ink3 text-sm">…</div>;
  return <AreaChart data={data} height={height} format={(v) => speed(v, lang)} label={(x) => fmt.format(new Date(x * 1000))} />;
}

/** Tiny area sparkline. */
export function Spark({ values, color = "#4c8dff", height = 34 }: { values: number[]; color?: string; height?: number }) {
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
          <stop offset="0%" stopColor={color} stopOpacity={0.4} />
          <stop offset="100%" stopColor={color} stopOpacity={0} />
        </linearGradient>
      </defs>
      <path d={`${line} L${W},${height} L0,${height} Z`} fill={`url(#s${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth={1.6} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

/** Horizontal stacked bar of parts (status breakdown). */
export function StackBar({ parts, height = 8 }: { parts: { value: number; color: string; label: string }[]; height?: number }) {
  const total = parts.reduce((s, p) => s + p.value, 0);
  return (
    <div className="flex rounded-full overflow-hidden bg-white/[.06] gap-[2px]" style={{ height }}>
      {total > 0 &&
        parts
          .filter((p) => p.value > 0)
          .map((p) => <div key={p.label} title={p.label} style={{ width: `${(p.value / total) * 100}%`, background: p.color }} className="h-full transition-[width] duration-700" />)}
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
