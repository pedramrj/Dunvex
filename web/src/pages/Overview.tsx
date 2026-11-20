import { useEffect, useState } from "react";
import { AlertTriangle, Cpu, Server } from "lucide-react";
import { api, type Group, type Overview } from "../lib/api";
import { bytes, duration, speed, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Dot, Meter, Panel, Spinner } from "../components/ui";
import { NetworkMap } from "../components/NetworkMap";
import { Trace, UsageBars } from "../components/charts";
import { NewUserButton, PageHead, navigate } from "../App";

export function OverviewPage() {
  const { t, lang, n } = useI18n();
  const [d, setD] = useState<Overview | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () => api<Overview>("overview").then((r) => alive && setD(r)).catch(() => {});
    load();
    const id = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);
  if (!d) return <Spinner />;

  const sys = d.system;
  const live = d.live.slice(-60);
  const last = live[live.length - 1] ?? { up: 0, down: 0 };
  const hist = d.history.slice(-90);
  const server = sys.hostname || sys.publicIp || "Dunvex";
  const coreErr = Object.entries(d.coreErrors ?? {});

  return (
    <div className="rise">
      <PageHead
        title={t("overview")}
        sub={
          <span className="inline-flex flex-wrap gap-x-3 gap-y-1">
            <bdi className="num" dir="ltr">{sys.publicIp}</bdi>
            <span>{sys.os}</span>
            <span>{t("uptime")}: {duration(sys.uptime, lang)}</span>
          </span>
        }
        action={<NewUserButton />}
      />

      {coreErr.length > 0 && (
        <div className="mb-4 flex gap-3 rounded-2xl border border-[rgba(255,93,115,.4)] bg-[rgba(255,93,115,.07)] px-4 py-3 text-[13.5px]">
          <AlertTriangle className="size-5 text-bad shrink-0 mt-0.5" />
          <div className="min-w-0">
            {coreErr.map(([k, v]) => (
              <div key={k} className="break-words"><b dir="ltr">{k}</b>: <bdi dir="ltr" className="text-ink2">{v}</bdi></div>
            ))}
          </div>
        </div>
      )}

      <Panel className="mb-4" title={t("network")} action={<span className="text-[13px] text-ink2 num">{n(d.inboundsOn)} / {n(d.inbounds)} {t("stations")}</span>}>
        <NetworkMap
          lines={d.lines}
          online={d.online}
          server={server}
          onStation={(id) => navigate("protocols", String(id))}
          onAdd={(g: Group) => navigate("protocols", "add-" + g)}
        />
      </Panel>

      {/* stats strip */}
      <section className="mb-4 rounded-2xl bg-panel border border-hair grid grid-cols-2 lg:grid-cols-4 2xl:grid-cols-7 overflow-hidden [&>*]:border-hair [&>*]:border-b [&>*]:border-e">
        <Cell label={t("onlineNow")}>
          <div className="flex items-baseline gap-2">
            <span className="text-[26px] font-bold num text-online">{n(d.online)}</span>
            <span className="text-ink3 num">/ {n(d.users)}</span>
          </div>
          <div className="flex flex-wrap gap-x-3 gap-y-0.5 mt-1 text-[12px] text-ink2">
            {(["active", "pending", "limited", "expired"] as const).map((s) =>
              d.statuses[s] ? (
                <span key={s} className="inline-flex items-center gap-1"><Dot color={statusMeta[s].color} />{n(d.statuses[s]!)} {t(statusMeta[s].key)}</span>
              ) : null,
            )}
          </div>
        </Cell>
        <Cell label={t("liveSpeed")}>
          <div className="text-[20px] font-bold num leading-tight">{speed(last.down + last.up, lang)}</div>
          <Trace values={live.map((s) => s.down + s.up)} />
        </Cell>
        <Cell label={t("today")}>
          <div className="text-[20px] font-bold num leading-tight">{bytes(d.today.down + d.today.up, lang)}</div>
          <div className="text-[12px] text-ink2 num mt-1">↓ {bytes(d.today.down, lang)} · ↑ {bytes(d.today.up, lang)}</div>
        </Cell>
        <Cell label={t("thisMonth")}>
          <div className="text-[20px] font-bold num leading-tight">{bytes(d.month.down + d.month.up, lang)}</div>
          <div className="text-[12px] text-ink2 num mt-1">{t("total")}: {bytes(d.lifetime, lang)}</div>
        </Cell>
        <Cell label={t("cpu")}>
          <div className="flex items-baseline justify-between"><span className="text-[20px] font-bold num">{n(sys.cpu, 0)}%</span><span className="text-[12px] text-ink3 num">{n(sys.cores)} {lang === "fa" ? "هسته" : "cores"}</span></div>
          <Trace values={hist.map((h) => h.cpu)} color="var(--color-udp)" />
        </Cell>
        <Cell label={t("ram")}>
          <div className="text-[20px] font-bold num">{bytes(sys.memUsed, lang)}</div>
          <Meter value={sys.memUsed} max={sys.memTotal} className="mt-2" />
          <div className="text-[12px] text-ink3 num mt-1">{t("total")} {bytes(sys.memTotal, lang)}</div>
        </Cell>
        <Cell label={t("disk")}>
          <div className="text-[20px] font-bold num">{bytes(sys.diskUsed, lang)}</div>
          <Meter value={sys.diskUsed} max={sys.diskTotal} className="mt-2" />
          <div className="text-[12px] text-ink3 num mt-1">{t("total")} {bytes(sys.diskTotal, lang)}</div>
        </Cell>
      </section>

      <div className="grid gap-4 xl:grid-cols-[1.6fr_1fr]">
        <Panel title={t("usage30")}>
          <UsageBars series={d.series} />
        </Panel>
        <div className="grid gap-4 content-start">
          <Panel title={t("topUsers")}>
            {d.top.length === 0 ? (
              <div className="text-ink3 text-sm py-6 text-center">—</div>
            ) : (
              <ol className="flex flex-col gap-3">
                {d.top.map((u, i) => (
                  <li key={u.id}>
                    <a href={`#/users/${u.id}`} className="flex items-center gap-3 text-[13.5px] hover:text-white">
                      <span className="w-5 text-ink3 num">{n(i + 1)}</span>
                      <span className="truncate font-medium" dir="ltr">{u.name}</span>
                      <span className="ms-auto num text-ink2">{bytes(u.bytes, lang)}</span>
                    </a>
                    <Meter value={u.bytes} max={d.top[0].bytes} className="ms-8 mt-1.5" color="var(--color-cdn)" plain />
                  </li>
                ))}
              </ol>
            )}
          </Panel>
          <Panel title={t("cores")}>
            <ul className="flex flex-col gap-2.5">
              {d.cores.map((c) => (
                <li key={c.name} className="flex items-center gap-3 text-[13.5px]">
                  {c.name === "xray" ? <Cpu className="size-4 text-ink3" /> : <Server className="size-4 text-ink3" />}
                  <span className="font-medium" dir="ltr">{c.name}</span>
                  <span className="text-ink3 num" dir="ltr">{c.version || "—"}</span>
                  <span className="ms-auto inline-flex items-center gap-1.5 text-[12.5px]">
                    <Dot color={c.running ? "var(--color-online)" : c.installed ? "var(--color-ink3)" : "var(--color-bad)"} pulse={c.running} />
                    {c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.installed ? t("stopped") : t("notInstalled")}
                  </span>
                </li>
              ))}
            </ul>
          </Panel>
        </div>
      </div>
    </div>
  );
}

function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="px-4 py-3.5 min-w-0">
      <div className="text-[12.5px] text-ink2 mb-1">{label}</div>
      {children}
    </div>
  );
}
