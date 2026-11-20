import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Cable, Cloud, Globe2, Maximize2, Minimize2, Minus, Network as NetworkIcon, Plus, RotateCcw, Server, ShieldCheck, Zap } from "lucide-react";
import { api, type ClientMap, type Geo, type Group, type Line, type Overview } from "../lib/api";
import { ago, bytes, cx, duration, groupColor, groups, regionName, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { usePrefs } from "../lib/prefs";
import { LAND, landCells, project } from "../lib/worldmap";
import { Badge, Dot, ErrorState, EventMsg, IconButton, KV, Meter, Panel, Skeleton, Tabs, Timeline } from "../components/ui";
import { Logo } from "../components/Logo";
import { NetworkMap, shortName } from "../components/NetworkMap";
import { PageHead, eventMeta, navigate, useSession } from "../App";

type View = "topology" | "map";

export function NetworkPage() {
  const { t, lang, n, pc } = useI18n();
  const { events } = useSession();
  const [d, setD] = useState<Overview | null>(null);
  const [geo, setGeo] = useState<Geo | null>(null);
  const [geoErr, setGeoErr] = useState("");
  const [err, setErr] = useState("");
  const [view, setView] = useState<View>("topology");
  const [full, setFull] = useState(false);
  const [inspect, setInspect] = useState<Line | "server" | null>("server");
  const [clients, setClients] = useState<ClientMap | null>(null);
  const [hl, setHl] = useState<string | null>(null);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>("overview?days=1").then((r) => { if (alive) { setD(r); setErr(""); } }).catch((e) => alive && setErr(e.message));
    load();
    const id = setInterval(load, 4000);
    api<Geo>("network/geo").then(setGeo).catch((e) => setGeoErr(e.message));
    return () => { alive = false; clearInterval(id); };
  }, []);
  // where users connect from: only while the map is on screen
  useEffect(() => {
    if (view !== "map") return;
    let alive = true;
    const load = () => api<ClientMap>("network/clients").then((r) => alive && setClients(r)).catch(() => {});
    load();
    const id = setInterval(load, 15000);
    return () => { alive = false; clearInterval(id); };
  }, [view]);
  useEffect(() => {
    const on = () => setFull(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", on);
    return () => document.removeEventListener("fullscreenchange", on);
  }, []);
  const toggleFull = () => (document.fullscreenElement ? document.exitFullscreen() : box.current?.requestFullscreen?.());

  if (!d) return err ? <ErrorState message={err} /> : <Skeleton className="h-[560px]" />;
  const live = d.live[d.live.length - 1] ?? { up: 0, down: 0 };
  const server = d.system.hostname || d.system.publicIp || "Dunvex";
  const line = typeof inspect === "object" && inspect ? d.lines.find((l) => l.id === inspect.id) ?? inspect : null;

  return (
    <div>
      <PageHead title={t("networkCenter")} sub={t("networkSub")}
        action={<Tabs value={view} onChange={setView} items={[{ id: "topology", label: t("topologyView"), icon: <NetworkIcon className="size-4" /> }, { id: "map", label: t("mapView"), icon: <Globe2 className="size-4" /> }]} />} />

      <div ref={box} className={cx("glass rounded-[20px] overflow-hidden grid lg:grid-cols-[minmax(0,1fr)_340px]", full && "rounded-none bg-ground h-dvh")}>
        <div className={cx("relative min-w-0", view === "topology" && "hidden md:block")} style={{ height: full ? "100%" : "min(68vh, 640px)" }}>
          {view === "topology" ? <Topology d={d} server={server} onPick={setInspect} /> : <WorldMap geo={geo} err={geoErr} server={server} online={d.online} clients={clients} hl={hl} setHl={setHl} />}

          <div className="absolute top-3 [inset-inline-end:12px]">
            <IconButton label={full ? t("exitFullscreen") : t("fullscreen")} onClick={toggleFull} className="glass-strong">{full ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}</IconButton>
          </div>
        </div>
        {view === "topology" && (
          <div className="md:hidden p-4">
            <NetworkMap lines={d.lines} online={d.online} server={server} onStation={(id) => setInspect(d.lines.find((l) => l.id === id) ?? null)} onAdd={(g: Group) => navigate("protocols", "add-" + g)} />
          </div>
        )}
        <aside className="flex flex-col gap-4 p-4 border-t border-hair lg:border-t-0 lg:border-s overflow-y-auto overscroll-contain" style={{ maxHeight: full ? "100dvh" : "min(68vh, 640px)" }}>
          <div className="flex flex-wrap gap-2">
            <Badge color="var(--ok)" className="h-8 px-3"><Dot color="var(--ok)" pulse size={6} />{n(d.online)} {t("online")}</Badge>
            <Badge color="var(--primary2)" className="h-8 px-3 num">↓ {speed(live.down, lang)} · ↑ {speed(live.up, lang)}</Badge>
            <Badge color="var(--text2)" className="h-8 px-3">{n(d.inboundsOn)} / {n(d.inbounds)} {t("stations")}</Badge>
          </div>
          {view === "map" && <ClientsList clients={clients} hl={hl} setHl={setHl} />}
          {inspect && (
            <div className="rounded-2xl bg-fg/[.03] border border-hair p-4">
              {inspect === "server" ? (
                <>
                  <div className="flex items-center gap-3 mb-3">
                    <Logo part="emblem" height={26} />
                    <div className="min-w-0">
                      <div className="font-semibold truncate" dir="ltr">{server}</div>
                      <div className="text-[12px] text-ink3 mono sensitive" dir="ltr">{d.system.publicIp}</div>
                    </div>
                    <Badge color="var(--ok)" className="ms-auto">{t("healthy")}</Badge>
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <KV label={t("cpu")}><span className="num">{pc(d.system.cpu, 0)}</span></KV>
                    <KV label={t("ram")}><span className="num">{pc(d.system.memTotal ? (d.system.memUsed / d.system.memTotal) * 100 : 0, 0)}</span></KV>
                    <KV label={t("uptime")}>{duration(d.system.uptime, lang)}</KV>
                    <KV label={t("today")}>{bytes(d.today.down + d.today.up, lang)}</KV>
                    <KV label={t("location")}>{geo ? (geo.offline ? regionName(geo.countryCode, lang) : geo.city || geo.country) : "—"}</KV>
                    <KV label="TCP / UDP"><span className="num">{n(d.system.tcp)} / {n(d.system.udp)}</span></KV>
                  </div>
                </>
              ) : line ? (
                <>
                  <div className="flex items-center gap-3 mb-3">
                    <span className="size-3 rounded-full" style={{ background: groupColor(line.group), boxShadow: `0 0 10px ${groupColor(line.group)}` }} />
                    <div className="min-w-0 flex-1">
                      <div className="font-semibold truncate" dir="ltr">{line.remark || line.name}</div>
                      <div className="text-[12px] text-ink3 mono" dir="ltr">:{line.port}</div>
                    </div>
                    <Badge color={line.error ? "var(--bad)" : line.enabled ? "var(--ok)" : "var(--text3)"}>{line.error ? t("error") : line.enabled ? t("active") : t("disabled")}</Badge>
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <KV label={t("speedNow")}><span className="num">{speed(line.rateDown + line.rateUp, lang)}</span></KV>
                    <KV label={t("download")}>{bytes(line.down, lang)}</KV>
                    <KV label={t("upload")}>{bytes(line.up, lang)}</KV>
                  </div>
                  {line.error && <p className="text-[12px] text-bad mt-2 break-words" dir="ltr">{line.error}</p>}
                  <button onClick={() => navigate("protocols", String(line.id))} className="mt-3 text-[12.5px] text-primary2 hover:underline">{t("openProtocol")}</button>
                </>
              ) : null}
            </div>
          )}
          <div className="min-h-0">
            <div className="text-[12.5px] font-semibold mb-2.5">{t("recentEvents")}</div>
            {events.length === 0 ? <p className="text-ink3 text-[12px]">{t("noEvents")}</p> : (
              <Timeline dense items={events.slice(0, 8).map((e) => {
                const m = eventMeta(e.kind);
                return { id: e.id, icon: m.icon, color: m.color, title: <EventMsg message={e.message} className="block text-[12.5px] leading-5" />, meta: ago(e.at, lang) };
              })} />
            )}
          </div>
        </aside>
      </div>

      <div className="grid gap-[var(--gap)] md:grid-cols-2 xl:grid-cols-4 mt-[var(--gap)]">
        {groups.map((g) => {
          const list = d.lines.filter((l) => l.group === g.id);
          const rate = list.reduce((s, l) => s + l.rateDown + l.rateUp, 0);
          const total = list.reduce((s, l) => s + l.up + l.down, 0);
          return (
            <Panel key={g.id} title={t(g.label)} icon={<span className="size-2.5 rounded-full" style={{ background: g.color, boxShadow: `0 0 8px ${g.color}` }} />}>
              <div className="grid grid-cols-3 gap-3">
                <KV label={t("stations")}><span className="num">{n(list.filter((l) => l.enabled).length)} / {n(list.length)}</span></KV>
                <KV label={t("speedNow")}><span className="num">{speed(rate, lang)}</span></KV>
                <KV label={t("traffic")}>{bytes(total, lang)}</KV>
              </div>
            </Panel>
          );
        })}
      </div>
    </div>
  );
}

// ================================================================= topology graph

const stationName = (l: Line) => {
  const s = l.remark || shortName[l.preset] || l.name;
  return s.length > 14 ? s.slice(0, 13) + "…" : s;
};

function Topology({ d, server, onPick }: { d: Overview; server: string; onPick: (x: Line | "server") => void }) {
  const { t, n } = useI18n();
  const { prefs } = usePrefs();
  const reduced = prefs.motion === "reduced";
  const W = 1000, H = 620, cx0 = W / 2, cy0 = H / 2;
  const MAX = 12;
  const fam = groups.map((g, i) => {
    const a = (-Math.PI / 4) + (i * Math.PI) / 2;
    const all = d.lines.filter((l) => l.group === g.id);
    return { ...g, x: cx0 + Math.cos(a) * 190, y: cy0 + Math.sin(a) * 150, a, list: all.slice(0, MAX), more: Math.max(0, all.length - MAX) };
  });
  // stations fan out from their hub; more than four alternate between two rings
  const st = fam.flatMap((f) => {
    const k = f.list.length;
    const spread = k <= 1 ? 0 : Math.min(2.2, 0.42 * (k - 1));
    return f.list.map((l, i) => {
      const a = f.a + (k === 1 ? 0 : -spread / 2 + (spread * i) / (k - 1));
      const r = k > 4 ? (i % 2 ? 185 : 118) : 140;
      return { l, f, a, x: f.x + Math.cos(a) * r, y: f.y + Math.sin(a) * r * 0.9 };
    });
  });
  // each label takes the first spot that touches no node, hub or earlier label
  type Box = { x0: number; y0: number; x1: number; y1: number };
  const hit = (p: Box, q: Box) => p.x0 < q.x1 && q.x0 < p.x1 && p.y0 < q.y1 && q.y0 < p.y1;
  const blocked: Box[] = [
    ...st.map((q) => ({ x0: q.x - 17, y0: q.y - 17, x1: q.x + 17, y1: q.y + 17 })),
    ...fam.map((f) => ({ x0: f.x - 48, y0: f.y - 50, x1: f.x + 48, y1: f.y + 52 })),
    { x0: cx0 - 70, y0: cy0 - 66, x1: cx0 + 70, y1: cy0 + 104 },
  ];
  const labels = st.map((q) => {
    const text = stationName(q.l);
    const w = text.length * 6.8 + 4;
    const c = Math.cos(q.a), sn = Math.sin(q.a);
    const cands: { x: number; y: number; anchor: "start" | "middle" | "end" }[] = [
      { x: q.x + c * 24, y: q.y + sn * 24 + 4 + sn * 6, anchor: c > 0.35 ? "start" : c < -0.35 ? "end" : "middle" },
      { x: q.x, y: q.y + 33, anchor: "middle" },
      { x: q.x, y: q.y - 22, anchor: "middle" },
      { x: q.x + 22, y: q.y + 4, anchor: "start" },
      { x: q.x - 22, y: q.y + 4, anchor: "end" },
    ];
    const boxOf = (p: (typeof cands)[number]): Box => {
      const x0 = p.anchor === "start" ? p.x : p.anchor === "end" ? p.x - w : p.x - w / 2;
      return { x0, x1: x0 + w, y0: p.y - 12, y1: p.y + 3 };
    };
    let best = cands[0], score = Infinity;
    for (const p of cands) {
      const bx = boxOf(p);
      const sc = blocked.filter((o) => hit(bx, o)).length + (bx.x0 < 4 || bx.x1 > W - 4 || bx.y0 < 4 || bx.y1 > H - 4 ? 5 : 0);
      if (sc < score) { best = p; score = sc; if (sc === 0) break; }
    }
    blocked.push(boxOf(best));
    return { text, ...best };
  });
  // curves: hub links bow sideways, station links fan out along the family direction
  const hubPath = (f: (typeof fam)[number]) => {
    const mx = (cx0 + f.x) / 2, my = (cy0 + f.y) / 2;
    const dx = f.x - cx0, dy = f.y - cy0, len = Math.hypot(dx, dy) || 1;
    return `M${cx0},${cy0} Q${mx - (dy / len) * 26},${my + (dx / len) * 26} ${f.x},${f.y}`;
  };
  const stationPath = (f: (typeof fam)[number], x: number, y: number) => {
    const dist = Math.hypot(x - f.x, y - f.y);
    return `M${f.x},${f.y} Q${f.x + Math.cos(f.a) * dist * 0.55},${f.y + Math.sin(f.a) * dist * 0.55} ${x},${y}`;
  };
  // zoom to what is drawn: every node, label and the server, with a margin
  const fit = (() => {
    const all = [...blocked, { x0: cx0 - 80, y0: cy0 - 80, x1: cx0 + 80, y1: cy0 + 110 }];
    const x0 = Math.max(0, Math.min(...all.map((b) => b.x0)) - 28), y0 = Math.max(0, Math.min(...all.map((b) => b.y0)) - 28);
    const x1 = Math.min(W, Math.max(...all.map((b) => b.x1)) + 28), y1 = Math.min(H, Math.max(...all.map((b) => b.y1)) + 28);
    return `${x0} ${y0} ${x1 - x0} ${y1 - y0}`;
  })();
  const flowDur = (rate: number) => `${Math.max(0.6, 3.2 - Math.log10(Math.max(rate, 1000)) / 2.2).toFixed(2)}s`;
  return (
    <svg viewBox={fit} className="w-full h-full" role="img" aria-label={t("topologyView")}>
      <defs>
        <radialGradient id="topo-hub"><stop offset="0%" stopColor="var(--primary)" stopOpacity={0.42} /><stop offset="100%" stopColor="var(--primary)" stopOpacity={0} /></radialGradient>
        <radialGradient id="topo-fade"><stop offset="0%" stopColor="#fff" /><stop offset="70%" stopColor="#fff" stopOpacity={0.6} /><stop offset="100%" stopColor="#fff" stopOpacity={0} /></radialGradient>
        <mask id="topo-mask"><rect width={W} height={H} fill="url(#topo-fade)" /></mask>
        <pattern id="topo-dots" width={22} height={22} patternUnits="userSpaceOnUse"><circle cx={1} cy={1} r={1} fill="var(--text2)" opacity={0.22} /></pattern>
        <filter id="topo-glow" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge></filter>
        <filter id="topo-soft" x="-80%" y="-80%" width="260%" height="260%"><feGaussianBlur stdDeviation="7" /></filter>
        {fam.map((f) => (
          <g key={f.id}>
            <radialGradient id={`tg-${f.id}`}><stop offset="0%" stopColor={f.color} stopOpacity={0.4} /><stop offset="100%" stopColor={f.color} stopOpacity={0} /></radialGradient>
            <linearGradient id={`tp-${f.id}`} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={f.color} stopOpacity={0.55} /><stop offset="100%" stopColor={f.color} stopOpacity={0} /></linearGradient>
          </g>
        ))}
      </defs>
      <rect width={W} height={H} fill="url(#topo-dots)" mask="url(#topo-mask)" />
      <circle cx={cx0} cy={cy0} r={260} fill="url(#topo-hub)" />
      {[120, 220, 300].map((r) => <circle key={r} cx={cx0} cy={cy0} r={r} fill="none" stroke="var(--border)" strokeOpacity={0.7} strokeDasharray="2 8" />)}
      {fam.map((f) => {
        const rate = f.list.reduce((s, l) => s + l.rateDown + l.rateUp, 0);
        const Icon = famIcon[f.id];
        return (
          <g key={f.id}>
            <path d={hubPath(f)} fill="none" stroke={f.color} strokeWidth={f.list.length ? 3 : 1.5} opacity={f.list.length ? 0.85 : 0.3} strokeDasharray={f.list.length ? undefined : "3 6"} filter="url(#topo-glow)" />
            {rate > 0 && <path d={hubPath(f)} fill="none" stroke="#fff" strokeWidth={2.4} strokeLinecap="round" className="flow" style={{ ["--flow-dur" as string]: flowDur(rate), filter: `drop-shadow(0 0 4px ${f.color})` }} />}
            {st.map((q, i) => {
              if (q.f.id !== f.id) return null;
              const { l, x, y } = q;
              const lb = labels[i];
              const r8 = l.rateDown + l.rateUp;
              const active = l.enabled && r8 > 0;
              const stroke = !l.enabled ? "var(--text3)" : l.error ? "var(--bad)" : f.color;
              const SIcon = l.error ? AlertTriangle : Icon;
              return (
                <g key={l.id} className="cursor-pointer topo-node" onClick={() => onPick(l)}>
                  <title>{`${l.remark || l.name} :${l.port}`}</title>
                  <path d={stationPath(f, x, y)} fill="none" stroke={f.color} strokeOpacity={l.enabled ? 0.42 : 0.14} strokeWidth={1.5} />
                  {active && <path d={stationPath(f, x, y)} fill="none" stroke={f.color} strokeWidth={2} strokeLinecap="round" className="flow" style={{ ["--flow-dur" as string]: flowDur(r8) }} />}
                  {l.enabled && <rect x={x - 0.75} y={y + 16} width={1.5} height={44} fill={`url(#tp-${f.id})`} opacity={active ? 0.9 : 0.45} />}
                  {l.enabled && <circle cx={x} cy={y} r={22} fill={stroke} opacity={active ? 0.3 : 0.13} filter="url(#topo-soft)" />}
                  {active && <circle cx={x} cy={y} r={15} fill="none" stroke={stroke} strokeWidth={1.5} className="topo-pulse" />}
                  <circle cx={x} cy={y} r={15} fill="var(--surface)" stroke={stroke} strokeWidth={2.5} strokeDasharray={l.enabled ? undefined : "3 3"} />
                  <SIcon x={x - 7.5} y={y - 7.5} width={15} height={15} color={stroke} strokeWidth={2.2} aria-hidden />
                  <text x={lb.x} y={lb.y} textAnchor={lb.anchor} fill={l.enabled ? "var(--text)" : "var(--text3)"} fontSize={12} fontWeight={600} direction="ltr" style={{ fontFamily: "DVX Geist, sans-serif" }}>{lb.text}</text>
                </g>
              );
            })}
            {f.more > 0 && <text x={f.x} y={f.y + 46} textAnchor="middle" fill="var(--text2)" fontSize={11.5} direction="ltr">+{n(f.more)}</text>}
            <circle cx={f.x} cy={f.y} r={40} fill={`url(#tg-${f.id})`} />
            <circle cx={f.x} cy={f.y} r={24} fill="var(--surface)" stroke={f.color} strokeWidth={2.5} filter="url(#topo-glow)" opacity={f.list.length ? 1 : 0.5} />
            <Icon x={f.x - 10} y={f.y - 10} width={20} height={20} color={f.color} strokeWidth={2} aria-hidden />
            <circle cx={f.x + 18} cy={f.y - 18} r={10} fill={f.color} stroke="var(--surface)" strokeWidth={2} />
            <text x={f.x + 18} y={f.y - 14} textAnchor="middle" fill="#0b1324" fontSize={11} fontWeight={800} style={{ fontFamily: "DVX Geist, sans-serif" }}>{n(f.list.length)}</text>
            {(() => {
              const ly = f.y < cy0 ? f.y + 48 : f.y - 38;
              const w = t(f.label).length * 7 + 22;
              return (
                <g>
                  <rect x={f.x - w / 2} y={ly - 15} width={w} height={22} rx={11} fill="var(--surface)" stroke={f.color} strokeOpacity={0.45} />
                  <text x={f.x} y={ly} textAnchor="middle" fill={f.color} fontSize={12} fontWeight={700}>{t(f.label)}</text>
                </g>
              );
            })()}
          </g>
        );
      })}
      <g className="cursor-pointer" onClick={() => onPick("server")}>
        <circle cx={cx0} cy={cy0} r={56} fill="none" stroke="var(--primary2)" strokeWidth={2} className="topo-pulse" />
        <circle cx={cx0} cy={cy0} r={54} fill="var(--bg)" stroke="var(--primary2)" strokeWidth={3} filter="url(#topo-glow)" />
        <circle cx={cx0} cy={cy0} r={64} fill="none" stroke="var(--primary2)" strokeOpacity={0.4} strokeWidth={1.5} strokeDasharray="4 6">
          {!reduced && <animateTransform attributeName="transform" type="rotate" from={`0 ${cx0} ${cy0}`} to={`360 ${cx0} ${cy0}`} dur="24s" repeatCount="indefinite" />}
        </circle>
        <foreignObject x={cx0 - 34} y={cy0 - 28} width={68} height={46}><div className="w-full h-full grid place-items-center"><Logo part="emblem" width={60} /></div></foreignObject>
        <text x={cx0} y={cy0 + 84} textAnchor="middle" fill="var(--text)" fontSize={13} fontWeight={700} direction="ltr">{server}</text>
        <text x={cx0} y={cy0 + 101} textAnchor="middle" fill="var(--ok)" fontSize={11.5}>● {n(d.online)} {t("online")}</text>
      </g>
    </svg>
  );
}

const famIcon: Record<string, typeof Cloud> = { reality: ShieldCheck, cdn: Cloud, udp: Zap, classic: Cable };

// ================================================================= world map (dotted land, real server position, client arcs)

type Pt = [number, number];
const at = (lon: number, lat: number): Pt => { const [x, y] = project(lon, lat); return [x + 0.5, y + 0.5]; };

/** A flight-path curve from a client country to the server, bowing north. */
function arcPath(a: Pt, b: Pt): { d: string; len: number } {
  const dx = b[0] - a[0], dy = b[1] - a[1], len = Math.hypot(dx, dy);
  let nx = -dy / len, ny = dx / len;
  if (ny > 0) { nx = -nx; ny = -ny; }
  const lift = Math.min(len * 0.32, 16);
  const cx = (a[0] + b[0]) / 2 + nx * lift, cy = (a[1] + b[1]) / 2 + ny * lift;
  return { d: `M${a[0].toFixed(2)},${a[1].toFixed(2)} Q${cx.toFixed(2)},${cy.toFixed(2)} ${b[0].toFixed(2)},${b[1].toFixed(2)}`, len };
}

function WorldMap({ geo, err, server, online, clients, hl, setHl }: {
  geo: Geo | null; err: string; server: string; online: number; clients: ClientMap | null; hl: string | null; setHl: (cc: string | null) => void;
}) {
  const { t, n, lang } = useI18n();
  const { prefs } = usePrefs();
  const reduced = prefs.motion === "reduced" || (typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
  const cells = useMemo(() => landCells(), []);
  const [vb, setVb] = useState({ x: 0, y: 0, w: LAND.W, h: LAND.H });
  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null);
  const svg = useRef<SVGSVGElement>(null);
  const zoom = (f: number) => setVb((v) => {
    const w = Math.max(18, Math.min(LAND.W, v.w * f)), h = w * (LAND.H / LAND.W);
    return { x: Math.max(0, Math.min(LAND.W - w, v.x + (v.w - w) / 2)), y: Math.max(0, Math.min(LAND.H - h, v.y + (v.h - h) / 2)), w, h };
  });
  const pin = geo ? at(geo.lon, geo.lat) : null;
  // marks and labels keep their size on screen while the map zooms
  const k = vb.w / LAND.W;

  // arcs, heaviest last so they draw on top; clients in the server's own country ring the pin
  const { arcs, local } = useMemo(() => {
    const out: { cc: string; a: Pt; d: string; len: number; devices: number; users: number }[] = [];
    let local = 0;
    if (!pin || !clients) return { arcs: out, local };
    for (const c of clients.countries) {
      const a = at(c.lon, c.lat);
      const { d, len } = arcPath(a, pin);
      if (len < 1.5) { local += c.devices; continue; }
      out.push({ cc: c.cc, a, d, len, devices: c.devices, users: c.users });
    }
    return { arcs: out.reverse(), local };
  }, [clients, pin?.[0], pin?.[1]]); // eslint-disable-line react-hooks/exhaustive-deps

  // labels: biggest countries first; one that would cover another label is left out (its marker stays)
  const labels = useMemo(() => {
    const placed: { cc: string; x: number; y: number; text: string }[] = [];
    const boxes: [number, number, number, number][] = [];
    const fs = 2.05 * k;
    for (const c of [...arcs].reverse()) {
      const text = `${c.cc} ${n(c.devices)}`;
      const w = text.length * fs * 0.62 + fs * 0.6, h = fs * 1.25;
      const spots: Pt[] = [[c.a[0], c.a[1] - fs * 1.15], [c.a[0], c.a[1] + fs * 1.9], [c.a[0] + w / 2 + fs * 0.9, c.a[1] + fs * 0.4], [c.a[0] - w / 2 - fs * 0.9, c.a[1] + fs * 0.4]];
      for (const [x, y] of spots) {
        const box: [number, number, number, number] = [x - w / 2, y - h + fs * 0.25, x + w / 2, y + fs * 0.25];
        if (boxes.some((o) => box[0] < o[2] && box[2] > o[0] && box[1] < o[3] && box[3] > o[1])) continue;
        boxes.push(box);
        placed.push({ cc: c.cc, x, y, text });
        break;
      }
    }
    return placed;
  }, [arcs, k, n]);

  const maxDev = Math.max(1, ...arcs.map((a) => a.devices));
  return (
    <div className="absolute inset-0">
      <svg ref={svg} viewBox={`${vb.x} ${vb.y} ${vb.w} ${vb.h}`} className="w-full h-full touch-none" preserveAspectRatio="xMidYMid meet"
        onWheel={(e) => zoom(e.deltaY > 0 ? 1.15 : 0.87)}
        onPointerDown={(e) => { (e.target as Element).setPointerCapture?.(e.pointerId); drag.current = { x: e.clientX, y: e.clientY, vx: vb.x, vy: vb.y }; }}
        onPointerMove={(e) => {
          if (!drag.current || !svg.current) return;
          const r = svg.current.getBoundingClientRect();
          const kk = vb.w / r.width;
          const nx = drag.current.vx - (e.clientX - drag.current.x) * kk, ny = drag.current.vy - (e.clientY - drag.current.y) * kk;
          setVb((v) => ({ ...v, x: Math.max(0, Math.min(LAND.W - v.w, nx)), y: Math.max(0, Math.min(LAND.H - v.h, ny)) }));
        }}
        onPointerUp={() => (drag.current = null)} role="img" aria-label={t("mapView")}>
        <defs>
          {arcs.map((c) => (
            <linearGradient key={c.cc} id={`arc-${c.cc}`} gradientUnits="userSpaceOnUse" x1={c.a[0]} y1={c.a[1]} x2={pin![0]} y2={pin![1]}>
              <stop offset="0%" style={{ stopColor: "var(--color-violet)", stopOpacity: 0.35 }} />
              <stop offset="100%" style={{ stopColor: "var(--primary2)", stopOpacity: 0.95 }} />
            </linearGradient>
          ))}
        </defs>
        {cells.map(([c, r]) => <circle key={c + "-" + r} cx={c + 0.5} cy={r + 0.5} r={0.3} fill="var(--text3)" opacity={0.45} />)}

        {arcs.map((c) => {
          const dim = hl !== null && hl !== c.cc;
          const w = (0.16 + 0.2 * Math.log2(1 + c.devices)) * k;
          return (
            <g key={c.cc} opacity={dim ? 0.18 : 1} style={{ transition: "opacity 160ms ease" }}
              onPointerEnter={() => setHl(c.cc)} onPointerLeave={() => setHl(null)}>
              <title>{`${regionName(c.cc, lang)} · ${n(c.devices)} ${t("devices")} · ${n(c.users)} ${t("users")}`}</title>
              <path d={c.d} fill="none" stroke={`url(#arc-${c.cc})`} strokeWidth={Math.min(w, 1.2 * k)} strokeLinecap="round" />
              {/* a wide invisible stroke makes the thin arc easy to point at */}
              <path d={c.d} fill="none" stroke="transparent" strokeWidth={2.4 * k} />
              {!reduced && (
                <circle r={0.34 * k} fill="#fff" opacity={0.9}>
                  <animateMotion dur={`${Math.max(1.8, c.len / 12).toFixed(1)}s`} repeatCount="indefinite" path={c.d} />
                </circle>
              )}
              <circle cx={c.a[0]} cy={c.a[1]} r={(0.9 + 1.4 * (c.devices / maxDev)) * k} fill="var(--color-violet)" opacity={0.16} />
              <circle cx={c.a[0]} cy={c.a[1]} r={0.6 * k} fill="var(--color-violet)" stroke="#fff" strokeWidth={0.16 * k} />
            </g>
          );
        })}
        {labels.map((l) => (
          <text key={l.cc} x={l.x} y={l.y} textAnchor="middle" fontSize={2.05 * k} fontWeight={600} fill="var(--text)"
            opacity={hl !== null && hl !== l.cc ? 0.25 : 0.92} paintOrder="stroke" stroke="var(--bg)" strokeWidth={0.6 * k} strokeLinejoin="round"
            className="num pointer-events-none" direction="ltr" style={{ transition: "opacity 160ms ease" }}>{l.text}</text>
        ))}

        {pin && (
          <g>
            {local > 0 && <circle cx={pin[0]} cy={pin[1]} r={2.1 * k} fill="none" stroke="var(--color-violet)" strokeWidth={0.22 * k} strokeDasharray={`${0.5 * k} ${0.35 * k}`}><title>{`${n(local)} ${t("devices")} · ${t("localClients")}`}</title></circle>}
            <circle cx={pin[0]} cy={pin[1]} r={2.6 * k} fill="var(--primary)" opacity={0.18}>
              {!reduced && <animate attributeName="r" values={`${1.2 * k};${3.4 * k};${1.2 * k}`} dur="2.6s" repeatCount="indefinite" />}
            </circle>
            <circle cx={pin[0]} cy={pin[1]} r={0.85 * k} fill="var(--primary2)" stroke="#fff" strokeWidth={0.25 * k} />
          </g>
        )}
      </svg>
      <div className="absolute top-14 [inset-inline-end:12px] flex flex-col gap-1 glass-strong rounded-xl p-1">
        <IconButton label="+" onClick={() => zoom(0.75)}><Plus className="size-4" /></IconButton>
        <IconButton label="−" onClick={() => zoom(1.33)}><Minus className="size-4" /></IconButton>
        <IconButton label={t("resetZoom")} onClick={() => setVb({ x: 0, y: 0, w: LAND.W, h: LAND.H })}><RotateCcw className="size-4" /></IconButton>
      </div>
      {geo ? (
        <div className="absolute top-14 [inset-inline-start:12px] glass-strong rounded-xl px-3 py-2 text-[12.5px] flex items-center gap-2.5">
          <Server className="size-4 text-primary2" />
          <span className="font-semibold" dir="ltr">{server}</span>
          <span className="text-ink3">{geo.city ? `${geo.city}, ` : ""}{geo.offline ? regionName(geo.countryCode, lang) : geo.country}</span>
          <span className="text-online num">● {n(online)}</span>
        </div>
      ) : (
        <div className="absolute top-14 [inset-inline-start:12px] max-w-sm glass-strong rounded-xl px-3 py-2 text-[12px] text-ink3">{err ? `${t("geoUnavailable")} (${err})` : t("locating")}</div>
      )}
      {clients && (
        <div className="absolute bottom-3 [inset-inline-start:12px] text-[11px] text-ink3">
          {clients.missing ? t("geoMissing") : clients.source.startsWith("DB-IP")
            ? <a href="https://db-ip.com" target="_blank" rel="noreferrer noopener" className="hover:text-ink2 underline-offset-2 hover:underline">{t("geoAttribution")}</a>
            : t("geoPartial")}
        </div>
      )}
    </div>
  );
}

/** The side list next to the map: countries by connected devices. */
function ClientsList({ clients, hl, setHl }: { clients: ClientMap | null; hl: string | null; setHl: (cc: string | null) => void }) {
  const { t, n, lang } = useI18n();
  if (!clients) return <Skeleton className="h-28" />;
  const max = Math.max(1, ...clients.countries.map((c) => c.devices));
  return (
    <div>
      <div className="flex items-center gap-2 mb-1">
        <Globe2 className="size-4 text-primary2" />
        <span className="text-[12.5px] font-semibold">{t("whereFrom")}</span>
        <span className="ms-auto text-[12px] text-ink3 num">{n(clients.devices)} {t("devices")}</span>
      </div>
      <p className="text-[11.5px] text-ink3 mb-3">{t("whereFromSub")}</p>
      {clients.countries.length === 0 && clients.unplaced === 0 ? (
        <p className="text-ink3 text-[12.5px] py-3">{t("noClientsNow")}</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {clients.countries.map((c) => (
            <li key={c.cc}>
              <button type="button" onPointerEnter={() => setHl(c.cc)} onPointerLeave={() => setHl(null)} onFocus={() => setHl(c.cc)} onBlur={() => setHl(null)}
                className={cx("w-full text-start rounded-lg px-2 py-1.5 transition-colors", hl === c.cc ? "bg-fg/[.06]" : "hover:bg-fg/[.04]")}>
                <div className="flex items-center gap-2 text-[12.5px]">
                  <span className="mono text-[11px] text-violet w-6 shrink-0" dir="ltr">{c.cc}</span>
                  <span className="truncate flex-1">{regionName(c.cc, lang)}</span>
                  <span className="num text-ink2">{n(c.devices)}</span>
                  <span className="text-ink3 text-[11px] num w-14 text-end">{n(c.users)} {t("users")}</span>
                </div>
                <Meter value={c.devices} max={max} color="var(--color-violet)" height={3} className="mt-1.5" plain />
              </button>
            </li>
          ))}
          {clients.unplaced > 0 && (
            <li className="flex items-center gap-2 text-[12px] text-ink3 px-2 py-1.5">
              <span className="w-6 shrink-0 text-center">·</span>
              <span className="flex-1">{t("unplaced")}</span>
              <span className="num">{n(clients.unplaced)}</span>
            </li>
          )}
        </ul>
      )}
      <p className="text-[11px] text-ink3 mt-3 leading-relaxed">{t("geoPrivacy")}</p>
    </div>
  );
}
