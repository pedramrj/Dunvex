import { useState, type CSSProperties } from "react";
import { CircleCheck, CircleSlash, CircleX, Cloud, HeartPulse, RefreshCw, Repeat2, Server } from "lucide-react";
import type { HealthReport, HealthResult } from "../lib/api";
import { ago, cx, until } from "../lib/format";
import { msColor, sortResults, useHealth } from "../lib/health";
import { useI18n } from "../lib/i18n";
import { Badge, Button, Dot, Panel, Ring, Skeleton, useToast } from "./ui";
import { StackBar } from "./charts";
import { navigate } from "../App";
import { shortName } from "./NetworkMap";

const SHOWN = 9; // tiles before "Show all"

/** Overview widget: every config tried from the server, and what came back. */
export function ConfigHealthPanel({ className, style }: { className?: string; style?: CSSProperties }) {
  const { t } = useI18n();
  const toast = useToast();
  const { report, err, check } = useHealth();
  const [asked, setAsked] = useState(false);
  const running = !!report?.running || asked;
  const start = () => {
    setAsked(true);
    check().catch((e) => toast(e.message, "error")).finally(() => setAsked(false));
  };
  return (
    <div className={className} style={style}>
      <Panel icon={<HeartPulse className="size-4" />} title={t("configHealth")} sub={report && <HealthLine report={report} running={running} />}
        action={<Button size="sm" icon={<RefreshCw className={cx("size-3.5", running && "animate-spin")} />} disabled={running} onClick={start}>{running ? t("checking") : t("checkNow")}</Button>}>
        {!report ? (
          err ? <p className="text-[13px] text-bad py-4">{err}</p> : <Skeleton className="h-36" />
        ) : report.at === 0 && !running ? (
          <div className="flex flex-col sm:flex-row sm:items-center gap-4 py-3">
            <span className="grid place-items-center size-12 rounded-2xl shrink-0 text-primary2 bg-primary/10 border border-primary/25"><HeartPulse className="size-6" /></span>
            <p className="text-[13.5px] text-ink2 max-w-[62ch] leading-relaxed">{t("healthIntro")}</p>
          </div>
        ) : (
          <HealthBody report={report} running={running} />
        )}
      </Panel>
    </div>
  );
}

function HealthLine({ report, running }: { report: HealthReport; running: boolean }) {
  const { t, lang } = useI18n();
  if (running) return <span className="inline-flex items-center gap-1.5 text-primary2"><Dot color="var(--primary2)" pulse size={6} />{t("checkingConfigs")}</span>;
  if (!report.at) return <span>{t("healthNever")}</span>;
  const next = report.hours > 0 && report.next ? t("healthNext").replace("{in}", until(report.next, lang)) : t("healthManual");
  return <span>{t("healthChecked").replace("{ago}", ago(report.at, lang))} · {next}</span>;
}

function HealthBody({ report, running }: { report: HealthReport; running: boolean }) {
  const { t, n } = useI18n();
  const [all, setAll] = useState(false);
  const { ok, fail, skip, total } = report.summary;
  const pct = total > 0 ? (ok / total) * 100 : 0;
  const ring = fail === 0 ? "url(#ring-brand)" : fail * 2 > total ? "url(#ring-bad)" : "url(#ring-warn)";
  const tested = sortResults(report.results.filter((r) => r.status !== "skip"));
  const skipped = report.results.filter((r) => r.status === "skip");
  const prefix = titlePrefix(report.results);
  const shown = all ? tested : tested.slice(0, Math.max(SHOWN, tested.filter((r) => r.status === "fail").length));
  return (
    <div className={cx("transition-opacity", running && "opacity-60")}>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-4 mb-5">
        <Ring value={pct} size={84} stroke={9} color={ring}>
          <span className="text-[17px] font-bold num leading-none">{n(ok)}<span className="text-ink3 text-[12px] font-normal">/{n(total)}</span></span>
        </Ring>
        <div className="min-w-[150px] flex-1">
          <div className="text-[17px] font-semibold">{total > 0 && fail === 0 ? t("configsAllWork") : t("configsWork").replace("{ok}", n(ok)).replace("{total}", n(total))}</div>
          <div className="mt-2.5 max-w-md">
            <StackBar height={7} parts={[
              { value: ok, color: "var(--ok)", label: t("healthOk") },
              { value: fail, color: "var(--bad)", label: t("healthFail") },
              { value: skip, color: "color-mix(in oklab, var(--fg) 18%, transparent)", label: t("healthSkip") },
            ]} />
          </div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-ink2">
            <Legend color="var(--ok)" label={t("healthOk")} value={ok} />
            <Legend color="var(--bad)" label={t("healthFail")} value={fail} />
            {skip > 0 && <Legend color="var(--text3)" label={t("healthSkip")} value={skip} />}
          </div>
        </div>
      </div>
      {tested.length > 0 && (
        <ul className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {shown.map((r, i) => <Tile key={`${r.inboundId}-${r.name}-${i}`} r={r} name={r.name.slice(prefix.length) || r.name} />)}
        </ul>
      )}
      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        {skipped.length > 0 ? (
          <p className="text-[12px] text-ink3 min-w-0" title={skipped.map((s) => `${s.name}: ${s.error ?? ""}`).join("\n")}>
            <CircleSlash className="inline size-3.5 me-1 -mt-0.5" />
            {t("healthSkipped").replace("{n}", n(skipped.length))}: <bdi dir="ltr">{[...new Set(skipped.map((s) => shortName[s.preset] ?? s.preset))].join(", ")}</bdi>
          </p>
        ) : <span />}
        {tested.length > shown.length || all ? (
          <button className="text-[12.5px] text-primary2 hover:underline" onClick={() => setAll(!all)}>
            {all ? t("healthShowLess") : t("healthShowAll").replace("{n}", n(tested.length))}
          </button>
        ) : null}
      </div>
    </div>
  );
}

function Legend({ color, label, value }: { color: string; label: string; value: number }) {
  const { n } = useI18n();
  return <span className="inline-flex items-center gap-1.5"><Dot color={color} size={7} />{label} <span className="num font-semibold text-ink">{n(value)}</span></span>;
}

/** The panel's name ("Dunvex · ") that starts every config name, left off the tiles. */
function titlePrefix(results: HealthResult[]): string {
  const first = results[0]?.name ?? "";
  const i = first.indexOf(" · ");
  if (i < 0) return "";
  const p = first.slice(0, i + 3);
  return results.every((r) => r.name.startsWith(p) && r.name.length > p.length) ? p : "";
}

function Tile({ r, name }: { r: HealthResult; name: string }) {
  const { t, n } = useI18n();
  const failed = r.status === "fail";
  return (
    <li>
      <button onClick={() => navigate("protocols", String(r.inboundId))}
        className={cx("w-full text-start rounded-2xl border px-3 py-2.5 transition-colors hover:bg-fg/[.04]",
          failed ? "border-bad/35 bg-bad/[.06]" : "border-hair bg-fg/[.02]")}>
        <div className="flex items-center gap-2 min-w-0">
          {failed ? <CircleX className="size-4 shrink-0 text-bad" /> : <CircleCheck className="size-4 shrink-0 text-online" />}
          <bdi dir="ltr" className="min-w-0 flex-1 truncate text-[13px] font-medium" title={r.name}>{name}</bdi>
          {!failed && <span className="num text-[12px] font-semibold shrink-0" style={{ color: msColor(r.ms) }}>{n(r.ms ?? 0)} <span className="font-normal text-ink3">ms</span></span>}
        </div>
        {(r.server || r.via) && (
          <div className="mt-1.5 flex flex-wrap gap-1">
            {r.server && <Badge className="h-5 text-[10.5px] px-2" color="var(--color-cyan)" icon={<Server className="size-2.5" />}><bdi dir="ltr">{r.server}</bdi></Badge>}
            {r.via === "cdn" && <Badge className="h-5 text-[10.5px] px-2" color="var(--color-cdn)" icon={<Cloud className="size-2.5" />}>{t("viaCdn")}</Badge>}
            {r.via === "relay" && <Badge className="h-5 text-[10.5px] px-2" color="var(--color-violet)" icon={<Repeat2 className="size-2.5" />}>{t("viaRelay")}</Badge>}
          </div>
        )}
        {failed && r.error && <p className="mt-1.5 text-[11.5px] text-bad/90 line-clamp-2 [overflow-wrap:anywhere]" dir="ltr" title={r.error}>{r.error}</p>}
      </button>
    </li>
  );
}
