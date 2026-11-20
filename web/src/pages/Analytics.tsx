import { useEffect, useMemo, useState } from "react";
import { Activity, ChartPie, Database, Network, Users as UsersIcon } from "lucide-react";
import { api, type Inbound, type Overview, type Preset, type User } from "../lib/api";
import { bytes, groupColor, groups, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Avatar, ErrorState, Meter, Panel, Segmented, Skeleton } from "../components/ui";
import { Donut, LiveChart, UsageChart } from "../components/charts";
import { shortName } from "../components/NetworkMap";
import { PageHead } from "../App";

/** Traffic analytics from real counters: periods, protocols, users, live. */
export function AnalyticsPage() {
  const { t, lang, n, pc } = useI18n();
  const [days, setDays] = useState(30);
  const [d, setD] = useState<Overview | null>(null);
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [err, setErr] = useState("");
  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>(`overview?days=${days}`).then((r) => alive && setD(r)).catch((e) => alive && setErr(e.message));
    load();
    const id = setInterval(load, 5000);
    return () => { alive = false; clearInterval(id); };
  }, [days]);
  useEffect(() => {
    api<Inbound[]>("inbounds").then(setInbounds).catch(() => {});
    api<Preset[]>("presets").then(setPresets).catch(() => {});
    api<User[]>("users").then(setUsers).catch(() => {});
  }, []);
  const byPreset = useMemo(() => Object.fromEntries(presets.map((p) => [p.id, p])), [presets]);

  const famParts = groups.map((g) => ({ label: t(g.label), color: g.color, value: inbounds.filter((i) => byPreset[i.preset]?.group === g.id).reduce((s, i) => s + i.up + i.down, 0) }));
  const famTotal = famParts.reduce((s, p) => s + p.value, 0);
  const protoRank = [...inbounds].sort((a, b) => b.up + b.down - (a.up + a.down)).slice(0, 8);
  const statusParts = (["active", "pending", "paused", "limited", "expired", "disabled"] as const).map((s) => ({ label: t(statusMeta[s].key), color: statusMeta[s].color, value: users.filter((u) => u.status === s).length }));
  const topUsers = [...users].sort((a, b) => b.used - a.used).slice(0, 10);

  if (!d) return err ? <ErrorState message={err} /> : <Skeleton className="h-96" />;

  return (
    <div>
      <PageHead title={t("analytics")} sub={t("analyticsSub")}
        action={<Segmented value={days} onChange={setDays} options={[{ value: 1, label: t("r24h") }, { value: 7, label: t("r7d") }, { value: 30, label: t("r30d") }, { value: 90, label: t("r90d") }]} />} />
      <div className="grid gap-[var(--gap)] xl:grid-cols-12">
        <Panel className="xl:col-span-12" icon={<Database className="size-4" />} title={t("trafficByPeriod")} sub={t("chartHelp")}>
          <UsageChart series={d.series} days={days} height={260} exportName={`dunvex-traffic-${days}d`} />
        </Panel>
        <Panel className="xl:col-span-7" icon={<Activity className="size-4" />} title={t("liveTraffic")} sub={t("liveSub")}>
          <LiveChart live={d.live.slice(-60)} height={200} />
        </Panel>
        <Panel className="xl:col-span-5" icon={<ChartPie className="size-4" />} title={t("protocolDistribution")} sub={t("allTime")}>
          <div className="flex flex-wrap items-center gap-6">
            <Donut parts={famParts} center={<div><div className="text-[15px] font-bold num">{bytes(famTotal, lang, 0)}</div><div className="text-[11px] text-ink3">{t("total")}</div></div>} />
            <ul className="flex-1 min-w-40 flex flex-col gap-2.5">
              {famParts.map((p) => (
                <li key={p.label} className="flex items-center gap-2 text-[13px]">
                  <i className="size-2.5 rounded-full" style={{ background: p.color }} />
                  <span className="flex-1">{p.label}</span>
                  <span className="num text-ink2">{pc(famTotal ? (p.value / famTotal) * 100 : 0, 0)}</span>
                </li>
              ))}
            </ul>
          </div>
        </Panel>
        <Panel className="xl:col-span-6" icon={<Network className="size-4" />} title={t("protocolComparison")}>
          <ul className="flex flex-col gap-3">
            {protoRank.map((i) => {
              const p = byPreset[i.preset];
              return (
                <li key={i.id}>
                  <div className="flex items-center gap-2 text-[13px] mb-1">
                    <i className="size-2 rounded-full" style={{ background: p ? groupColor(p.group) : "var(--text3)" }} />
                    <span className="truncate" dir="ltr">{i.remark || shortName[i.preset] || i.name}</span>
                    <span className="mono text-[11px] text-ink3" dir="ltr">:{i.port}</span>
                    <span className="ms-auto num text-ink2">{bytes(i.up + i.down, lang)}</span>
                  </div>
                  <Meter value={i.up + i.down} max={protoRank[0] ? protoRank[0].up + protoRank[0].down : 0} plain height={5} color={p ? groupColor(p.group) : undefined} />
                </li>
              );
            })}
            {protoRank.length === 0 && <li className="text-ink3 text-sm py-6 text-center">—</li>}
          </ul>
        </Panel>
        <Panel className="xl:col-span-6" icon={<UsersIcon className="size-4" />} title={t("userConsumption")} sub={t("currentPeriod")}>
          <ul className="flex flex-col gap-3">
            {topUsers.map((u) => (
              <li key={u.id}>
                <a href={`#/users/${u.id}`} className="flex items-center gap-2.5 text-[13px] mb-1 group">
                  <Avatar name={u.name} size={24} online={u.online} />
                  <span className="truncate group-hover:text-primary2" dir="ltr">{u.name}</span>
                  <span className="ms-auto num text-ink2">{bytes(u.used, lang)}{u.dataLimit ? <span className="text-ink3"> / {bytes(u.dataLimit, lang)}</span> : null}</span>
                </a>
                <Meter value={u.used} max={topUsers[0]?.used || 0} plain height={5} />
              </li>
            ))}
            {topUsers.length === 0 && <li className="text-ink3 text-sm py-6 text-center">—</li>}
          </ul>
        </Panel>
        <Panel className="xl:col-span-12" icon={<ChartPie className="size-4" />} title={t("userStatus")}>
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
            {statusParts.map((p) => (
              <div key={p.label} className="rounded-2xl border border-hair bg-fg/[.02] p-3">
                <div className="flex items-center gap-2 text-[12px] text-ink2"><i className="size-2 rounded-full" style={{ background: p.color }} />{p.label}</div>
                <div className="text-[22px] font-bold num mt-1">{n(p.value)}</div>
              </div>
            ))}
          </div>
        </Panel>
      </div>
    </div>
  );
}
