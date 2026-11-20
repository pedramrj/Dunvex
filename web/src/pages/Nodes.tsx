import { useEffect, useState } from "react";
import { Cpu, HardDrive, LayoutGrid, List, MemoryStick, Network, Server, Users as UsersIcon } from "lucide-react";
import { api, type Geo, type Overview } from "../lib/api";
import { bytes, duration, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Dot, ErrorState, KV, Meter, Panel, Ring, ringColor, Segmented, Skeleton } from "../components/ui";
import { Spark } from "../components/charts";
import { Logo } from "../components/Logo";
import { PageHead } from "../App";

/** Dunvex runs one server today; this page shows it as a node with real metrics. */
export function NodesPage() {
  const { t, lang, n, pc } = useI18n();
  const [d, setD] = useState<Overview | null>(null);
  const [geo, setGeo] = useState<Geo | null>(null);
  const [err, setErr] = useState("");
  const [mode, setMode] = useState<"grid" | "list">("grid");
  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>("overview?days=1").then((r) => alive && setD(r)).catch((e) => alive && setErr(e.message));
    load();
    const id = setInterval(load, 5000);
    api<Geo>("network/geo").then(setGeo).catch(() => {});
    return () => { alive = false; clearInterval(id); };
  }, []);
  if (!d) return err ? <ErrorState message={err} /> : <Skeleton className="h-96" />;
  const s = d.system;
  const pct = (a: number, b: number) => (b ? (a / b) * 100 : 0);
  const problems = Object.keys(d.coreErrors ?? {}).length + (d.inboundErrs ?? 0);
  const hist = d.history.slice(-90);
  const live = d.live[d.live.length - 1] ?? { up: 0, down: 0 };

  const card = (
    <Panel className="max-w-4xl">
      <div className="flex flex-wrap items-center gap-4 mb-5">
        <span className="grid place-items-center size-14 rounded-2xl bg-primary/12 border border-primary/30"><Logo part="emblem" width={42} /></span>
        <div className="min-w-0">
          <div className="text-[18px] font-bold" dir="ltr">{s.hostname || "Dunvex"}</div>
          <div className="text-[12.5px] text-ink3"><span className="mono sensitive" dir="ltr">{s.publicIp}</span>{geo ? ` · ${geo.city ? geo.city + ", " : ""}${geo.country}` : ""}</div>
        </div>
        <div className="ms-auto flex flex-wrap gap-2">
          <Badge color={problems ? "var(--bad)" : "var(--ok)"}><Dot color={problems ? "var(--bad)" : "var(--ok)"} size={6} pulse={!problems} />{problems ? t("issues") : t("healthy")}</Badge>
          <Badge color="var(--text2)">node-1</Badge>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-3 mb-5">
        {[{ k: t("cpu"), v: s.cpu, i: <Cpu className="size-3.5" /> }, { k: t("ram"), v: pct(s.memUsed, s.memTotal), i: <MemoryStick className="size-3.5" /> }, { k: t("disk"), v: pct(s.diskUsed, s.diskTotal), i: <HardDrive className="size-3.5" /> }].map((x) => (
          <div key={x.k} className="flex items-center gap-3 rounded-2xl border border-hair bg-fg/[.02] p-3">
            <Ring value={x.v} size={54} stroke={6} color={ringColor(x.v)}><span className="text-[11.5px] font-bold num">{pc(x.v, 0)}</span></Ring>
            <span className="text-[12.5px] text-ink2 inline-flex items-center gap-1.5">{x.i}{x.k}</span>
          </div>
        ))}
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-5">
        <KV label={t("uptime")}>{duration(s.uptime, lang)}</KV>
        <KV label={t("lastHeartbeat")}>{t("justNow")}</KV>
        <KV label={t("bandwidth")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(live.down, lang)}</span><span>↑ {speed(live.up, lang)}</span></span></KV>
        <KV label={t("netNow")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(s.netDown, lang)}</span><span>↑ {speed(s.netUp, lang)}</span></span></KV>
        <KV label={t("today")}>{bytes(d.today.down + d.today.up, lang)}</KV>
        <KV label={t("thisMonth")}>{bytes(d.month.down + d.month.up, lang)}</KV>
        <KV label={t("users")}><span className="inline-flex items-center gap-1"><UsersIcon className="size-3.5 text-ink3" /><span className="num">{n(d.users)} · {n(d.online)} {t("online")}</span></span></KV>
        <KV label={t("protocols")}><span className="inline-flex items-center gap-1"><Network className="size-3.5 text-ink3" /><span className="num">{n(d.inboundsOn)} / {n(d.inbounds)}</span></span></KV>
      </div>
      <div className="grid sm:grid-cols-2 gap-4">
        <div><div className="text-[11.5px] text-ink3 mb-1">{t("cpu")} · {t("last6min")}</div><Spark values={hist.map((h) => h.cpu)} color="var(--warn)" height={44} /></div>
        <div><div className="text-[11.5px] text-ink3 mb-1">{t("ram")} · {t("last6min")}</div><Spark values={hist.map((h) => h.mem)} color="var(--primary2)" height={44} /></div>
      </div>
      <div className="mt-5 flex flex-wrap gap-2">
        {d.cores.map((c) => (
          <Badge key={c.name} color={c.running ? "var(--ok)" : "var(--text3)"}><Dot color={c.running ? "var(--ok)" : "var(--text3)"} size={6} /><span dir="ltr">{c.name} {c.version}</span></Badge>
        ))}
      </div>
      <div className="mt-4"><Meter value={s.diskUsed} max={s.diskTotal} /></div>
    </Panel>
  );

  return (
    <div>
      <PageHead title={t("nodes")} sub={t("nodesSub")} action={<Segmented value={mode} onChange={setMode} options={[{ value: "grid", label: <LayoutGrid className="size-4" /> }, { value: "list", label: <List className="size-4" /> }]} />} />
      {mode === "grid" ? card : (
        <div className="glass rounded-[18px] overflow-x-auto">
          <table className="w-full text-[13px]">
            <thead className="text-ink3 text-[12px]"><tr className="border-b border-hair">{[t("name"), t("status"), "IP", t("cpu"), t("ram"), t("disk"), t("uptime"), t("users")].map((h) => <th key={h} className="text-start font-medium px-4 h-11">{h}</th>)}</tr></thead>
            <tbody>
              <tr>
                <td className="px-4 h-14"><span className="inline-flex items-center gap-2"><Server className="size-4 text-primary2" /><span dir="ltr">{s.hostname}</span></span></td>
                <td className="px-4"><Badge color={problems ? "var(--bad)" : "var(--ok)"}>{problems ? t("issues") : t("healthy")}</Badge></td>
                <td className="px-4 mono sensitive" dir="ltr">{s.publicIp}</td>
                <td className="px-4 num">{pc(s.cpu, 0)}</td>
                <td className="px-4 num">{pc(pct(s.memUsed, s.memTotal), 0)}</td>
                <td className="px-4 num">{pc(pct(s.diskUsed, s.diskTotal), 0)}</td>
                <td className="px-4">{duration(s.uptime, lang)}</td>
                <td className="px-4 num">{n(d.users)}</td>
              </tr>
            </tbody>
          </table>
        </div>
      )}
      <p className="text-[12.5px] text-ink3 mt-4 max-w-2xl">{t("multiNodeNote")}</p>
    </div>
  );
}
