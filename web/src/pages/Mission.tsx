import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp, Cpu, Users as UsersIcon, Wifi, X, Zap } from "lucide-react";
import { api, type Group, type Overview } from "../lib/api";
import { ago, bytes, duration } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Bytes, Dot, EventMsg, Num, Ring, ringColor } from "../components/ui";
import { LiveChart } from "../components/charts";
import { NetworkMap } from "../components/NetworkMap";
import { Logo } from "../components/Logo";
import { eventMeta, navigate, useSession } from "../App";

/** Full-screen monitoring wall for large displays. Esc returns to the panel. */
export function MissionControl() {
  const { t, lang } = useI18n();
  const { events } = useSession();
  const [d, setD] = useState<Overview | null>(null);
  const [now, setNow] = useState(new Date());
  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>("overview?days=1").then((r) => alive && setD(r)).catch(() => {});
    load();
    const id = setInterval(load, 3000);
    const c = setInterval(() => setNow(new Date()), 1000);
    const esc = (e: KeyboardEvent) => e.key === "Escape" && navigate("overview");
    window.addEventListener("keydown", esc);
    return () => { alive = false; clearInterval(id); clearInterval(c); window.removeEventListener("keydown", esc); };
  }, []);
  const live = d?.live[d.live.length - 1] ?? { up: 0, down: 0 };
  const s = d?.system;
  const pct = (a = 0, b = 0) => (b ? (a / b) * 100 : 0);
  return (
    <div className="relative min-h-dvh bg-ground overflow-hidden">
      <div className="aurora" aria-hidden />
      <div className="relative z-[1] p-6 lg:p-8 flex flex-col gap-6 min-h-dvh">
        <header className="flex items-center gap-4">
          <Logo part="emblem" height={40} />
          <div>
            <Logo part="wordmark" width={150} />
            <div className="text-[12px] text-ink3 mt-1">{t("missionControl")}</div>
          </div>
          <div className="ms-auto text-end">
            <div className="text-[30px] font-bold num leading-none">{new Intl.DateTimeFormat(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(now)}</div>
            <div className="text-[12px] text-ink3">{s ? `${t("uptime")} ${duration(s.uptime, lang)}` : "…"}</div>
          </div>
          <button onClick={() => navigate("overview")} className="grid place-items-center size-11 rounded-xl glass-strong" aria-label={t("close")}><X className="size-5" /></button>
        </header>
        {!d ? <div className="shimmer rounded-3xl flex-1" /> : (
          <>
            <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
              <Big icon={<Wifi />} color="var(--ok)" label={t("onlineNow")} value={<Num value={d.online} />} />
              <Big icon={<UsersIcon />} color="var(--primary2)" label={t("totalUsers")} value={<Num value={d.users} />} />
              <Big icon={<ArrowDown />} color="var(--primary2)" label={t("download")} value={<Bytes value={live.down} rate />} />
              <Big icon={<ArrowUp />} color="var(--cyan)" label={t("upload")} value={<Bytes value={live.up} rate />} />
              <Big icon={<Zap />} color="var(--warn)" label={t("today")} value={<Bytes value={d.today.down + d.today.up} />} />
            </div>
            <div className="grid gap-4 lg:grid-cols-12 flex-1">
              <div className="lg:col-span-8 glass rounded-3xl p-6 flex flex-col gap-4">
                <NetworkMap lines={d.lines} online={d.online} server={s?.hostname || "Dunvex"} onStation={(id) => navigate("protocols", String(id))} onAdd={(g: Group) => navigate("protocols", "add-" + g)} />
                <LiveChart live={d.live.slice(-60)} height={160} />
              </div>
              <div className="lg:col-span-4 flex flex-col gap-4">
                <div className="glass rounded-3xl p-6 grid grid-cols-3 gap-3">
                  {[{ k: t("cpu"), v: s?.cpu ?? 0 }, { k: t("ram"), v: pct(s?.memUsed, s?.memTotal) }, { k: t("disk"), v: pct(s?.diskUsed, s?.diskTotal) }].map((x) => (
                    <div key={x.k} className="flex flex-col items-center gap-2">
                      <Ring value={x.v} size={88} stroke={8} color={ringColor(x.v)}><span className="text-[16px] font-bold"><Num value={x.v} suffix="%" /></span></Ring>
                      <span className="text-[12.5px] text-ink2 inline-flex items-center gap-1"><Cpu className="size-3.5" />{x.k}</span>
                    </div>
                  ))}
                </div>
                <div className="glass rounded-3xl p-6 flex-1 min-h-0 overflow-hidden">
                  <div className="text-[13px] font-semibold mb-3 flex items-center gap-2"><Dot color="var(--ok)" pulse size={7} />{t("recentEvents")}</div>
                  <ul className="flex flex-col gap-2.5">
                    {events.slice(0, 9).map((e) => {
                      const m = eventMeta(e.kind);
                      return (
                        <li key={e.id} className="flex items-start gap-2.5 text-[13px]">
                          <m.icon className="size-4 shrink-0 mt-0.5" style={{ color: m.color }} />
                          <EventMsg message={e.message} truncate className="flex-1 min-w-0" />
                          <span className="text-ink3 text-[11.5px] whitespace-nowrap">{ago(e.at, lang)}</span>
                        </li>
                      );
                    })}
                  </ul>
                  <div className="mt-4 text-[12px] text-ink3">{t("thisMonth")}: {bytes(d.month.down + d.month.up, lang)}</div>
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function Big({ icon, color, label, value }: { icon: React.ReactNode; color: string; label: string; value: React.ReactNode }) {
  return (
    <div className="glass edge rounded-3xl p-5">
      <div className="flex items-center gap-2 text-[13px] text-ink2"><span className="[&>svg]:size-4" style={{ color }}>{icon}</span>{label}</div>
      <div className="text-[34px] font-bold mt-2 leading-none">{value}</div>
    </div>
  );
}
