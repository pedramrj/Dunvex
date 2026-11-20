import { useEffect, useState } from "react";
import {
  Activity, AlertTriangle, ArrowDown, ArrowUp, CalendarDays, CheckCircle2, Cpu, Database, HardDrive, KeyRound, MemoryStick, Network, Server,
  Settings2, Smartphone, Sparkles, User as UserIcon, Users as UsersIcon, Wifi, Zap,
} from "lucide-react";
import { api, type Event, type Group, type Overview } from "../lib/api";
import { ago, bytes, cx, duration, speed, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Avatar, Badge, Dot, Meter, Panel, Ring, ringColor, Skeleton, StatCard } from "../components/ui";
import { NetworkMap } from "../components/NetworkMap";
import { LiveChart, Spark, StackBar, UsageChart } from "../components/charts";
import { NewUserButton, PageHead, can, navigate, useSession } from "../App";

export function OverviewPage() {
  const { t, lang, n } = useI18n();
  const { me } = useSession();
  const [d, setD] = useState<Overview | null>(null);
  const [events, setEvents] = useState<Event[]>([]);
  const isAdmin = can(me.admin.role, "admin");
  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>("overview").then((r) => alive && setD(r)).catch(() => {});
    const loadEvents = () => isAdmin && api<Event[]>("events").then((r) => alive && setEvents(r.slice(0, 8))).catch(() => {});
    load();
    loadEvents();
    const id = setInterval(load, 5000);
    const id2 = setInterval(loadEvents, 15000);
    return () => {
      alive = false;
      clearInterval(id);
      clearInterval(id2);
    };
  }, [isAdmin]);

  if (!d) return <OverviewSkeleton />;
  if (d.role === "reseller") return <ResellerOverview d={d} />;

  const sys = d.system;
  const live = d.live.slice(-60);
  const last = live[live.length - 1] ?? { up: 0, down: 0 };
  const hist = d.history.slice(-90);
  const server = sys.hostname || sys.publicIp || "Dunvex";
  const coreErr = Object.entries(d.coreErrors ?? {});
  const problems = coreErr.length + (d.inboundErrs ?? 0);
  const pct = (a: number, b: number) => (b > 0 ? (a / b) * 100 : 0);
  const statusParts = (["active", "pending", "paused", "limited", "expired", "disabled"] as const).map((s) => ({ value: d.statuses[s] ?? 0, color: statusMeta[s].color, label: t(statusMeta[s].key) }));

  return (
    <div>
      <PageHead
        eyebrow={<span>{t("welcome")}{lang === "fa" ? "، " : ", "}<bdi dir="ltr" className="text-ink2">{me.admin.username}</bdi></span>}
        title={t("overview")}
        sub={
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="inline-flex items-center gap-1.5"><Server className="size-3.5 text-ink3" /><bdi className="mono text-[12.5px]" dir="ltr">{sys.publicIp || "—"}</bdi></span>
            <span className="text-ink3">·</span>
            <span>{sys.os}</span>
            <span className="text-ink3">·</span>
            <span>{t("uptime")}: {duration(sys.uptime, lang)}</span>
          </span>
        }
        action={
          <>
            {problems === 0 ? (
              <Badge color="var(--color-online)" icon={<CheckCircle2 className="size-3.5" />} className="h-8 px-3">{t("allGood")}</Badge>
            ) : (
              <Badge color="var(--color-bad)" icon={<AlertTriangle className="size-3.5" />} className="h-8 px-3">{t("issues")} · {n(problems)}</Badge>
            )}
            <NewUserButton />
          </>
        }
      />

      {coreErr.length > 0 && (
        <div className="rise mb-5 flex gap-3 rounded-2xl border border-bad/40 bg-bad/[.07] px-4 py-3 text-[13.5px]">
          <AlertTriangle className="size-5 text-bad shrink-0 mt-0.5" />
          <div className="min-w-0">
            {coreErr.map(([k, v]) => (
              <div key={k} className="break-words"><b dir="ltr">{k}</b>: <bdi dir="ltr" className="text-ink2 mono text-[12px]">{v}</bdi></div>
            ))}
          </div>
        </div>
      )}

      <div className="stagger grid gap-4 xl:grid-cols-12">
        {/* live traffic */}
        <Panel className="xl:col-span-8" icon={<Activity className="size-4" />} title={t("liveTraffic")} sub={lang === "fa" ? "۵ دقیقهٔ اخیر · هر ۵ ثانیه" : "last 5 minutes · every 5 s"}
          action={<span className="inline-flex items-center gap-2 text-[12px] text-online"><Dot color="var(--color-online)" pulse size={7} />LIVE</span>}>
          <div className="flex flex-wrap gap-x-8 gap-y-3 mb-3">
            <Speed icon={<ArrowDown className="size-4" />} color="#6ea4ff" label={t("download")} value={speed(last.down, lang)} />
            <Speed icon={<ArrowUp className="size-4" />} color="#a68cff" label={t("upload")} value={speed(last.up, lang)} />
            <div className="ms-auto text-end">
              <div className="text-[11.5px] text-ink3">{t("onlineNow")}</div>
              <div className="text-[22px] font-bold num text-online leading-tight">{n(d.online)} <span className="text-[13px] text-ink3 font-normal">/ {n(d.users)}</span></div>
            </div>
          </div>
          <LiveChart live={live} height={170} />
        </Panel>

        {/* server health */}
        <Panel className="xl:col-span-4" icon={<Cpu className="size-4" />} title={t("serverHealth")} sub={<span dir="ltr" className="mono">{server}</span>}>
          <div className="grid grid-cols-3 gap-2">
            <Gauge label={t("cpu")} value={sys.cpu} text={`${n(sys.cpu, 0)}%`} sub={`${n(sys.cores)} ${lang === "fa" ? "هسته" : "cores"}`} />
            <Gauge label={t("ram")} value={pct(sys.memUsed, sys.memTotal)} text={`${n(pct(sys.memUsed, sys.memTotal), 0)}%`} sub={bytes(sys.memTotal, lang, 0)} />
            <Gauge label={t("disk")} value={pct(sys.diskUsed, sys.diskTotal)} text={`${n(pct(sys.diskUsed, sys.diskTotal), 0)}%`} sub={bytes(sys.diskTotal, lang, 0)} />
          </div>
          <div className="mt-4">
            <div className="flex items-center justify-between text-[11.5px] text-ink3 mb-1"><span>{t("cpu")} · 6 min</span><span className="num">{n(Math.max(0, ...hist.map((h) => h.cpu)), 0)}% max</span></div>
            <Spark values={hist.map((h) => h.cpu)} color="#f2a93b" height={36} />
          </div>
          <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5 text-[12.5px]">
            <Row k={t("netNow")} v={<span className="num">↓{speed(sys.netDown, lang)} ↑{speed(sys.netUp, lang)}</span>} />
            <Row k={t("connections")} v={<span className="num">TCP {n(sys.tcp)} · UDP {n(sys.udp)}</span>} />
            <Row k={t("load")} v={<span className="num mono text-[12px]" dir="ltr">{(sys.load ?? []).map((x) => x.toFixed(2)).join("  ") || "—"}</span>} />
            <Row k={t("swap")} v={<span className="num">{sys.swapTotal ? `${bytes(sys.swapUsed, lang)} / ${bytes(sys.swapTotal, lang)}` : "—"}</span>} />
          </dl>
        </Panel>

        {/* KPIs */}
        <div className="xl:col-span-12 grid gap-4 grid-cols-2 lg:grid-cols-4">
          <StatCard icon={<Wifi className="size-[18px]" />} accent="#3ddc97" label={t("onlineNow")} value={<span className="text-online">{n(d.online)}</span>}
            sub={<span className="inline-flex items-center gap-1"><Smartphone className="size-3.5" />{n(d.devices ?? 0)} {t("devicesNow")}</span>}>
            <Spark values={live.map((s) => s.down + s.up)} color="#3ddc97" height={30} />
          </StatCard>
          <StatCard icon={<UsersIcon className="size-[18px]" />} accent="#4c8dff" label={t("users")} value={n(d.users)}
            sub={<span>{n((d.statuses.active ?? 0) + (d.statuses.pending ?? 0))} {t("active")}</span>}>
            <StackBar parts={statusParts} />
            <div className="flex flex-wrap gap-x-2.5 gap-y-0.5 mt-2 text-[11px] text-ink3">
              {statusParts.filter((p) => p.value > 0).map((p) => <span key={p.label} className="inline-flex items-center gap-1"><Dot color={p.color} size={6} />{n(p.value)}</span>)}
            </div>
          </StatCard>
          <StatCard icon={<Zap className="size-[18px]" />} accent="#f2a93b" label={t("today")} value={bytes(d.today.down + d.today.up, lang)}
            sub={<span>↓ {bytes(d.today.down, lang)} · ↑ {bytes(d.today.up, lang)}</span>}>
            <Meter value={d.today.down} max={d.today.down + d.today.up} plain color="linear-gradient(90deg,#f2c94c,#f2a93b)" />
          </StatCard>
          <StatCard icon={<CalendarDays className="size-[18px]" />} accent="#9b7bff" label={t("thisMonth")} value={bytes(d.month.down + d.month.up, lang)}
            sub={<span>{t("lifetime")}: {bytes(d.lifetime, lang)}</span>}>
            <Meter value={d.month.down + d.month.up} max={Math.max(d.lifetime, 1)} plain color="linear-gradient(90deg,#4c8dff,#9b7bff)" />
          </StatCard>
        </div>

        {/* network map */}
        <Panel className="xl:col-span-12" icon={<Network className="size-4" />} title={t("network")}
          sub={<span className="num">{n(d.inboundsOn)} / {n(d.inbounds)} {t("stations")}</span>}
          action={isAdmin && <a href="#/protocols/add" className="text-[13px] text-[#8db6ff] hover:text-white">{t("addProtocol")} +</a>}>
          <NetworkMap lines={d.lines} online={d.online} server={server} onStation={(id) => navigate("protocols", String(id))} onAdd={(g: Group) => navigate("protocols", "add-" + g)} />
        </Panel>

        {/* usage */}
        <Panel className="xl:col-span-8" icon={<Database className="size-4" />} title={t("usage30")}>
          <UsageChart series={d.series} />
        </Panel>
        <TopUsers top={d.top} className="xl:col-span-4" />

        {/* cores + events */}
        <Panel className="xl:col-span-4" icon={<Server className="size-4" />} title={t("cores")}>
          <ul className="flex flex-col gap-3">
            {d.cores.map((c) => (
              <li key={c.name} className="flex items-center gap-3 rounded-2xl bg-white/[.03] border border-white/[.06] p-3">
                <span className={cx("grid place-items-center size-10 rounded-xl", c.running ? "bg-online/12 text-online" : "bg-white/[.05] text-ink3")}>
                  {c.name === "xray" ? <Sparkles className="size-5" /> : <HardDrive className="size-5" />}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="font-semibold text-[13.5px]" dir="ltr">{c.name} <span className="mono text-[11.5px] text-ink3 font-normal">{c.version || "—"}</span></div>
                  <div className="text-[12px] text-ink3">{c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.installed ? t("stopped") : t("notInstalled")}</div>
                </div>
                <Dot color={c.running ? "var(--color-online)" : c.installed ? "var(--color-ink3)" : "var(--color-bad)"} pulse={c.running} />
              </li>
            ))}
          </ul>
        </Panel>
        {isAdmin && (
          <Panel className="xl:col-span-8" icon={<KeyRound className="size-4" />} title={t("recentEvents")}>
            {events.length === 0 ? (
              <div className="text-ink3 text-sm py-8 text-center">{t("noEvents")}</div>
            ) : (
              <ol className="flex flex-col">
                {events.map((e) => (
                  <li key={e.id} className="flex items-start gap-3 py-2.5 border-b border-white/[.05] last:border-0">
                    <EventIcon kind={e.kind} />
                    <span className="min-w-0 flex-1 text-[13px] text-ink break-words" dir="ltr" style={{ textAlign: lang === "fa" ? "right" : "left" }}>{e.message}</span>
                    <span className="text-[11.5px] text-ink3 whitespace-nowrap">{ago(e.at, lang)}</span>
                  </li>
                ))}
              </ol>
            )}
          </Panel>
        )}
      </div>
    </div>
  );
}

function EventIcon({ kind }: { kind: string }) {
  const m: Record<string, [typeof UserIcon, string]> = {
    user: [UserIcon, "#4c8dff"], limit: [Smartphone, "#9b7bff"], core: [Cpu, "#f2a93b"], auth: [KeyRound, "#3ddc97"], settings: [Settings2, "#a9b5cf"],
  };
  const [Icon, color] = m[kind] ?? [Activity, "#a9b5cf"];
  return (
    <span className="grid place-items-center size-7 rounded-lg shrink-0" style={{ color, background: `color-mix(in oklab, ${color} 14%, transparent)` }}>
      <Icon className="size-3.5" />
    </span>
  );
}

function TopUsers({ top, className }: { top: Overview["top"]; className?: string }) {
  const { t, lang, n } = useI18n();
  return (
    <Panel className={className} icon={<UsersIcon className="size-4" />} title={t("topUsers")} sub={lang === "fa" ? "۳۰ روز" : "30 days"}>
      {top.length === 0 ? (
        <div className="text-ink3 text-sm py-8 text-center">—</div>
      ) : (
        <ol className="flex flex-col gap-3.5">
          {top.map((u, i) => (
            <li key={u.id}>
              <a href={`#/users/${u.id}`} className="flex items-center gap-3 text-[13.5px] group">
                <span className="w-4 text-ink3 num text-[12px]">{n(i + 1)}</span>
                <Avatar name={u.name} size={30} />
                <span className="truncate font-medium group-hover:text-white" dir="ltr">{u.name}</span>
                <span className="ms-auto num text-ink2 text-[12.5px]">{bytes(u.bytes, lang)}</span>
              </a>
              <Meter value={u.bytes} max={top[0].bytes} className="ms-[54px] mt-1.5" plain height={4} />
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}

function Speed({ icon, color, label, value }: { icon: React.ReactNode; color: string; label: string; value: string }) {
  return (
    <div className="flex items-center gap-3">
      <span className="grid place-items-center size-10 rounded-xl" style={{ color, background: `color-mix(in oklab, ${color} 15%, transparent)`, boxShadow: `inset 0 0 0 1px color-mix(in oklab, ${color} 30%, transparent)` }}>{icon}</span>
      <div>
        <div className="text-[11.5px] text-ink3">{label}</div>
        <div className="text-[20px] font-bold num leading-tight">{value}</div>
      </div>
    </div>
  );
}

function Gauge({ label, value, text, sub }: { label: string; value: number; text: string; sub: string }) {
  return (
    <div className="flex flex-col items-center gap-1.5 rounded-2xl bg-white/[.025] border border-white/[.06] py-3">
      <Ring value={value} size={70} stroke={7} color={ringColor(value)}>
        <span className="text-[14px] font-bold num">{text}</span>
      </Ring>
      <div className="text-[12px] font-medium">{label}</div>
      <div className="text-[11px] text-ink3 num -mt-1">{sub}</div>
    </div>
  );
}

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] text-ink3">{k}</dt>
      <dd className="truncate">{v}</dd>
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="grid gap-4 xl:grid-cols-12">
      <Skeleton className="h-10 w-64 xl:col-span-12" />
      <Skeleton className="h-72 xl:col-span-8" />
      <Skeleton className="h-72 xl:col-span-4" />
      {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-36 xl:col-span-3" />)}
      <Skeleton className="h-80 xl:col-span-12" />
    </div>
  );
}

// ---------------------------------------------------------------- reseller

function ResellerOverview({ d }: { d: Overview }) {
  const { t, lang, n } = useI18n();
  const { me } = useSession();
  const q = d.quota!;
  const usersPct = q.maxUsers > 0 ? (q.users / q.maxUsers) * 100 : 0;
  const dataPct = q.maxData > 0 ? (q.allocated / q.maxData) * 100 : 0;
  const statusParts = (["active", "pending", "paused", "limited", "expired", "disabled"] as const).map((s) => ({ value: d.statuses[s] ?? 0, color: statusMeta[s].color, label: t(statusMeta[s].key) }));
  return (
    <div>
      <PageHead eyebrow={<span>{t("welcome")}{lang === "fa" ? "، " : ", "}<bdi dir="ltr">{me.admin.username}</bdi></span>} title={t("overview")} action={<NewUserButton />} />
      <div className="stagger grid gap-4 xl:grid-cols-12">
        <Panel className="xl:col-span-5" icon={<Sparkles className="size-4" />} title={t("quota")}>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col items-center gap-2 rounded-2xl bg-white/[.03] border border-white/[.06] py-4">
              <Ring value={usersPct} size={96} stroke={9} color={ringColor(usersPct)}>
                <div><div className="text-[18px] font-bold num">{n(q.users)}</div><div className="text-[11px] text-ink3 num">/ {q.maxUsers ? n(q.maxUsers) : "∞"}</div></div>
              </Ring>
              <div className="text-[12.5px] font-medium">{t("users")}</div>
            </div>
            <div className="flex flex-col items-center gap-2 rounded-2xl bg-white/[.03] border border-white/[.06] py-4">
              <Ring value={dataPct} size={96} stroke={9} color={ringColor(dataPct)}>
                <div><div className="text-[15px] font-bold num">{bytes(q.allocated, lang, 0)}</div><div className="text-[11px] text-ink3 num">/ {q.maxData ? bytes(q.maxData, lang, 0) : "∞"}</div></div>
              </Ring>
              <div className="text-[12.5px] font-medium">{t("allocated")}</div>
            </div>
          </div>
        </Panel>
        <div className="xl:col-span-7 grid gap-4 grid-cols-2">
          <StatCard icon={<Wifi className="size-[18px]" />} accent="#3ddc97" label={t("onlineNow")} value={<span className="text-online">{n(d.online)}</span>} sub={<span>{n(d.devices ?? 0)} {t("devicesNow")}</span>} />
          <StatCard icon={<UsersIcon className="size-[18px]" />} accent="#4c8dff" label={t("yourUsers")} value={n(d.users)}><StackBar parts={statusParts} /></StatCard>
          <StatCard icon={<Zap className="size-[18px]" />} accent="#f2a93b" label={t("today")} value={bytes(d.today.down + d.today.up, lang)} />
          <StatCard icon={<MemoryStick className="size-[18px]" />} accent="#9b7bff" label={t("thisMonth")} value={bytes(d.month.down + d.month.up, lang)} sub={<span>{t("lifetime")}: {bytes(d.lifetime, lang)}</span>} />
        </div>
        <Panel className="xl:col-span-8" icon={<Database className="size-4" />} title={t("usage30")}><UsageChart series={d.series} /></Panel>
        <TopUsers top={d.top} className="xl:col-span-4" />
      </div>
    </div>
  );
}
