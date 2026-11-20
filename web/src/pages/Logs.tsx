import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownToLine, Copy, Download, Pause, Play, Search } from "lucide-react";
import { api, type Event } from "../lib/api";
import { copyText, cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Button, Input, Segmented, Toggle, useToast } from "../components/ui";
import { PageHead } from "../App";

type Level = "error" | "warn" | "info" | "debug";
type Source = "xray" | "sing-box" | "panel";
interface Row { id: string; src: Source; level: Level; time: string; text: string }

const levelColor: Record<Level, string> = { error: "var(--bad)", warn: "var(--warn)", info: "var(--primary2)", debug: "var(--text3)" };

function levelOf(s: string): Level {
  const l = s.toLowerCase();
  if (/\b(error|fatal|panic|failed)\b/.test(l)) return "error";
  if (/\b(warn|warning)\b/.test(l)) return "warn";
  if (/\bdebug\b/.test(l)) return "debug";
  return "info";
}

/** Live log stream of both cores and the panel's own events. */
export function LogsPage() {
  const { t, n } = useI18n();
  const toast = useToast();
  const [rows, setRows] = useState<Row[]>([]);
  const [paused, setPaused] = useState(false);
  const [auto, setAuto] = useState(true);
  const [q, setQ] = useState("");
  const [lv, setLv] = useState<"all" | Level>("all");
  const [src, setSrc] = useState<"all" | Source>("all");
  const [open, setOpen] = useState<string | null>(null);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (paused) return;
    let alive = true;
    const load = async () => {
      const [x, s, ev] = await Promise.all([
        api<string[]>("cores/xray/logs").catch(() => [] as string[]),
        api<string[]>("cores/sing-box/logs").catch(() => [] as string[]),
        api<Event[]>("events?limit=200").catch(() => [] as Event[]),
      ]);
      if (!alive) return;
      const mk = (src: Source) => (line: string, i: number): Row => {
        const m = line.match(/^(\S+\s\S+)\s(.*)$/);
        return { id: `${src}-${i}-${line.length}`, src, level: levelOf(line), time: m && /\d/.test(m[1]) ? m[1] : "", text: m && /\d/.test(m[1]) ? m[2] : line };
      };
      const evRows = ev.slice().reverse().map((e): Row => ({ id: "p-" + e.id, src: "panel", level: e.kind === "core" ? "warn" : "info", time: new Date(e.at * 1000).toLocaleString("en-GB"), text: `[${e.kind}] ${e.message}` }));
      setRows([...x.map(mk("xray")), ...s.map(mk("sing-box")), ...evRows]);
    };
    load();
    const id = setInterval(load, 2500);
    return () => { alive = false; clearInterval(id); };
  }, [paused]);

  const list = useMemo(() => {
    const s = q.trim().toLowerCase();
    return rows.filter((r) => (lv === "all" || r.level === lv) && (src === "all" || r.src === src) && (!s || r.text.toLowerCase().includes(s))).slice(-1500);
  }, [rows, q, lv, src]);
  const counts = useMemo(() => ({ error: rows.filter((r) => r.level === "error").length, warn: rows.filter((r) => r.level === "warn").length }), [rows]);

  useEffect(() => {
    if (auto && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [list, auto]);

  const exportLog = () => {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([list.map((r) => `${r.time} [${r.src}] [${r.level}] ${r.text}`).join("\n")], { type: "text/plain" }));
    a.download = "dunvex-logs.txt";
    a.click();
  };

  return (
    <div>
      <PageHead title={t("logs")} sub={t("logsSub")}
        action={<>
          <Button icon={paused ? <Play className="size-4" /> : <Pause className="size-4" />} onClick={() => setPaused(!paused)}>{paused ? t("resume") : t("pause")}</Button>
          <Button icon={<Download className="size-4" />} onClick={exportLog}>{t("export")}</Button>
        </>} />
      <div className="flex flex-wrap items-center gap-3 mb-3">
        <div className="relative w-full sm:w-72">
          <Search className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("searchLogs")} className="ps-10" aria-label={t("searchLogs")} spellCheck={false} />
        </div>
        <Segmented value={lv} onChange={setLv} options={[{ value: "all", label: t("all") }, { value: "error", label: `${t("errors")} ${n(counts.error)}` }, { value: "warn", label: `${t("warnings")} ${n(counts.warn)}` }, { value: "info", label: t("infoLevel") }]} />
        <Segmented value={src} onChange={setSrc} options={[{ value: "all", label: t("allSources") }, { value: "xray", label: "Xray" }, { value: "sing-box", label: "sing-box" }, { value: "panel", label: t("panel") }]} />
        <span className="ms-auto"><Toggle checked={auto} onChange={setAuto} label={<span className="inline-flex items-center gap-1.5"><ArrowDownToLine className="size-4" />{t("autoScroll")}</span>} /></span>
      </div>
      <div ref={box} className="glass rounded-[18px] h-[64vh] overflow-y-auto overscroll-contain p-2 mono text-[12px] leading-relaxed" dir="ltr" role="log" aria-live="off">
        {list.map((r) => (
          <div key={r.id} style={{ contentVisibility: "auto", containIntrinsicSize: "0 22px" }}
            className={cx("group flex items-start gap-2 px-2 py-0.5 rounded-lg hover:bg-fg/[.04] cursor-pointer", open === r.id && "bg-fg/[.05]")} onClick={() => setOpen(open === r.id ? null : r.id)}>
            <span className="w-1 self-stretch rounded-full shrink-0" style={{ background: levelColor[r.level] }} />
            <span className="text-ink3 shrink-0 w-[150px] truncate">{r.time}</span>
            <span className="shrink-0 w-[64px] truncate" style={{ color: r.src === "panel" ? "var(--cyan)" : "var(--text2)" }}>{r.src}</span>
            <span className={cx("flex-1 min-w-0", open === r.id ? "whitespace-pre-wrap break-all" : "truncate")} style={{ color: r.level === "error" ? "var(--bad)" : r.level === "warn" ? "var(--warn)" : undefined }}>{r.text}</span>
            <button onClick={(e) => { e.stopPropagation(); copyText(r.text).then(() => toast(t("copied"))); }} aria-label={t("copy")} className="opacity-0 group-hover:opacity-100 text-ink3 hover:text-ink"><Copy className="size-3.5" /></button>
          </div>
        ))}
        {list.length === 0 && <div className="text-center text-ink3 py-16 font-sans">{t("noLogs")}</div>}
      </div>
      <div className="mt-2 flex gap-2 text-[12px] text-ink3"><Badge color="var(--text3)">{n(list.length)}</Badge>{paused && <Badge color="var(--warn)">{t("paused2")}</Badge>}</div>
    </div>
  );
}
