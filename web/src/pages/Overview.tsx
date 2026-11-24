import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import {
  Activity, AlertTriangle, ArrowDown, ArrowUp, Bell, CalendarDays, CheckCircle2, ChevronDown, ChevronUp, Cpu, Database, HardDrive, LayoutGrid,
  Network, Server, Sparkles, Users as UsersIcon, Wifi, Zap,
} from "lucide-react";
import { BorderBeam } from "@/components/ui/border-beam";
import { api, type Group, type Overview } from "../lib/api";
import { ago, bytes, cx, duration, speed, statusMeta } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { allWidgets, usePrefs, type Widget } from "../lib/prefs";
import { ArcGauge, Avatar, Badge, Button, Bytes, Dot, ErrorState, EventMsg, Meter, Num, Panel, Ring, ringColor, Segmented, Sheet, Skeleton, StatCard, Timeline, Toggle } from "../components/ui";
import { NetworkMap } from "../components/NetworkMap";
import { ConfigHealthPanel } from "../components/ConfigHealth";
import { LiveChart, Spark, StackBar, UsageChart } from "../components/charts";
import { NewUserButton, PageHead, can, eventMeta, navigate, useSession } from "../App";
import { CoreErrors } from "../components/ErrText";
import { Rack } from "../components/Rack";

// the live map and its world data load only when the widget is on screen
const EarthCard = lazy(() => import("../components/Earth").then((m) => ({ default: m.EarthCard })));

const widgetKey: Record<Widget, Key> = { live: "liveTraffic", health: "serverHealth", kpis: "kpis", map: "liveMap", network: "network", configs: "configHealth", usage: "usageHistory", top: "topUsers", cores: "cores", events: "recentEvents" };

export function OverviewPage() {
  const { t, lang, n, pc } = useI18n();
  const { me, events } = useSession();
  const { prefs } = usePrefs();
  const [d, setD] = useState<Overview | null>(null);
  const [err, setErr] = useState("");
  const [days, setDays] = useState(30);
  const [custom, setCustom] = useState(false);
  const isAdmin = can(me.admin.role, "admin");

  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>(`overview?days=${days}`).then((r) => { if (alive) { setD(r); setErr(""); } }).catch((e) => alive && setErr(e.message));
    load();
    const id = setInterval(load, 5000);
    return () => { alive = false; clearInterval(id); };
  }, [days]);

  if (!d) return err ? <ErrorState message={err} onRetry={() => location.reload()} /> : <OverviewSkeleton />;
  if (d.role === "reseller") return <ResellerOverview d={d} days={days} setDays={setDays} />;

  const sys = d.system;
  const live = d.live.slice(-60);
  const last = live[live.length - 1] ?? { up: 0, down: 0 };
  const hist = d.history.slice(-90);
  const server = sys.hostname || sys.publicIp || "Dunvex";
  const coreErr = Object.entries(d.coreErrors ?? {});
  const problems = coreErr.length + (d.inboundErrs ?? 0);
  const pct = (a: number, b: number) => (b > 0 ? (a / b) * 100 : 0);
  const show = (w: Widget) => prefs.widgets.includes(w);
  const order = (w: Widget) => ({ order: prefs.widgets.indexOf(w) });
  // a row of widgets sits where its first visible widget is in the chosen order
  const rowOrder = (ws: Widget[]) => ({ order: Math.min(...ws.map((w) => { const i = prefs.widgets.indexOf(w); return i < 0 ? 99 : i; })) });

  return (
    <div>
      <PageHead
        eyebrow={<span>{t("welcome")}{lang === "fa" ? "، " : ", "}<bdi dir="ltr" className="text-ink2">{me.admin.username}</bdi></span>}
        title={t("overview")}
        sub={
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="inline-flex items-center gap-1.5"><Server className="size-3.5 text-ink3" /><bdi className="mono text-[12.5px] sensitive" dir="ltr">{sys.publicIp || "—"}</bdi></span>
            <span className="text-ink3">·</span><span>{sys.os}</span><span className="text-ink3">·</span><span>{t("uptime")}: {duration(sys.uptime, lang)}</span>
          </span>
        }
        action={
          <>
            {problems === 0 ? (
              <Badge color="var(--ok)" icon={<CheckCircle2 className="size-3.5" />} className="h-9 px-3">{t("allGood")}</Badge>
            ) : (
              <Badge color="var(--bad)" icon={<AlertTriangle className="size-3.5" />} className="h-9 px-3">{t("issues")} · {n(problems)}</Badge>
            )}
            <Button icon={<LayoutGrid className="size-4" />} onClick={() => setCustom(true)}>{t("customize")}</Button>
            <NewUserButton />
          </>
        }
      />

      {coreErr.length > 0 && (
        <div className="rise mb-5"><CoreErrors errors={coreErr} /></div>
      )}

      <div className="flex flex-col gap-[var(--gap)]">
        <div className="grid gap-[var(--gap)] xl:grid-cols-12" style={rowOrder(["live", "health"])}>
          {show("live") && (
            <Panel className="xl:col-span-8 overflow-hidden" icon={<Activity className="size-4" />} title={t("liveTraffic")} sub={t("liveSub")}
              action={<span className="inline-flex items-center gap-2 text-[11.5px] font-semibold text-online tracking-wider"><Dot color="var(--ok)" pulse size={7} />LIVE</span>}>
              <div className="flex flex-wrap gap-x-8 gap-y-3 mb-3">
                <Speed icon={<ArrowDown className="size-4" />} color="var(--primary2)" label={t("download")} value={last.down} />
                <Speed icon={<ArrowUp className="size-4" />} color="var(--cyan)" label={t("upload")} value={last.up} />
                <div className="ms-auto text-end">
                  <div className="text-[11.5px] text-ink3">{t("onlineNow")}</div>
                  <div className="text-[24px] font-bold text-online leading-tight"><Num value={d.online} /> <span className="text-[13px] text-ink3 font-normal">/ <Num value={d.users} /></span></div>
                </div>
              </div>
              <LiveChart live={live} height={170} />
              <BorderBeam size={90} duration={9} colorFrom="var(--primary2)" colorTo="var(--cyan)" />
            </Panel>
          )}
          {show("health") && (
            <Panel className="xl:col-span-4" icon={<Cpu className="size-4" />} title={t("serverHealth")} sub={<span dir="ltr" className="mono">{server}</span>}>
              <ArcGauge value={sys.cpu} label={t("cpu")} sub={`${n(sys.cores)} ${t("coresUnit")}`} />
              <div className="mt-4 grid grid-cols-2 gap-3">
                <Res label={t("ram")} value={pct(sys.memUsed, sys.memTotal)} sub={`${bytes(sys.memUsed, lang, 1)} / ${bytes(sys.memTotal, lang, 0)}`} />
                <Res label={t("disk")} value={pct(sys.diskUsed, sys.diskTotal)} sub={`${bytes(sys.diskUsed, lang, 0)} / ${bytes(sys.diskTotal, lang, 0)}`} />
              </div>
              <div className="mt-4">
                <div className="flex items-center justify-between text-[11.5px] text-ink3 mb-1"><span>{t("cpu")} · {t("last6min")}</span><span className="num">{t("peak")} {pc(Math.max(0, ...hist.map((h) => h.cpu)), 0)}</span></div>
                <Spark values={hist.map((h) => h.cpu)} color="var(--warn)" height={36} />
              </div>
              <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5 text-[12.5px]">
                <Row k={t("netNow")} v={<span className="num">↓{speed(sys.netDown, lang)} ↑{speed(sys.netUp, lang)}</span>} />
                <Row k={t("connections")} v={<span className="num">TCP {n(sys.tcp)} · UDP {n(sys.udp)}</span>} />
                <Row k={t("load")} v={<span className="mono text-[12px]" dir="ltr">{(sys.load ?? []).map((x) => x.toFixed(2)).join("  ") || "—"}</span>} />
                <Row k={t("swap")} v={<span className="num">{sys.swapTotal ? `${bytes(sys.swapUsed, lang)} / ${bytes(sys.swapTotal, lang)}` : "—"}</span>} />
              </dl>
            </Panel>
          )}
        </div>

        {show("kpis") && (
          <div style={order("kpis")}><Rack d={d} server={server} /></div>
        )}

        {show("map") && (
          <div style={order("map")}>
            <Suspense fallback={<Skeleton className="h-[340px]" />}><EarthCard /></Suspense>
          </div>
        )}

        {show("network") && (
          <div style={order("network")}>
          <Panel icon={<Network className="size-4" />} title={t("network")} sub={<span className="num">{n(d.inboundsOn)} / {n(d.inbounds)} {t("stations")}</span>}
            action={isAdmin && <a href="#/network" className="text-[13px] text-primary2 hover:underline">{t("networkCenter")}</a>}>
            <NetworkMap lines={d.lines} online={d.online} server={server} onStation={(id) => navigate("protocols", String(id))} onAdd={(g: Group) => navigate("protocols", "add-" + g)} />
          </Panel>
          </div>
        )}

        {show("configs") && isAdmin && <ConfigHealthPanel style={order("configs")} />}

        <div className="grid gap-[var(--gap)] xl:grid-cols-12" style={rowOrder(["usage", "top", "cores", "events"])}>
          {show("usage") && (
            <Panel className="xl:col-span-8" icon={<Database className="size-4" />} title={t("usageHistory")}
              action={<Segmented value={days} onChange={setDays} options={[{ value: 1, label: t("r24h") }, { value: 7, label: t("r7d") }, { value: 30, label: t("r30d") }, { value: 90, label: t("r90d") }]} />}>
              <UsageChart series={d.series} days={days} exportName={`dunvex-usage-${days}d`} />
            </Panel>
          )}
          {show("top") && <TopUsers top={d.top} className="xl:col-span-4" />}
          {show("cores") && (
            <Panel className="xl:col-span-4" icon={<Server className="size-4" />} title={t("cores")}>
              <ul className="flex flex-col gap-3">
                {d.cores.map((c) => (
                  <li key={c.name} className="flex items-center gap-3 rounded-2xl bg-fg/[.025] border border-hair p-3">
                    <span className={cx("grid place-items-center size-10 rounded-xl", c.running ? "bg-online/12 text-online" : "bg-fg/[.05] text-ink3")}>
                      {c.name === "xray" ? <Sparkles className="size-5" /> : <HardDrive className="size-5" />}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="font-semibold text-[13.5px]" dir="ltr">{c.name} <span className="mono text-[11.5px] text-ink3 font-normal">{c.version || "—"}</span></div>
                      <div className="text-[12px] text-ink3">{c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.idle ? t("notInUse") : c.installed ? t("stopped") : t("notInstalled")}</div>
                    </div>
                    <Dot color={c.running ? "var(--ok)" : c.idle || c.installed ? "var(--text3)" : "var(--bad)"} pulse={c.running} />
                  </li>
                ))}
              </ul>
            </Panel>
          )}
          {show("events") && isAdmin && (
            <Panel className="xl:col-span-8" icon={<Bell className="size-4" />} title={t("recentEvents")} action={<a href="#/alerts" className="text-[13px] text-primary2 hover:underline">{t("viewAll")}</a>}>
              {events.length === 0 ? (
                <div className="text-ink3 text-sm py-8 text-center">{t("noEvents")}</div>
              ) : (
                <Timeline items={events.slice(0, 7).map((e) => {
                  const m = eventMeta(e.kind);
                  return { id: e.id, icon: m.icon, color: m.color, title: <EventMsg message={e.message} className="block" />, meta: ago(e.at, lang), tag: <Badge color={m.color}>{t(m.key)}</Badge> };
                })} />
              )}
            </Panel>
          )}
        </div>
      </div>

      <CustomizeSheet open={custom} onClose={() => setCustom(false)} />
    </div>
  );
}


function CustomizeSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  const { prefs, set } = usePrefs();
  const hidden = allWidgets.filter((w) => !prefs.widgets.includes(w));
  const move = (w: Widget, dir: -1 | 1) => {
    const list = [...prefs.widgets];
    const i = list.indexOf(w);
    const j = i + dir;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    set("widgets", list);
  };
  return (
    <Sheet open={open} onClose={onClose} title={t("customize")} sub={t("customizeSub")}>
      <ul className="flex flex-col gap-2">
        {[...prefs.widgets, ...hidden].map((w) => {
          const on = prefs.widgets.includes(w);
          return (
            <li key={w} className="flex items-center gap-3 rounded-2xl border border-hair bg-fg/[.02] px-3 h-14">
              <Toggle checked={on} onChange={(v) => set("widgets", v ? [...prefs.widgets, w] : prefs.widgets.filter((x) => x !== w))} />
              <span className={cx("flex-1 text-[14px]", !on && "text-ink3")}>{t(widgetKey[w])}</span>
              {on && <>
                <button onClick={() => move(w, -1)} className="grid place-items-center size-8 rounded-lg hover:bg-fg/[.06]" aria-label="up"><ChevronUp className="size-4" /></button>
                <button onClick={() => move(w, 1)} className="grid place-items-center size-8 rounded-lg hover:bg-fg/[.06]" aria-label="down"><ChevronDown className="size-4" /></button>
              </>}
            </li>
          );
        })}
      </ul>
      <Button className="mt-4" variant="quiet" onClick={() => set("widgets", allWidgets)}>{t("resetLayout")}</Button>
    </Sheet>
  );
}

function TopUsers({ top, className }: { top: Overview["top"]; className?: string }) {
  const { t, lang, n } = useI18n();
  return (
    <Panel className={className} icon={<UsersIcon className="size-4" />} title={t("topUsers")} sub={t("r30d")}>
      {top.length === 0 ? (
        <div className="text-ink3 text-sm py-8 text-center">—</div>
      ) : (
        <ol className="flex flex-col gap-3.5">
          {top.map((u, i) => (
            <li key={u.id}>
              <a href={`#/users/${u.id}`} className="flex items-center gap-3 text-[13.5px] group">
                <span className="w-4 text-ink3 num text-[12px]">{n(i + 1)}</span>
                <Avatar name={u.name} size={30} />
                <span className="truncate font-medium group-hover:text-primary2 transition-colors" dir="ltr">{u.name}</span>
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

function Speed({ icon, color, label, value }: { icon: React.ReactNode; color: string; label: string; value: number }) {
  return (
    <div className="flex items-center gap-3">
      <span className="grid place-items-center size-10 rounded-xl" style={{ color, background: `color-mix(in oklab, ${color} 15%, transparent)`, boxShadow: `inset 0 0 0 1px color-mix(in oklab, ${color} 30%, transparent)` }}>{icon}</span>
      <div>
        <div className="text-[11.5px] text-ink3">{label}</div>
        <div className="text-[21px] font-bold leading-tight"><Bytes value={value} rate /></div>
      </div>
    </div>
  );
}

function Res({ label, value, sub }: { label: string; value: number; sub: string }) {
  const { pc } = useI18n();
  return (
    <div className="rounded-2xl bg-fg/[.03] border border-hair px-3.5 py-3">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[12px] text-ink2 font-medium">{label}</span>
        <span className="text-[15px] font-bold num" style={{ color: ringColor(value) }}>{pc(value, 0)}</span>
      </div>
      <Meter value={value} max={100} className="mt-2" />
      <div className="mt-1.5 text-[11px] text-ink3 num truncate">{sub}</div>
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
      {[0, 1, 2, 3, 4, 5].map((i) => <Skeleton key={i} className="h-32 xl:col-span-2" />)}
      <Skeleton className="h-80 xl:col-span-12" />
    </div>
  );
}

// ================================================================= reseller

function ResellerOverview({ d, days, setDays }: { d: Overview; days: number; setDays: (v: number) => void }) {
  const { t, lang } = useI18n();
  const { me } = useSession();
  const q = d.quota!;
  const usersPct = q.maxUsers > 0 ? (q.users / q.maxUsers) * 100 : 0;
  const dataPct = q.maxData > 0 ? (q.allocated / q.maxData) * 100 : 0;
  const stats = useMemo(() => (["active", "pending", "paused", "limited", "expired", "disabled"] as const).map((s) => ({ value: d.statuses[s] ?? 0, color: statusMeta[s].color, label: t(statusMeta[s].key) })), [d.statuses, t]);
  return (
    <div>
      <PageHead eyebrow={<span>{t("welcome")}{lang === "fa" ? "، " : ", "}<bdi dir="ltr">{me.admin.username}</bdi></span>} title={t("resellerDash")} action={<NewUserButton />} />
      <div className="stagger grid gap-[var(--gap)] xl:grid-cols-12">
        <Panel className="xl:col-span-5" icon={<Sparkles className="size-4" />} title={t("quota")}>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col items-center gap-2 rounded-2xl bg-fg/[.02] border border-hair py-4">
              <Ring value={usersPct} size={100} stroke={9} color={ringColor(usersPct)}>
                <div><div className="text-[18px] font-bold"><Num value={q.users} /></div><div className="text-[11px] text-ink3">/ {q.maxUsers ? <Num value={q.maxUsers} /> : "∞"}</div></div>
              </Ring>
              <div className="text-[12.5px] font-medium">{t("users")}</div>
            </div>
            <div className="flex flex-col items-center gap-2 rounded-2xl bg-fg/[.02] border border-hair py-4">
              <Ring value={dataPct} size={100} stroke={9} color={ringColor(dataPct)}>
                <div><div className="text-[15px] font-bold num">{bytes(q.allocated, lang, 0)}</div><div className="text-[11px] text-ink3 num">/ {q.maxData ? bytes(q.maxData, lang, 0) : "∞"}</div></div>
              </Ring>
              <div className="text-[12.5px] font-medium">{t("allocated")}</div>
            </div>
          </div>
          <p className="text-[12px] text-ink3 mt-3">{t("quotaLeft")}: <span className="num">{q.maxData ? bytes(Math.max(0, q.maxData - q.allocated), lang) : "∞"}</span></p>
        </Panel>
        <div className="xl:col-span-7 grid gap-[var(--gap)] grid-cols-2">
          <StatCard icon={<Wifi className="size-[18px]" />} accent="var(--ok)" label={t("onlineNow")} value={<span className="text-online"><Num value={d.online} /></span>} sub={<span><Num value={d.devices ?? 0} /> {t("devicesNow")}</span>} />
          <StatCard icon={<UsersIcon className="size-[18px]" />} label={t("yourUsers")} value={<Num value={d.users} />}><StackBar parts={stats} /></StatCard>
          <StatCard icon={<Zap className="size-[18px]" />} accent="var(--warn)" label={t("today")} value={<Bytes value={d.today.down + d.today.up} />} />
          <StatCard icon={<CalendarDays className="size-[18px]" />} accent="var(--color-violet)" label={t("thisMonth")} value={<Bytes value={d.month.down + d.month.up} />} sub={<span>{t("lifetime")}: {bytes(d.lifetime, lang)}</span>} />
        </div>
        <Panel className="xl:col-span-8" icon={<Database className="size-4" />} title={t("usageHistory")}
          action={<Segmented value={days} onChange={setDays} options={[{ value: 1, label: t("r24h") }, { value: 7, label: t("r7d") }, { value: 30, label: t("r30d") }, { value: 90, label: t("r90d") }]} />}>
          <UsageChart series={d.series} days={days} exportName={`dunvex-usage-${days}d`} />
        </Panel>
        <TopUsers top={d.top} className="xl:col-span-4" />
      </div>
    </div>
  );
}
