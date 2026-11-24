import { useEffect, useMemo, useState } from "react";
import { BellOff, CheckCheck } from "lucide-react";
import { api, type Event } from "../lib/api";
import { cx } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { usePrefs } from "../lib/prefs";
import { Badge, Button, Empty, EventMsg, Panel, Segmented, Toggle } from "../components/ui";
import { PageHead, eventMeta } from "../App";

const kinds: { id: string; key: Key }[] = [
  { id: "limit", key: "catLimit" }, { id: "core", key: "catCore" }, { id: "auth", key: "catAuth" }, { id: "user", key: "catUser" }, { id: "health", key: "catHealth" }, { id: "backup", key: "catBackup" }, { id: "settings", key: "catSettings" },
];
const severity = (e: Event): "high" | "medium" | "low" =>
  e.kind === "core" || /fail|error|busy/i.test(e.message) ? "high" : e.kind === "limit" || e.kind === "auth" ? "medium" : "low";
const sevColor = { high: "var(--bad)", medium: "var(--warn)", low: "var(--text3)" };

/** Notification centre: every panel event, grouped by day, with read state. */
export function AlertsPage() {
  const { t, lang, n } = useI18n();
  const { prefs, set } = usePrefs();
  const [events, setEvents] = useState<Event[]>([]);
  const [kind, setKind] = useState("all");
  const [unreadOnly, setUnreadOnly] = useState(false);
  const lastRead = prefs.lastRead;
  useEffect(() => {
    const load = () => api<Event[]>("events?limit=500").then(setEvents).catch(() => {});
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, []);
  const list = events.filter((e) => (kind === "all" || e.kind === kind) && (!unreadOnly || e.id > lastRead));
  const days = useMemo(() => {
    const m = new Map<string, Event[]>();
    const fmt = new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", { weekday: "long", day: "numeric", month: "long" });
    for (const e of list) {
      const k = fmt.format(new Date(e.at * 1000));
      m.set(k, [...(m.get(k) ?? []), e]);
    }
    return [...m.entries()];
  }, [list, lang]);
  const time = new Intl.DateTimeFormat(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit" });
  const unread = events.filter((e) => e.id > lastRead).length;

  return (
    <div>
      <PageHead title={t("alerts")} sub={<span className="num">{n(unread)} {t("unread")}</span>}
        action={<>
          <Toggle checked={prefs.quiet} onChange={(v) => set("quiet", v)} label={<span className="inline-flex items-center gap-1.5"><BellOff className="size-4" />{t("quietMode")}</span>} />
          <Button icon={<CheckCheck className="size-4" />} onClick={() => set("lastRead", events[0]?.id ?? lastRead)}>{t("markAllRead")}</Button>
        </>} />
      <div className="flex flex-wrap gap-3 mb-4">
        <Segmented value={kind} onChange={setKind} options={[{ value: "all", label: t("all") }, ...kinds.map((k) => ({ value: k.id, label: `${t(k.key)} ${n(events.filter((e) => e.kind === k.id).length)}` }))]} />
        <Toggle checked={unreadOnly} onChange={setUnreadOnly} label={t("unreadOnly")} />
      </div>
      {days.length === 0 ? (
        <div className="glass rounded-[18px]"><Empty icon={<BellOff className="size-7" />} title={t("noEvents")} hint={t("alertsHint")} /></div>
      ) : (
        <div className="flex flex-col gap-[var(--gap)]">
          {days.map(([day, items]) => (
            <Panel key={day} title={day} sub={<span className="num">{n(items.length)}</span>}>
              <ul className="flex flex-col">
                {items.map((e) => {
                  const m = eventMeta(e.kind);
                  const sev = severity(e);
                  return (
                    <li key={e.id} className={cx("flex items-start gap-3 py-3 border-b border-hair last:border-0", e.id > lastRead && "relative")}>
                      {e.id > lastRead && <span className="absolute [inset-inline-start:-12px] top-5 size-1.5 rounded-full bg-primary2" aria-label={t("unread")} />}
                      <span className="grid place-items-center size-8 rounded-lg shrink-0" style={{ color: m.color, background: `color-mix(in oklab, ${m.color} 14%, transparent)` }}><m.icon className="size-4" /></span>
                      <div className="flex-1 min-w-0">
                        <EventMsg message={e.message} className="block text-[13.5px]" />
                        <div className="flex items-center gap-2 mt-1">
                          <Badge color={sevColor[sev]} className="h-5 text-[11px]">{t(sev === "high" ? "sevHigh" : sev === "medium" ? "sevMedium" : "sevLow")}</Badge>
                          <span className="text-[11.5px] text-ink3">{t((kinds.find((k) => k.id === e.kind)?.key ?? "catSettings") as Key)}</span>
                        </div>
                      </div>
                      <span className="text-[12px] text-ink3 num whitespace-nowrap">{time.format(new Date(e.at * 1000))}</span>
                    </li>
                  );
                })}
              </ul>
            </Panel>
          ))}
        </div>
      )}
    </div>
  );
}
