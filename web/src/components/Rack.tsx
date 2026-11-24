// The overview's signal rack: this server as the main module, every protocol as
// a module with a segment meter, and a patch cable from each one to the server.
// A packet runs along each cable as fast as its real traffic; a protocol whose
// core is missing has no cable at all, and a stopped one has its jack dark.
import { useLayoutEffect, useRef, useState } from "react";
import type { Line, Overview } from "../lib/api";
import { bytes, cx, groups, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Logo } from "./Logo";
import { navigate } from "../App";

const color = (l: Line) => groups.find((g) => g.id === l.group)?.color ?? "var(--primary2)";
const noCore = (l: Line) => /not installed/i.test(l.error);
const rate = (l: Line) => l.rateUp + l.rateDown;

function state(l: Line): "off" | "nocore" | "err" | "live" | "idle" {
  if (!l.enabled) return "off";
  if (noCore(l)) return "nocore";
  if (l.error) return "err";
  return rate(l) > 512 ? "live" : "idle";
}
const led = { off: "", nocore: "rd", err: "rd", live: "on live", idle: "bl" } as const;

/** Lit cells of a 12-cell meter, on a log scale against the busiest protocol. */
function level(r: number, max: number) {
  if (r <= 0 || max <= 0) return 0;
  return Math.max(1, Math.round((12 * Math.log1p(r)) / Math.log1p(max)));
}

export function Cells({ lit, n = 12, c, vertical }: { lit: number; n?: number; c: string; vertical?: boolean }) {
  return (
    <span className={cx("flex gap-[2px]", vertical ? "flex-col-reverse" : "")} dir="ltr" aria-hidden>
      {Array.from({ length: n }, (_, i) => (
        <i key={i} className={vertical ? "w-3 h-[6px] rounded-[1.5px]" : "flex-1 h-[7px] rounded-[1.5px]"}
          style={i < lit ? { background: c, boxShadow: `0 0 6px color-mix(in oklab, ${c} 55%, transparent)` } : { background: "color-mix(in oklab, var(--text3) 20%, transparent)" }} />
      ))}
    </span>
  );
}

export function Rack({ d, server }: { d: Overview; server: string }) {
  const { t, lang, n, pc } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  const srv = useRef<HTMLDivElement>(null);
  const jacks = useRef<(HTMLSpanElement | null)[]>([]);
  const [paths, setPaths] = useState<{ d: string; c: string; dur: number; on: boolean; bad: boolean }[]>([]);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const lines = d.lines;
  const max = Math.max(1, ...lines.map(rate));
  const sys = d.system;
  const cpu = sys.cpu ?? 0, mem = sys.memTotal ? (sys.memUsed / sys.memTotal) * 100 : 0;
  const live = d.live[d.live.length - 1] ?? { up: 0, down: 0 };

  // cables follow the modules wherever they are laid out
  useLayoutEffect(() => {
    const draw = () => {
      const el = box.current, s = srv.current;
      if (!el || !s) return;
      const b = el.getBoundingClientRect(), sr = s.getBoundingClientRect();
      const out: typeof paths = [];
      lines.forEach((l, i) => {
        const j = jacks.current[i];
        const st = state(l);
        if (!j || st === "off" || st === "nocore") return;
        const r = j.getBoundingClientRect();
        const x1 = r.left + r.width / 2 - b.left + el.scrollLeft, y1 = r.top + r.height / 2 - b.top;
        const toLeft = x1 < sr.left - b.left + el.scrollLeft;
        const x2 = (toLeft ? sr.left : sr.right) - b.left + el.scrollLeft, y2 = sr.top - b.top + 46 + (i % 7) * 20;
        const sag = y1 + 48 + (i % 5) * 7;
        const k = (toLeft ? -1 : 1) * 46;
        out.push({
          d: `M${x1.toFixed(1)},${y1.toFixed(1)} C${x1.toFixed(1)},${sag} ${(x2 - k).toFixed(1)},${sag} ${x2.toFixed(1)},${y2.toFixed(1)}`,
          c: st === "err" ? "var(--bad)" : color(l), dur: Math.max(0.6, 3.4 - Math.log10(1 + rate(l)) * 0.42), on: st === "live", bad: st === "err",
        });
      });
      setPaths(out);
      setSize({ w: el.scrollWidth, h: el.clientHeight });
    };
    draw();
    const ro = new ResizeObserver(draw);
    if (box.current) ro.observe(box.current);
    return () => ro.disconnect();
  }, [lines]); // eslint-disable-line react-hooks/exhaustive-deps

  const chip = (label: string, value: string, tone?: string) => (
    <span className="stat-chip"><span className="ss">{label}</span><b className="font-semibold num" style={tone ? { color: tone } : undefined}>{value}</b></span>
  );

  return (
    <div className="mach rounded-[16px] overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 px-4 pt-3.5 pb-3 border-b border-hair">
        <h3 className="text-[15px] font-semibold me-1">{t("rackTitle")}</h3>
        <span className="ss me-auto">SIGNAL RACK</span>
        <button className="stat-chip" onClick={() => navigate("users")}><span className="led on live" /><b className="font-semibold num">{n(d.online)}</b>{t("rackOnline")}<span className="text-ink3">/ {n(d.users)}</span></button>
        {chip(t("rackToday"), bytes(d.today.up + d.today.down, lang))}
        {chip(t("rackMonth"), bytes(d.month.up + d.month.down, lang))}
        {chip("CPU", pc(cpu, 0), cpu > 90 ? "var(--warn)" : undefined)}
        {chip("MEM", pc(mem, 0), mem > 90 ? "var(--warn)" : undefined)}
      </div>

      {/* wide screens: modules and cables */}
      <div ref={box} className="relative hidden md:block overflow-x-auto overscroll-x-contain" style={{ height: 300 }}>
        <svg className="absolute top-0 pointer-events-none" style={{ insetInlineStart: 0, left: 0 }} width={size.w || "100%"} height={size.h || 300} aria-hidden>
          {paths.map((p, i) => (
            <g key={i}>
              <path d={p.d} fill="none" stroke="var(--bg)" strokeWidth={7} strokeLinecap="round" />
              <path d={p.d} fill="none" stroke={p.c} strokeWidth={3.5} strokeLinecap="round" strokeOpacity={0.9} strokeDasharray={p.bad ? "6 5" : undefined} />
              {p.on && <path d={p.d} fill="none" stroke="#fff" strokeWidth={2} strokeLinecap="round" strokeOpacity={0.8} pathLength={100} className="rack-pulse" style={{ animationDuration: p.dur + "s" }} />}
            </g>
          ))}
        </svg>
        <div className="relative flex items-start gap-2.5 p-4 w-max min-w-full">
          <div ref={srv} className="shrink-0 w-[240px] h-[196px] rounded-[12px] border border-hair2 p-3.5 bg-gradient-to-b from-panel3 to-panel2 shadow-[inset_0_1px_0_var(--edge)]">
            <div className="flex items-center gap-2"><Logo part="emblem" height={20} /><span className="ss !text-ink truncate">{server}</span><span className="ms-auto led on" /></div>
            <div className="mt-3 flex items-baseline gap-2" dir="ltr"><b className="mono text-[24px] font-semibold">{speed(live.up + live.down, "en").split(" ")[0]}</b><span className="ss">{speed(live.up + live.down, "en").split(" ")[1] ?? ""}</span></div>
            <div className="mt-3 flex flex-col gap-2">
              <div><div className="flex justify-between text-[12px]"><span className="ss">CPU</span><span className="num">{pc(cpu, 0)}</span></div><Cells lit={Math.round(cpu / 10)} n={10} c={cpu > 90 ? "var(--warn)" : "var(--ok)"} /></div>
              <div><div className="flex justify-between text-[12px]"><span className="ss">MEM</span><span className="num" style={mem > 90 ? { color: "var(--warn)" } : undefined}>{pc(mem, 0)}</span></div><Cells lit={Math.round(mem / 10)} n={10} c={mem > 90 ? "var(--warn)" : "var(--ok)"} /></div>
            </div>
          </div>
          {lines.map((l, i) => {
            const st = state(l), c = color(l);
            return (
              <button key={l.id} onClick={() => navigate("protocols", String(l.id))} title={l.remark || l.name}
                className={cx("rack-mod shrink-0 relative w-[112px] h-[196px] rounded-[11px] border p-2.5 text-start", st === "off" ? "border-hair opacity-55" : "border-hair2")}>
                <span className="flex items-center gap-1.5" dir="ltr"><span className={cx("led", led[st])} /><span className="mono text-[11px] text-ink3">:{l.port}</span></span>
                <span className="block mt-1.5 text-[13px] font-semibold leading-tight line-clamp-2" dir="ltr" style={{ textAlign: "left" }}>{l.remark || l.name}</span>
                <span className="absolute bottom-9 left-2.5"><Cells lit={st === "off" || st === "nocore" ? 0 : level(rate(l), max)} c={c} vertical /></span>
                <span className="absolute bottom-9 right-2.5 text-end text-[11.5px] leading-5">
                  <span className="block text-ink2 num">{st === "live" ? speed(rate(l), lang) : "—"}</span>
                  <span className="block text-ink3">{st === "nocore" ? <span className="text-bad">{t("rackNoCore")}</span> : st === "off" ? t("rackOff") : st === "err" ? <span className="text-bad">{t("error")}</span> : bytes(l.up + l.down, lang)}</span>
                </span>
                <span ref={(e) => { jacks.current[i] = e; }} className="absolute bottom-2 left-1/2 -ml-2 size-4 rounded-full bg-ground border-2" style={{ borderColor: st === "off" ? "var(--border2)" : st === "nocore" || st === "err" ? "var(--bad)" : c, boxShadow: "inset 0 0 0 3px #0e0f12" }} />
              </button>
            );
          })}
        </div>
      </div>

      {/* phones: one row per protocol */}
      <ul className="md:hidden divide-y divide-[var(--border)]">
        {lines.map((l) => {
          const st = state(l), c = color(l);
          return (
            <li key={l.id}>
              <button onClick={() => navigate("protocols", String(l.id))} className="w-full grid grid-cols-[10px_minmax(0,1fr)_92px] items-center gap-3 px-4 py-3 text-start">
                <span className={cx("led", led[st])} />
                <span className="min-w-0"><span className="block font-semibold truncate" dir="ltr" style={{ textAlign: lang === "fa" ? "right" : "left" }}>{l.remark || l.name}</span>
                  <span className="block text-[12px] text-ink3 truncate">{st === "nocore" ? <span className="text-bad">{t("rackNoCore")}</span> : st === "off" ? t("rackOff") : st === "live" ? speed(rate(l), lang) : bytes(l.up + l.down, lang)} · <span className="mono" dir="ltr">:{l.port}</span></span></span>
                <Cells lit={st === "off" || st === "nocore" ? 0 : Math.round(level(rate(l), max) * 10 / 12)} n={10} c={c} />
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
