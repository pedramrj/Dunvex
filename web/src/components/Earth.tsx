// The live world map: real coastlines (Natural Earth), every server and node as
// a pin, every place users connect from as a light, and an arc from each place
// to each server its devices reach. Places come from the server's own offline
// data; the map never receives an address. Zoom with the wheel, drag to move.
import { useEffect, useMemo, useRef, useState } from "react";
import { Minus, Plus, RotateCcw, Search, Server as ServerIcon, Signal, Globe2 } from "lucide-react";
import { api, type MapPlace, type MapServer, type UserMap } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { cx, regionName } from "../lib/format";
import { operatorName } from "../lib/operators";
import { BORDERS, LAND, WORLD, toXY } from "../lib/world";
import { navigate } from "../App";

const W = WORLD.w, H = WORLD.h;
export const SERVER_COLORS = ["#3d8bff", "#24cfd2", "#a48bff", "#3ad48a", "#ff8fb1", "#f2c94c", "#7fd1ff"];
export const serverColor = (servers: MapServer[], id: number) => {
  const i = servers.findIndex((s) => s.id === id);
  return SERVER_COLORS[(i < 0 ? 0 : i) % SERVER_COLORS.length];
};

type View = { k: number; x: number; y: number };
const FULL: View = { k: 1, x: 0, y: 0 };

/** Fits a view around points (in map units), with some air. */
function fit(pts: [number, number][], maxK = 6): View {
  if (!pts.length) return FULL;
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const [x, y] of pts) { x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x); y1 = Math.max(y1, y); }
  const pad = 40;
  const k = Math.max(1, Math.min(maxK, W / (x1 - x0 + 2 * pad), H / (y1 - y0 + 2 * pad)));
  const cx_ = (x0 + x1) / 2, cy = (y0 + y1) / 2;
  return clamp({ k, x: cx_ - W / k / 2, y: cy - H / k / 2 });
}
function clamp(v: View): View {
  const k = Math.max(1, Math.min(8, v.k));
  return { k, x: Math.max(0, Math.min(W - W / k, v.x)), y: Math.max(0, Math.min(H - H / k, v.y)) };
}

/** An arc from a place to a server, bowed up in proportion to its length. */
function arc(a: [number, number], b: [number, number]) {
  const mx = (a[0] + b[0]) / 2, my = (a[1] + b[1]) / 2, d = Math.hypot(b[0] - a[0], b[1] - a[1]);
  return `M${a[0].toFixed(1)},${a[1].toFixed(1)} Q${mx.toFixed(1)},${(my - d * 0.32).toFixed(1)} ${b[0].toFixed(1)},${b[1].toFixed(1)}`;
}

export function placeKey(p: MapPlace) { return p.cc + "|" + (p.region ?? "") + "|" + (p.city ?? ""); }

interface Props {
  data: UserMap | null;
  /** only this user's places (the user drawer) */
  focusUser?: number;
  /** highlight: a place key, a server id ("s:3") or a user ("u:12") */
  hl?: string | null;
  setHl?: (h: string | null) => void;
  height?: number | string;
  compact?: boolean;
}

export function Earth({ data, focusUser, hl, setHl, height = 520, compact }: Props) {
  const { t, lang, n, sep } = useI18n();
  const [view, setView] = useState<View>(FULL);
  const [tip, setTip] = useState<{ x: number; y: number; p: MapPlace } | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const drag = useRef<{ px: number; py: number; v: View } | null>(null);

  const servers = data?.servers ?? [];
  const pinOf = useMemo(() => {
    const m = new Map<number, [number, number]>();
    for (const s of servers) if (s.place) m.set(s.id, toXY(s.place.lon, s.place.lat));
    return m;
  }, [servers]);
  const places = useMemo(() => {
    const list = data?.places ?? [];
    if (focusUser == null) return list;
    return list.map((p) => ({ ...p, devices: p.devices.filter((d) => d.uid === focusUser) })).filter((p) => p.devices.length);
  }, [data, focusUser]);

  // arcs: one per place and server, counted by devices
  const arcs = useMemo(() => {
    const out: { key: string; d: string; color: string; place: string; server: number; w: number }[] = [];
    for (const p of places) {
      const at = toXY(p.lon, p.lat), per = new Map<number, number>();
      for (const d of p.devices) for (const s of d.via) per.set(s, (per.get(s) ?? 0) + 1);
      for (const [sid, count] of per) {
        const pin = pinOf.get(sid);
        if (!pin || Math.hypot(pin[0] - at[0], pin[1] - at[1]) < 2) continue;
        out.push({ key: placeKey(p) + ">" + sid, d: arc(at, pin), color: serverColor(servers, sid), place: placeKey(p), server: sid, w: Math.min(3, 0.8 + Math.log2(1 + count) * 0.6) });
      }
    }
    return out;
  }, [places, pinOf, servers]);

  // the drawer's map opens on its user
  useEffect(() => {
    if (focusUser == null || !data) return;
    const pts: [number, number][] = places.map((p) => toXY(p.lon, p.lat));
    for (const p of places) for (const d of p.devices) for (const s of d.via) { const pin = pinOf.get(s); if (pin) pts.push(pin); }
    setView(fit(pts, 5));
  }, [focusUser, data]); // eslint-disable-line react-hooks/exhaustive-deps

  const zoomAt = (factor: number, fx = 0.5, fy = 0.5) => setView((v) => {
    const k = Math.max(1, Math.min(8, v.k * factor));
    const wx = v.x + (W / v.k) * fx, wy = v.y + (H / v.k) * fy;
    return clamp({ k, x: wx - (W / k) * fx, y: wy - (H / k) * fy });
  });
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      zoomAt(e.deltaY < 0 ? 1.25 : 0.8, (e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height);
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
  }, []);

  const k = view.k, s = 1 / k; // labels and lights keep their screen size
  const isHl = (place: string, uids: number[], sids: number[]) => !hl || hl === place || (hl.startsWith("u:") && uids.includes(+hl.slice(2))) || (hl.startsWith("s:") && sids.includes(+hl.slice(2)));
  const maxDev = Math.max(1, ...places.map((p) => p.devices.length));

  return (
    <div ref={box} className="relative overflow-hidden select-none earth" style={height === "auto" ? { aspectRatio: "2 / 1", touchAction: "none" } : { height, touchAction: "none" }}
      onPointerDown={(e) => { (e.target as Element).setPointerCapture?.(e.pointerId); drag.current = { px: e.clientX, py: e.clientY, v: view }; }}
      onPointerMove={(e) => {
        const d = drag.current, el = box.current;
        if (!d || !el) return;
        const r = el.getBoundingClientRect();
        setView(clamp({ k: d.v.k, x: d.v.x - ((e.clientX - d.px) / r.width) * (W / d.v.k), y: d.v.y - ((e.clientY - d.py) / r.height) * (H / d.v.k) }));
      }}
      onPointerUp={() => { drag.current = null; }}
      onDoubleClick={(e) => { const r = box.current!.getBoundingClientRect(); zoomAt(1.8, (e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height); }}>
      <svg viewBox={`${view.x} ${view.y} ${W / k} ${H / k}`} preserveAspectRatio="xMidYMid meet" className="w-full h-full block" role="img" aria-label={t("liveMap")}>
        <defs>
          <radialGradient id="earthGlow" cx="50%" cy="45%" r="60%"><stop offset="0" stopColor="#12161d" /><stop offset="1" stopColor="#07080a" /></radialGradient>
          <filter id="earthSoft" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="2.2" /></filter>
        </defs>
        <rect x={0} y={0} width={W} height={H} fill="url(#earthGlow)" />
        <g opacity={0.5} stroke="#1a1d22" strokeWidth={0.5} vectorEffect="non-scaling-stroke" fill="none">
          {[-60, -30, 0, 30, 60].map((lat) => { const a = toXY(-180, lat), b = toXY(180, lat); return <line key={lat} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} vectorEffect="non-scaling-stroke" />; })}
        </g>
        <path d={LAND} fill="#1b1e24" stroke="#363b44" strokeWidth={0.6} vectorEffect="non-scaling-stroke" />
        <path d={BORDERS} fill="none" stroke="#2b2f36" strokeWidth={0.5} vectorEffect="non-scaling-stroke" />

        {/* arcs, then the packets riding them */}
        <g fill="none">
          {arcs.map((a) => {
            const on = isHl(a.place, [], [a.server]) || (hl?.startsWith("u:") && places.find((p) => placeKey(p) === a.place)?.devices.some((d) => d.uid === +hl.slice(2) && d.via.includes(a.server)));
            return (
              <g key={a.key} opacity={on ? 1 : 0.12} style={{ transition: "opacity .3s" }}>
                <path d={a.d} stroke={a.color} strokeWidth={a.w} strokeOpacity={0.45} vectorEffect="non-scaling-stroke" className="earth-arc" />
                <path d={a.d} stroke="#fff" strokeWidth={a.w * 0.9} strokeOpacity={0.85} vectorEffect="non-scaling-stroke" strokeLinecap="round" className="earth-packet" pathLength={100} />
              </g>
            );
          })}
        </g>

        {/* places */}
        {places.map((p) => {
          const [x, y] = toXY(p.lon, p.lat), key = placeKey(p);
          const r = (2.2 + 4.5 * Math.sqrt(p.devices.length / maxDev)) * s;
          const on = isHl(key, p.devices.map((d) => d.uid), p.devices.flatMap((d) => d.via));
          return (
            <g key={key} opacity={on ? 1 : 0.25} style={{ transition: "opacity .3s", cursor: "pointer" }}
              onPointerEnter={(e) => { const b = box.current!.getBoundingClientRect(); setTip({ x: e.clientX - b.left, y: e.clientY - b.top, p }); }}
              onPointerLeave={() => setTip(null)}
              onClick={(e) => { e.stopPropagation(); setHl?.(hl === key ? null : key); }}>
              <circle cx={x} cy={y} r={r * 2.4} fill="#ffb23f" opacity={0.18} filter="url(#earthSoft)" />
              <circle cx={x} cy={y} r={r} fill="#ffb23f" stroke="#08090b" strokeWidth={0.8} vectorEffect="non-scaling-stroke" className="earth-place" />
              {(k > 2.2 || p.devices.length >= Math.max(2, maxDev * 0.4)) && !compact && (
                <text x={x + r + 2 * s} y={y + 3.5 * s} fontSize={10.5 * s} fill="#ece9e2" stroke="#08090b" strokeWidth={2.6 * s} paintOrder="stroke" style={{ direction: "ltr" }}>{p.city || regionName(p.cc, lang)}</text>
              )}
            </g>
          );
        })}

        {/* servers: a diamond with a ring; relays dashed */}
        {servers.map((sv) => {
          const pin = pinOf.get(sv.id);
          if (!pin) return null;
          const c = serverColor(servers, sv.id), [x, y] = pin, z = 6 * s;
          const on = !hl || hl === "s:" + sv.id || !hl.startsWith("s:");
          return (
            <g key={sv.id} opacity={on ? 1 : 0.3} style={{ cursor: "pointer" }} onClick={(e) => { e.stopPropagation(); setHl?.(hl === "s:" + sv.id ? null : "s:" + sv.id); }}>
              <circle cx={x} cy={y} r={z * 2.2} fill="none" stroke={c} strokeOpacity={0.5} strokeWidth={1} vectorEffect="non-scaling-stroke" strokeDasharray={sv.role === "relay" ? "3 3" : undefined} className={sv.online ? "earth-ring" : undefined} />
              <path d={`M${x},${y - z} L${x + z},${y} L${x},${y + z} L${x - z},${y} Z`} fill={sv.online ? c : "#3a3d44"} stroke="#08090b" strokeWidth={1} vectorEffect="non-scaling-stroke" />
              {!compact && <text x={x} y={y - z * 2.6} textAnchor="middle" fontSize={10.5 * s} fontWeight={600} fill={c} stroke="#08090b" strokeWidth={2.8 * s} paintOrder="stroke" style={{ direction: "ltr" }}>{sv.name}</text>}
            </g>
          );
        })}
      </svg>

      {tip && (
        <div className="absolute z-10 pointer-events-none rounded-xl border border-hair2 bg-panel/95 px-3 py-2 text-[13px] shadow-xl max-w-[260px]"
          style={{ left: Math.min(tip.x + 14, (box.current?.clientWidth ?? 300) - 260), top: Math.max(8, tip.y - 10) }}>
          <div className="font-semibold"><bdi>{tip.p.city || t("mapUnknownCity")}</bdi> <span className="text-ink3 font-normal">· {regionName(tip.p.cc, lang)}</span></div>
          <div className="text-ink2">{n(tip.p.devices.length)} {t("mapStatDevices")}{sep}{n(new Set(tip.p.devices.map((d) => d.uid)).size)} {t("mapStatUsers")}</div>
          <div className="mt-1 flex flex-wrap gap-1">{[...new Set(tip.p.devices.map((d) => d.user))].slice(0, 8).map((u) => <span key={u} className="mono text-[11.5px] px-1.5 rounded bg-fg/[.06]" dir="ltr">{u}</span>)}</div>
          {tip.p.devices.some((d) => d.org) && <div className="mt-1 text-[12px] text-ink3">{[...new Set(tip.p.devices.map((d) => operatorName(d.asn, d.org, lang)).filter(Boolean))].slice(0, 3).join(" · ")}</div>}
        </div>
      )}

      <div className="absolute bottom-3 [inset-inline-start:12px] flex flex-col gap-1.5">
        <button className="btn-ico" aria-label={t("mapZoomIn")} onClick={() => zoomAt(1.5)}><Plus className="size-4" /></button>
        <button className="btn-ico" aria-label={t("mapZoomOut")} onClick={() => zoomAt(1 / 1.5)}><Minus className="size-4" /></button>
        <button className="btn-ico" aria-label={t("mapReset")} onClick={() => setView(FULL)}><RotateCcw className="size-4" /></button>
      </div>
      {!data && <div className="absolute inset-0 grid place-items-center text-ink3 text-[13px]"><span className="earth-scan">{t("mapLoading")}</span></div>}
    </div>
  );
}

/** The whole page: the map with its categorised side lists. */
export function EarthView({ height = "auto" }: { height?: number | string }) {
  const { t, lang, n, sep } = useI18n();
  const [data, setData] = useState<UserMap | null>(null);
  const [err, setErr] = useState("");
  const [hl, setHl] = useState<string | null>(null);
  const [tab, setTab] = useState<"country" | "server" | "network">("country");
  const [q, setQ] = useState("");

  useEffect(() => {
    let alive = true;
    const load = () => api<UserMap>("network/users").then((r) => { if (alive) { setData(r); setErr(""); } }).catch((e) => alive && setErr(e.message));
    load();
    const id = setInterval(load, 15000);
    return () => { alive = false; clearInterval(id); };
  }, []);

  const stats = useMemo(() => {
    const cc = new Set<string>(), cities = new Set<string>();
    for (const p of data?.places ?? []) { cc.add(p.cc); if (p.city) cities.add(placeKey(p)); }
    return { countries: cc.size, cities: cities.size };
  }, [data]);

  const byCountry = useMemo(() => {
    const m = new Map<string, { cc: string; devices: number; places: MapPlace[] }>();
    for (const p of data?.places ?? []) {
      const e = m.get(p.cc) ?? { cc: p.cc, devices: 0, places: [] };
      e.devices += p.devices.length; e.places.push(p); m.set(p.cc, e);
    }
    return [...m.values()].sort((a, b) => b.devices - a.devices);
  }, [data]);
  const byServer = useMemo(() => (data?.servers ?? []).map((s) => {
    const users = new Set<number>(); let devices = 0;
    for (const p of data!.places) for (const d of p.devices) if (d.via.includes(s.id)) { devices++; users.add(d.uid); }
    return { s, devices, users: users.size };
  }), [data]);
  const byNet = useMemo(() => {
    const m = new Map<string, { name: string; devices: number; users: Set<number> }>();
    for (const p of data?.places ?? []) for (const d of p.devices) {
      const name = operatorName(d.asn, d.org, lang) || t("mapUnknownNet");
      const e = m.get(name) ?? { name, devices: 0, users: new Set<number>() };
      e.devices++; e.users.add(d.uid); m.set(name, e);
    }
    return [...m.values()].sort((a, b) => b.devices - a.devices);
  }, [data, lang, t]);
  const found = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s || !data) return [];
    const m = new Map<number, string>();
    for (const p of data.places) for (const d of p.devices) if (d.user.toLowerCase().includes(s)) m.set(d.uid, d.user);
    return [...m.entries()].slice(0, 8);
  }, [q, data]);
  const max = (xs: number[]) => Math.max(1, ...xs);

  return (
    <div className="mach rounded-[16px] overflow-hidden grid lg:grid-cols-[minmax(0,1fr)_340px]">
      <div className="relative min-w-0 border-b border-hair lg:border-b-0 lg:border-e">
        <Earth data={data} hl={hl} setHl={setHl} height={height} />
        <div className="absolute top-3 [inset-inline-start:12px] flex flex-wrap gap-1.5 max-w-[80%]">
          <span className="stat-chip"><span className="led on live" />{n(data?.users ?? 0)} {t("mapStatUsers")}</span>
          <span className="stat-chip">{n(data?.devices ?? 0)} {t("mapStatDevices")}</span>
          <span className="stat-chip">{n(stats.countries)} {t("mapStatCountries")}</span>
          {data?.city && <span className="stat-chip">{n(stats.cities)} {t("mapStatCities")}</span>}
        </div>
      </div>
      <aside className="flex flex-col min-h-0 max-h-[640px] lg:max-h-none lg:h-0 lg:min-h-full">
        <div className="p-3 border-b border-hair">
          <label className="field-recess flex items-center gap-2 h-10 px-3"><Search className="size-4 text-ink3" /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("mapSearch")} className="bg-transparent outline-none flex-1 min-w-0 text-[14px]" /></label>
          {found.length > 0 && <div className="mt-2 flex flex-wrap gap-1.5">{found.map(([uid, name]) => (
            <button key={uid} onClick={() => setHl(hl === "u:" + uid ? null : "u:" + uid)} className={cx("chip-sm mono", hl === "u:" + uid && "on")} dir="ltr">{name}</button>
          ))}</div>}
          <div className="seg mt-3" role="tablist">
            {([["country", t("mapByCountry"), Globe2], ["server", t("mapByServer"), ServerIcon], ["network", t("mapByNetwork"), Signal]] as const).map(([id, label, Icon]) => (
              <button key={id} role="tab" aria-selected={tab === id} className={cx(tab === id && "on")} onClick={() => setTab(id)}><Icon className="size-3.5" />{label}</button>
            ))}
          </div>
        </div>
        <div className="overflow-y-auto overscroll-contain p-3 flex-1">
          {err && <div className="text-bad text-[13px]">{err}</div>}
          {data && data.devices === 0 && <p className="text-ink3 text-[13.5px] leading-7">{t("mapEmpty")}</p>}
          {tab === "country" && byCountry.map((c) => (
            <div key={c.cc} className="py-2 border-b border-hair last:border-0">
              <div className="flex items-center gap-2"><b className="font-semibold">{regionName(c.cc, lang)}</b><span className="ss ms-auto">{c.cc}</span><span className="text-[12.5px] text-ink2 num">{n(c.devices)}</span></div>
              <div className="segbar mt-1.5"><i style={{ width: (c.devices * 100) / max(byCountry.map((x) => x.devices)) + "%" }} /></div>
              <div className="mt-1.5 flex flex-col">
                {c.places.slice(0, 6).map((p) => (
                  <button key={placeKey(p)} onClick={() => setHl(hl === placeKey(p) ? null : placeKey(p))} className={cx("row-btn", hl === placeKey(p) && "on")}>
                    <span className="led am" /><bdi className="truncate">{p.city || t("mapUnknownCity")}</bdi>
                    <span className="ms-auto text-ink3 text-[12px] truncate max-w-[45%]" dir="ltr">{[...new Set(p.devices.map((d) => d.user))].join(", ")}</span>
                  </button>
                ))}
              </div>
            </div>
          ))}
          {tab === "server" && byServer.map(({ s, devices, users }) => (
            <button key={s.id} onClick={() => setHl(hl === "s:" + s.id ? null : "s:" + s.id)} className={cx("row-btn py-2.5", hl === "s:" + s.id && "on")}>
              <span className="size-3 rotate-45 rounded-[2px] shrink-0" style={{ background: s.online ? serverColor(data!.servers, s.id) : "#3a3d44" }} />
              <span className="min-w-0 text-start"><b className="block font-semibold truncate" dir="ltr">{s.name}</b><span className="block text-[12px] text-ink3 truncate">{s.place ? (s.place.city ? s.place.city + " · " : "") + regionName(s.place.cc, lang) : t("mapUnknownCity")}{s.role === "relay" ? " · " + t("mapRelay") : ""}</span></span>
              <span className="ms-auto text-end text-[12.5px] text-ink2 num whitespace-nowrap">{n(users)} {t("mapStatUsers")}<span className="block text-ink3">{n(devices)} {t("mapStatDevices")}</span></span>
            </button>
          ))}
          {tab === "network" && byNet.map((x) => (
            <div key={x.name} className="py-2 border-b border-hair last:border-0">
              <div className="flex items-center gap-2"><bdi className="font-semibold truncate">{x.name}</bdi><span className="ms-auto text-[12.5px] text-ink2 num whitespace-nowrap">{n(x.users.size)} {t("mapStatUsers")} · {n(x.devices)}</span></div>
              <div className="segbar mt-1.5"><i style={{ width: (x.devices * 100) / max(byNet.map((y) => y.devices)) + "%" }} /></div>
            </div>
          ))}
        </div>
        <div className="px-3 py-2.5 border-t border-hair text-[11.5px] text-ink3 leading-6">
          {data && (data.relayed > 0 || data.unplaced > 0) && <div>{data.relayed > 0 && t("mapRelayed").replace("{n}", n(data.relayed))}{data.relayed > 0 && data.unplaced > 0 && sep}{data.unplaced > 0 && t("mapUnplaced").replace("{n}", n(data.unplaced))}</div>}
          {data && !data.city && <div>{t("mapNoCity")}</div>}
          <div>{t("mapSource").replace("{src}", data?.source || "DB-IP")}</div>
        </div>
      </aside>
    </div>
  );
}

/** A small map on the overview: click through to the full page. */
export function EarthCard() {
  const { t, n, sep } = useI18n();
  const [data, setData] = useState<UserMap | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () => api<UserMap>("network/users").then((r) => alive && setData(r)).catch(() => {});
    load();
    const id = setInterval(load, 20000);
    return () => { alive = false; clearInterval(id); };
  }, []);
  return (
    <div className="mach rounded-[16px] overflow-hidden">
      <div className="flex items-center gap-2 px-4 pt-3 pb-2">
        <h3 className="text-[15px] font-semibold">{t("liveMap")}</h3>
        <span className="ss">LIVE</span><span className="led on live" />
        <span className="ms-auto text-[12.5px] text-ink2">{n(data?.users ?? 0)} {t("mapStatUsers")}{sep}{n(data?.places.length ?? 0)} {t("mapPlaces")}</span>
        <button className="link-btn" onClick={() => navigate("map")}>{t("mapOpen")}</button>
      </div>
      <Earth data={data} height={300} compact />
    </div>
  );
}
