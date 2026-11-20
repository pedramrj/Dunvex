import { useId, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import type { Group, Line } from "../lib/api";
import { bytes, groups, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { usePrefs } from "../lib/prefs";
import { Logo } from "./Logo";

export const shortName: Record<string, string> = {
  "vless-xdns": "XDNS", "mtproto": "Telegram", "amneziawg": "AmneziaWG", "mieru": "Mieru", "mieru-udp": "Mieru UDP",
  "vless-reality-vision": "Vision", "vless-reality-universal": "Vision U", "vless-reality-xhttp": "XHTTP", "vless-reality-grpc": "gRPC", "trojan-reality": "Trojan",
  "vless-xhttp-tls": "XHTTP", "vless-ws-tls": "WS", "vless-httpupgrade-tls": "HTTPUpgrade", "vless-grpc-tls": "gRPC",
  "trojan-ws-tls": "Trojan WS", "vmess-ws-tls": "VMess", "vmess-ws": "VMess WS", "trojan-tls": "Trojan", "vless-enc-xhttp": "PQ-XHTTP",
  hysteria2: "Hysteria2", tuic: "TUIC", "hysteria2-sb": "Hy2 Masq", wireguard: "WireGuard", anytls: "AnyTLS", shadowtls: "ShadowTLS",
  naive: "Naive", "shadowsocks-2022": "SS-2022", shadowsocks: "SS", "vmess-kcp": "mKCP", socks: "SOCKS5", http: "HTTP",
};

const label = (l: Line) => l.remark || shortName[l.preset] || l.name;
/** Map labels stay short so neighbours never touch; the hover card shows the full name. */
const mapLabel = (l: Line) => { const s = label(l); return s.length > 12 ? s.slice(0, 11) + "…" : s; };

// The four lines leave the central interchange (viewBox 1000×360).
const geo: Record<Group, { path: string; from: number; to: number; y: number; flowDir: 1 | -1 }> = {
  reality: { path: "M566 166 H640 L700 106 H972", from: 714, to: 956, y: 106, flowDir: 1 },
  udp: { path: "M566 194 H640 L700 254 H972", from: 714, to: 956, y: 254, flowDir: 1 },
  cdn: { path: "M434 166 H360 L300 106 H28", from: 286, to: 44, y: 106, flowDir: -1 },
  classic: { path: "M434 194 H360 L300 254 H28", from: 286, to: 44, y: 254, flowDir: -1 },
};

/** Seconds for one packet to travel a line: faster with more traffic. */
function packetDur(rate: number) {
  if (rate <= 0) return 0;
  const l = Math.log10(Math.max(rate, 1000));
  return Math.max(0.9, 6 - (l - 3) * 1.1);
}

export function NetworkMap({ lines, online, server, onStation, onAdd, height }: { lines: Line[]; online: number; server: string; onStation: (id: number) => void; onAdd: (g: Group) => void; height?: number }) {
  const { t, lang, n } = useI18n();
  const { prefs } = usePrefs();
  const uid = useId().replace(/:/g, "");
  const [hover, setHover] = useState<number | null>(null);
  const reduced = prefs.motion === "reduced" || (typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
  const byGroup = useMemo(() => {
    const m: Record<Group, Line[]> = { reality: [], cdn: [], udp: [], classic: [] };
    for (const l of lines) m[l.group]?.push(l);
    return m;
  }, [lines]);
  const hovered = lines.find((l) => l.id === hover);

  const stations = (g: Group) => {
    const list = byGroup[g].slice(0, 6);
    const { from, to, y } = geo[g];
    return list.map((l, i) => ({ l, x: list.length === 1 ? from + (to - from) * 0.42 : from + ((to - from) * i) / (list.length - 1), y, labelUp: i % 2 === 0 }));
  };

  return (
    <div>
      <div className="relative hidden md:block">
        <svg viewBox="0 0 1000 360" className="w-full h-auto block" style={height ? { height } : undefined} role="img" aria-label={t("network")}>
          <defs>
            <filter id={`glow${uid}`} x="-20%" y="-50%" width="140%" height="200%">
              <feGaussianBlur stdDeviation="5" result="b" />
              <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
            <radialGradient id={`hub${uid}`}>
              <stop offset="0%" stopColor="var(--primary)" stopOpacity={0.35} />
              <stop offset="100%" stopColor="var(--primary)" stopOpacity={0} />
            </radialGradient>
          </defs>
          <circle cx={500} cy={180} r={150} fill={`url(#hub${uid})`} />
          {groups.map(({ id, color }) => {
            const g = geo[id];
            const list = byGroup[id];
            const empty = list.length === 0;
            const rate = list.reduce((s, l) => s + (l.enabled ? l.rateDown + l.rateUp : 0), 0);
            const dur = packetDur(rate);
            const pid = `p${uid}${id}`;
            const packets = !empty && dur > 0 && !reduced ? Math.min(4, 1 + Math.floor(Math.log10(Math.max(rate, 1000)) - 3)) : 0;
            return (
              <g key={id}>
                <path id={pid} d={g.path} fill="none" stroke={color} strokeWidth={empty ? 3 : 6} strokeLinecap="round" strokeLinejoin="round" opacity={empty ? 0.25 : 0.95}
                  strokeDasharray={empty ? "2 10" : undefined} filter={empty ? undefined : `url(#glow${uid})`} />
                {/* data packets travel from the server outwards, faster with more traffic */}
                {Array.from({ length: packets }).map((_, k) => (
                  <circle key={k} r={3.6} fill="#fff" opacity={0.95} style={{ filter: `drop-shadow(0 0 6px ${color})` }}>
                    <animateMotion dur={`${dur}s`} begin={`${(dur / packets) * k}s`} repeatCount="indefinite" rotate="auto">
                      <mpath href={`#${pid}`} />
                    </animateMotion>
                  </circle>
                ))}
                {empty ? (
                  <g className="cursor-pointer" onClick={() => onAdd(id)} role="button" aria-label={t("addProtocol")}>
                    <circle cx={g.to} cy={g.y} r={13} fill="var(--bg)" stroke={color} strokeWidth={2} strokeDasharray="3 3" />
                    <path d={`M${g.to - 5} ${g.y} h10 M${g.to} ${g.y - 5} v10`} stroke={color} strokeWidth={2} strokeLinecap="round" />
                  </g>
                ) : (
                  stations(id).map(({ l, x, y, labelUp }) => {
                    const active = l.rateDown + l.rateUp > 0;
                    const stroke = !l.enabled ? "var(--text3)" : l.error ? "var(--bad)" : color;
                    return (
                      <g key={l.id} className="cursor-pointer" onMouseEnter={() => setHover(l.id)} onMouseLeave={() => setHover(null)} onClick={() => onStation(l.id)}>
                        <circle cx={x} cy={y} r={hover === l.id ? 12.5 : 10} fill="var(--bg)" stroke={stroke} strokeWidth={4} strokeDasharray={!l.enabled ? "3 3" : undefined} style={{ transition: "r .18s" }} />
                        {active && l.enabled && <circle cx={x} cy={y} r={3.6} fill="var(--text)" />}
                        {l.error && <circle cx={x} cy={y} r={4} fill="var(--bad)" />}
                        <text x={x} y={labelUp ? y - 36 : y + 32} textAnchor="middle" direction="ltr" fill={l.enabled ? "var(--text)" : "var(--text3)"} fontSize={mapLabel(l).length > 9 ? 12.5 : 14} fontWeight={600} style={{ fontFamily: "DVX Geist, sans-serif" }}>{mapLabel(l)}</text>
                        <text x={x} y={labelUp ? y - 21 : y + 46} textAnchor="middle" direction="ltr" fill="var(--text3)" fontSize={11.5} style={{ fontFamily: "DVX Mono, monospace" }}>:{l.port}</text>
                      </g>
                    );
                  })
                )}
                {list.length > 6 && <text x={g.flowDir === 1 ? 990 : 10} y={g.y + 4} textAnchor={g.flowDir === 1 ? "end" : "start"} direction="ltr" fill="var(--text2)" fontSize={12}>+{list.length - 6}</text>}
              </g>
            );
          })}
          {/* the interchange: this server, marked with the emblem */}
          <rect x={430} y={142} width={140} height={76} rx={38} fill="var(--bg)" stroke="var(--border2)" strokeWidth={2} />
          <rect x={430} y={142} width={140} height={76} rx={38} fill="none" stroke="var(--primary2)" strokeWidth={2.5} opacity={0.8} />
          <foreignObject x={442} y={150} width={44} height={60}>
            <div className="w-full h-full grid place-items-center"><Logo part="emblem" width={42} /></div>
          </foreignObject>
          <text x={490} y={175} textAnchor="start" direction="ltr" fill="var(--text)" fontSize={12.5} fontWeight={700} style={{ fontFamily: "DVX Geist, sans-serif" }}>
            {server.length > 9 ? server.slice(0, 8) + "…" : server}
          </text>
          <text x={490} y={196} textAnchor="start" fill="var(--ok)" fontSize={11.5} style={{ fontFamily: "DVX Geist, sans-serif" }}>● {n(online)} {t("online")}</text>
        </svg>
        {hovered && <StationCard l={hovered} lang={lang} />}
      </div>

      {/* phones: lines as vertical strips */}
      <div className="md:hidden flex flex-col gap-4">
        <div className="flex items-center gap-3 rounded-xl border border-hair px-3 py-2.5">
          <Logo part="emblem" height={22} />
          <span className="font-semibold truncate" dir="ltr">{server}</span>
          <span className="ms-auto text-online text-[13px] num">● {n(online)} {t("online")}</span>
        </div>
        {groups.map(({ id, color, label: lk }) => {
          const list = byGroup[id];
          return (
            <div key={id} className="relative ps-6">
              <span className="absolute top-1 bottom-1 [inset-inline-start:7px] w-1.5 rounded-full" style={{ background: color, opacity: list.length ? 1 : 0.3, boxShadow: list.length ? `0 0 10px ${color}` : undefined }} />
              <div className="text-[13px] font-semibold mb-1.5" style={{ color }}>{t(lk)}</div>
              {list.length === 0 ? (
                <button onClick={() => onAdd(id)} className="text-ink3 text-[13px] inline-flex items-center gap-1 h-8"><Plus className="size-4" /> {t("addProtocol")}</button>
              ) : (
                list.map((l) => (
                  <button key={l.id} onClick={() => onStation(l.id)} className="relative w-full flex items-center gap-3 h-11 text-start">
                    <span className="absolute [inset-inline-start:-22px] size-3.5 rounded-full bg-ground border-[3px]" style={{ borderColor: !l.enabled ? "var(--text3)" : l.error ? "var(--bad)" : color }} />
                    <span className={l.enabled ? "font-medium" : "text-ink3"} dir="ltr">{label(l)}</span>
                    <bdi dir="ltr" className="text-ink3 text-[12px] mono">:{l.port}</bdi>
                    <span className="ms-auto text-[12px] text-ink2 num">{speed(l.rateDown + l.rateUp, lang)}</span>
                  </button>
                ))
              )}
            </div>
          );
        })}
      </div>

      <div className="hidden md:flex flex-wrap items-center gap-x-6 gap-y-2 mt-2 text-[13px]">
        {groups.map(({ id, color, label: lk }) => {
          const list = byGroup[id];
          const rate = list.reduce((s, l) => s + l.rateDown + l.rateUp, 0);
          return (
            <span key={id} className="inline-flex items-center gap-2">
              <i className="h-1.5 w-5 rounded-full" style={{ background: color, opacity: list.length ? 1 : 0.35 }} />
              <span className={list.length ? "text-ink" : "text-ink3"}>{t(lk)}</span>
              <span className="text-ink3 num">{list.length ? speed(rate, lang) : "—"}</span>
            </span>
          );
        })}
      </div>
    </div>
  );
}

function StationCard({ l, lang }: { l: Line; lang: "fa" | "en" }) {
  const g = geo[l.group];
  const left = ((g.from + g.to) / 2 / 1000) * 100;
  return (
    <div className="pop absolute pointer-events-none rounded-xl glass-strong px-3.5 py-2.5 text-[12.5px] min-w-52"
      style={{ left: `${left}%`, top: g.y < 180 ? "52%" : "6%", transform: "translateX(-50%)" }}>
      <div className="font-semibold text-[13.5px] mb-1" dir="ltr">{l.name}</div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-0.5 text-ink2">
        <span>{lang === "fa" ? "پورت" : "Port"}</span><span className="mono text-ink text-end">{l.port}</span>
        <span>{lang === "fa" ? "سرعت" : "Speed"}</span><span className="num text-ink text-end">{speed(l.rateDown + l.rateUp, lang)}</span>
        <span>{lang === "fa" ? "کل ترافیک" : "Total"}</span><span className="num text-ink text-end">{bytes(l.up + l.down, lang)}</span>
      </div>
      {l.error && <div className="mt-1.5 text-bad">{l.error}</div>}
    </div>
  );
}
