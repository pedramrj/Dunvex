import { useMemo, useState } from "react";
import { bytes, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";

/** Daily stacked bars (download + upload) built from hourly buckets. */
export function UsageBars({ series, days = 30 }: { series: { t: number; up: number; down: number }[]; days?: number }) {
  const { lang, n } = useI18n();
  const [hover, setHover] = useState<number | null>(null);
  const data = useMemo(() => {
    const out: { day: number; up: number; down: number }[] = [];
    const now = new Date();
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() / 1000 - (days - 1) * 86400;
    for (let i = 0; i < days; i++) out.push({ day: start + i * 86400, up: 0, down: 0 });
    for (const p of series) {
      const d = new Date(p.t * 1000);
      const key = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / 1000;
      const idx = Math.round((key - start) / 86400);
      if (idx >= 0 && idx < days) {
        out[idx].up += p.up;
        out[idx].down += p.down;
      }
    }
    return out;
  }, [series, days]);
  const max = Math.max(1, ...data.map((d) => d.up + d.down));
  const W = 720, H = 200, pad = 4;
  const bw = (W - pad * 2) / data.length;
  const fmtDay = new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", { month: "short", day: "numeric" });
  const h = hover !== null ? data[hover] : null;
  const total = data.reduce((s, d) => s + d.up + d.down, 0);
  return (
    <div>
      <div className="flex items-baseline justify-between mb-3 min-h-6 text-[13px]">
        {h ? (
          <>
            <span className="text-ink2">{fmtDay.format(new Date(h.day * 1000))}</span>
            <span className="num">
              <span className="text-ink">↓ {bytes(h.down, lang)}</span>
              <span className="text-ink3 ms-3">↑ {bytes(h.up, lang)}</span>
            </span>
          </>
        ) : (
          <>
            <span className="text-ink2">{n(days)} {lang === "fa" ? "روز" : "days"}</span>
            <span className="num text-ink">{bytes(total, lang)}</span>
          </>
        )}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full h-[180px] block" preserveAspectRatio="none" onMouseLeave={() => setHover(null)} role="img" aria-label="usage">
        {[0.25, 0.5, 0.75].map((g) => (
          <line key={g} x1={0} x2={W} y1={H * g} y2={H * g} stroke="var(--color-hair)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
        ))}
        {data.map((d, i) => {
          const hd = ((d.down) / max) * (H - 8);
          const hu = ((d.up) / max) * (H - 8);
          const x = pad + i * bw + bw * 0.18;
          const w = bw * 0.64;
          const active = hover === i;
          return (
            <g key={d.day} onMouseEnter={() => setHover(i)}>
              <rect x={pad + i * bw} y={0} width={bw} height={H} fill="transparent" />
              <rect x={x} y={H - hd} width={w} height={Math.max(hd, d.down > 0 ? 1.5 : 0)} rx={2} fill={active ? "#fff" : "var(--color-ink)"} opacity={active || hover === null ? 0.92 : 0.45} />
              <rect x={x} y={H - hd - hu} width={w} height={hu} rx={2} fill="var(--color-cdn)" opacity={active || hover === null ? 0.85 : 0.4} />
            </g>
          );
        })}
      </svg>
      <div className="flex gap-4 mt-3 text-[12px] text-ink2">
        <span className="inline-flex items-center gap-1.5"><i className="size-2 rounded-sm bg-ink" />{lang === "fa" ? "دانلود" : "Download"}</span>
        <span className="inline-flex items-center gap-1.5"><i className="size-2 rounded-sm bg-cdn" />{lang === "fa" ? "آپلود" : "Upload"}</span>
      </div>
    </div>
  );
}

/** Small live trace for a value that moves (recent history, real samples). */
export function Trace({ values, color = "var(--color-ink)", height = 28 }: { values: number[]; color?: string; height?: number }) {
  if (values.length < 2) return <div style={{ height }} />;
  const W = 120;
  const max = Math.max(1, ...values);
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * W},${height - 2 - (v / max) * (height - 4)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${W} ${height}`} className="w-full block" style={{ height }} preserveAspectRatio="none" aria-hidden>
      <polyline points={pts} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
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
