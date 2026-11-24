import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowLeft, Bot, Check, CheckCheck, Eye, ImagePlus, LifeBuoy, Lock, LockOpen, MessageCircleQuestion, Search, Send, Settings as SettingsIcon, Smartphone, Trash2,
  UserRound, X,
} from "lucide-react";
import { api, BASE, type SupportInfo, type SupportMessage, type SupportThread } from "../lib/api";
import { ago, bytes, cx, date, statusMeta } from "../lib/format";
import { useI18n, type Lang } from "../lib/i18n";
import { Avatar, Badge, Button, Dot, Empty, IconButton, Meter, Skeleton, StatusBadge, Textarea, useConfirm, useToast } from "../components/ui";
import { PageHead, can, navigate, useSession } from "../App";

/** Fired when a conversation was read or answered, so the menu badge refreshes at once. */
export const SUPPORT_EVENT = "dvx-support";
const touched = () => window.dispatchEvent(new Event(SUPPORT_EVENT));

type Filter = "open" | "waiting" | "closed" | "all";
type ThreadData = { thread: SupportThread; messages: SupportMessage[]; more: boolean };

const whoOf = (th: SupportThread) => th.user?.name || th.name || (th.username ? "@" + th.username : String(th.chat));
const avatarOf = (th: SupportThread) => {
  const x = th.user?.name || th.username || th.name;
  return x && /[A-Za-z0-9]/.test(x) ? x : String(th.chat);
};
const clock = (at: number, lang: Lang) =>
  new Intl.DateTimeFormat(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit" }).format(new Date(at * 1000));
/** The direction of a text from its first letter, so a preview is cut at its own end. */
const RTL = /[\p{Script=Arabic}\p{Script=Hebrew}]/u;
const textDir = (s: string) => {
  const m = /[A-Za-z\p{Script=Arabic}\p{Script=Hebrew}]/u.exec(s);
  return m && RTL.test(m[0]) ? "rtl" : "ltr";
};
const sameDay = (a: number, b: number) => new Date(a * 1000).toDateString() === new Date(b * 1000).toDateString();

/** The time of a list row: the hour today, the weekday this week, else the date. */
function when(at: number, lang: Lang) {
  const now = Date.now() / 1000;
  if (sameDay(at, now)) return clock(at, lang);
  const loc = lang === "fa" ? "fa-IR" : "en-GB";
  if (now - at < 6 * 86400) return new Intl.DateTimeFormat(loc, { weekday: "short" }).format(new Date(at * 1000));
  return new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", { month: "short", day: "numeric" }).format(new Date(at * 1000));
}

/** Support: what people write to the bot, one conversation each, answered from here. */
export function SupportPage({ arg }: { arg?: string }) {
  const { t, n } = useI18n();
  const { me } = useSession();
  const owner = can(me.admin.role, "owner");
  const [filter, setFilter] = useState<Filter>("open");
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [info, setInfo] = useState<SupportInfo | null>(null);
  const [err, setErr] = useState("");
  const selected = arg && /^\d+$/.test(arg) ? Number(arg) : 0;

  useEffect(() => {
    const id = setTimeout(() => setQuery(q.trim()), 250);
    return () => clearTimeout(id);
  }, [q]);
  const load = useCallback(() => {
    const p = new URLSearchParams({ status: filter === "all" ? "" : filter, q: query });
    return api<SupportInfo>("support?" + p).then((r) => { setInfo(r); setErr(""); }).catch((e) => setErr(e.message));
  }, [filter, query]);
  useEffect(() => {
    load();
    const id = setInterval(load, 8000);
    return () => clearInterval(id);
  }, [load]);

  if (!info) return err ? <Empty icon={<LifeBuoy className="size-7" />} title={err} /> : <div className="grid gap-4"><Skeleton className="h-16" /><Skeleton className="h-[480px]" /></div>;
  const c = info.counts;
  const total = c.open + c.closed;
  const configured = info.bot.configured;

  return (
    <div>
      {/* on phones an open conversation takes the whole height */}
      <div className={cx(selected > 0 && "max-lg:hidden")}>
        <PageHead title={t("support")}
          sub={total > 0 ? t("spSub").replace("{waiting}", n(c.waiting)).replace("{open}", n(c.open)) : undefined}
          action={owner && <Button size="sm" icon={<SettingsIcon className="size-4" />} onClick={() => navigate("settings", "telegram")}>{t("spSettings")}</Button>} />
      </div>

      {(!configured || !info.on) && total > 0 && (
        <div role="status" className="mb-4 flex flex-wrap items-center gap-3 rounded-2xl border border-warn/35 bg-warn/[.07] px-4 py-2.5 text-[13px]">
          <Bot className="size-4 text-warn shrink-0" />
          <span className="flex-1 min-w-0">{!configured ? t("spNoBotHint") : t("spOff")}</span>
          {owner && <Button size="sm" onClick={() => navigate("settings", "telegram")}>{t("spSettings")}</Button>}
        </div>
      )}

      {total === 0 && !query ? (
        <div className="glass rounded-[20px]">
          {!configured ? (
            <Empty icon={<Bot className="size-7" />} title={t("spNoBot")} hint={t("spNoBotHint")}
              action={owner && <Button variant="primary" onClick={() => navigate("settings", "telegram")}>{t("bcSetUpBot")}</Button>} />
          ) : (
            <Empty icon={<MessageCircleQuestion className="size-7" />} title={t("spNoThreads")} hint={info.on ? t("spNoThreadsHint") : t("spOff")}
              action={owner && !info.on && <Button variant="primary" onClick={() => navigate("settings", "telegram")}>{t("spSettings")}</Button>} />
          )}
        </div>
      ) : (
        <div className="glass edge rounded-[20px] overflow-hidden lg:grid lg:grid-cols-[minmax(280px,340px)_1fr] lg:h-[calc(100dvh-14rem)] lg:min-h-[540px]">
          <aside className={cx("min-w-0 lg:flex flex-col lg:border-e border-hair", selected ? "hidden" : "flex")}>
            <ThreadList info={info} filter={filter} setFilter={setFilter} q={q} setQ={setQ} selected={selected} />
          </aside>
          <section className={cx("min-w-0 min-h-0 lg:flex flex-col", selected ? "flex h-[calc(100dvh-11rem)] min-h-[420px] lg:h-auto lg:min-h-0" : "hidden")}>
            {selected ? (
              <Conversation key={selected} id={selected} maxAnswer={info.maxAnswer} canSend={configured} onChange={load} />
            ) : (
              <div className="flex-1 grid place-items-center"><Empty icon={<LifeBuoy className="size-7" />} title={t("spPick")} hint={t("spPickHint")} /></div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- the list

function ThreadList({ info, filter, setFilter, q, setQ, selected }: {
  info: SupportInfo; filter: Filter; setFilter: (f: Filter) => void; q: string; setQ: (s: string) => void; selected: number;
}) {
  const { t, lang, n } = useI18n();
  const c = info.counts;
  return (
    <>
      <div className="p-3 flex flex-col gap-2.5 border-b border-hair">
        <div className="relative">
          <Search className="size-4 text-ink3 absolute top-1/2 -translate-y-1/2 [inset-inline-start:12px] pointer-events-none" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("spSearch")} aria-label={t("spSearch")} dir="auto" spellCheck={false}
            className="w-full h-10 rounded-xl bg-rail border border-hair ps-9 pe-3 text-[13px] outline-none focus:border-primary2/60 focus:shadow-[0_0_0_4px_color-mix(in_oklab,var(--primary)_14%,transparent)]" />
        </div>
        <div role="tablist" aria-label={t("support")} className="grid grid-cols-4 gap-1 p-1 rounded-xl bg-fg/[.035] border border-hair">
          {([
            { id: "open", label: t("spOpen"), count: c.open },
            { id: "waiting", label: t("spWaiting"), count: c.waiting },
            { id: "closed", label: t("spClosed"), count: c.closed },
            { id: "all", label: t("all"), count: c.open + c.closed },
          ] as const).map((x) => (
            <button key={x.id} role="tab" aria-selected={filter === x.id} onClick={() => setFilter(x.id)}
              className={cx("h-11 rounded-lg flex flex-col items-center justify-center leading-tight transition-[background-color,color] duration-150",
                filter === x.id ? "bg-fg/[.09] text-ink shadow-[inset_0_1px_0_var(--edge)]" : "text-ink3 hover:text-ink2 hover:bg-fg/[.04]")}>
              <span className={cx("num text-[14px]", filter === x.id ? "font-bold" : "font-semibold", x.id === "waiting" && x.count > 0 && "text-warn")}>{n(x.count)}</span>
              <span className="text-[11px] truncate max-w-full px-1">{x.label}</span>
            </button>
          ))}
        </div>
      </div>
      <ul className="flex-1 overflow-y-auto overscroll-contain p-1.5" aria-label={t("support")}>
        {info.threads.length === 0 && <li className="px-4 py-10 text-center text-[13px] text-ink3">{t("spNoMatch")}</li>}
        {info.threads.map((th) => {
          const on = th.id === selected;
          return (
            <li key={th.id}>
              <a href={"#/support/" + th.id} aria-current={on ? "true" : undefined}
                className={cx("group relative flex gap-3 rounded-2xl px-3 py-2.5 transition-[background-color] duration-150",
                  on ? "bg-primary/[.13] shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--primary)_30%,transparent)]" : "hover:bg-fg/[.045]")}>
                <Avatar name={avatarOf(th)} size={40} online={th.user ? th.user.online : undefined} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline gap-2">
                    <bdi className={cx("truncate text-[13.5px]", th.unread > 0 ? "font-bold text-ink" : "font-semibold text-ink")}>{whoOf(th)}</bdi>
                    {!th.user && <span className="shrink-0 text-[10.5px] text-ink3 border border-hair rounded-full px-1.5 leading-4">{t("spGuest")}</span>}
                    <span className={cx("ms-auto shrink-0 num text-[11.5px]", th.unread > 0 ? "text-primary2 font-semibold" : "text-ink3")}>{when(th.lastAt, lang)}</span>
                  </span>
                  <span className="mt-0.5 flex items-center gap-2">
                    <span dir={textDir(th.lastText)} className={cx("truncate text-[12.5px] flex-1 text-start", th.unread > 0 ? "text-ink2" : "text-ink3")}>
                      {th.lastStaff && <bdi className="text-ink3">{t("spYou")} </bdi>}{th.lastText || "…"}
                    </span>
                    {th.unread > 0 ? (
                      <span className="shrink-0 num text-[11px] font-semibold rounded-full min-w-5 h-5 px-1.5 grid place-items-center bg-primary text-white shadow-[0_4px_12px_-4px_var(--primary)]">{n(th.unread)}</span>
                    ) : th.waiting ? (
                      <span className="shrink-0 size-2 rounded-full bg-warn" title={t("spWaiting")} />
                    ) : !th.open ? (
                      <Lock className="shrink-0 size-3.5 text-ink3" aria-label={t("spClosed")} />
                    ) : null}
                  </span>
                </span>
              </a>
            </li>
          );
        })}
      </ul>
    </>
  );
}

// ---------------------------------------------------------------- one conversation

function Conversation({ id, maxAnswer, canSend, onChange }: { id: number; maxAnswer: number; canSend: boolean; onChange: () => void }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const confirm = useConfirm();
  const [data, setData] = useState<ThreadData | null>(null);
  const [older, setOlder] = useState<SupportMessage[]>([]);
  const [olderMore, setOlderMore] = useState<boolean | null>(null);
  const [err, setErr] = useState("");
  const stream = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  const load = useCallback(() => api<ThreadData>(`support/threads/${id}`).then((r) => {
    setData(r); setErr("");
    if (r.thread.unread > 0 && document.visibilityState === "visible") {
      api(`support/threads/${id}/seen`, { method: "POST" }).then(() => { touched(); onChange(); }).catch(() => {});
    }
  }).catch((e) => setErr(e.message)), [id, onChange]);
  useEffect(() => {
    load();
    const iv = setInterval(load, 4000);
    const vis = () => document.visibilityState === "visible" && load();
    document.addEventListener("visibilitychange", vis);
    return () => { clearInterval(iv); document.removeEventListener("visibilitychange", vis); };
  }, [load]);

  const messages = useMemo(() => [...older, ...(data?.messages ?? [])], [older, data]);
  const last = messages[messages.length - 1]?.id ?? 0;
  // follow new messages while the reader is at the bottom
  useLayoutEffect(() => {
    const el = stream.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [last]);
  // photos load after the first layout: keep the newest message in view as they grow the stream
  const content = useRef<HTMLDivElement>(null);
  const ready = !!data;
  useEffect(() => {
    const el = content.current;
    if (!el) return;
    const ro = new ResizeObserver(() => { const s = stream.current; if (s && stick.current) s.scrollTop = s.scrollHeight; });
    ro.observe(el);
    return () => ro.disconnect();
  }, [ready]);
  const onScroll = () => {
    const el = stream.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };
  const loadOlder = () => {
    const first = messages[0]?.id;
    if (!first) return;
    const el = stream.current;
    const before = el ? el.scrollHeight - el.scrollTop : 0;
    api<ThreadData>(`support/threads/${id}?before=${first}`).then((r) => {
      stick.current = false;
      setOlder((o) => [...r.messages, ...o]);
      setOlderMore(r.more);
      requestAnimationFrame(() => { if (el) el.scrollTop = el.scrollHeight - before; });
    }).catch((e) => toast(e.message, "error"));
  };

  if (!data) return err ? (
    <div className="flex-1 grid place-items-center">
      <Empty icon={<LifeBuoy className="size-7" />} title={err} action={<Button icon={<ArrowLeft className="size-4 rtl:rotate-180" />} onClick={() => navigate("support")}>{t("spBack")}</Button>} />
    </div>
  ) : <div className="p-4 grid gap-3"><Skeleton className="h-14" /><Skeleton className="h-64" /></div>;
  const th = data.thread;
  const more = olderMore ?? data.more;
  const setOpen = (open: boolean) => api(`support/threads/${id}`, { method: "PUT", body: { open } }).then(() => { load(); onChange(); touched(); }).catch((e) => toast(e.message, "error"));
  const remove = async () => {
    if (!(await confirm({ title: t("spDelete"), body: t("spDeleteBody"), danger: true }))) return;
    api(`support/threads/${id}`, { method: "DELETE" }).then(() => { touched(); onChange(); navigate("support"); }).catch((e) => toast(e.message, "error"));
  };

  return (
    <div className="flex-1 min-h-0 flex 2xl:grid 2xl:grid-cols-[1fr_290px]">
      <div className="flex-1 min-w-0 min-h-0 flex flex-col">
        <Header th={th} onOpen={setOpen} onDelete={remove} />
        <div ref={stream} onScroll={onScroll} className="flex-1 min-h-0 overflow-y-auto overscroll-contain px-3 sm:px-5 py-4 bg-[radial-gradient(120%_60%_at_50%_0%,color-mix(in_oklab,var(--primary)_5%,transparent),transparent_70%)]">
          <div ref={content}>
            {more && (
              <div className="flex justify-center mb-3"><Button size="sm" variant="quiet" onClick={loadOlder}>{t("spOlder")}</Button></div>
            )}
            {messages.map((m, i) => (
              <div key={m.id}>
                {(i === 0 || !sameDay(messages[i - 1].at, m.at)) && <DaySep at={m.at} />}
                <Bubble m={m} grouped={i > 0 && messages[i - 1].staff === m.staff && m.at - messages[i - 1].at < 300 && sameDay(messages[i - 1].at, m.at)} />
              </div>
            ))}
            {!th.open && (
              <p className="mt-4 mx-auto max-w-md text-center text-[12px] text-ink3 leading-relaxed">
                <Lock className="inline size-3.5 me-1 -mt-0.5" />
                {t("spClosedBy").replace("{by}", th.closedBy || "—").replace("{ago}", ago(th.closedAt, lang))} {t("spClosedNote")}
              </p>
            )}
          </div>
        </div>
        <Composer id={id} maxAnswer={maxAnswer} canSend={canSend} onSent={() => { stick.current = true; load(); onChange(); touched(); }} />
      </div>
      <Details th={th} />
    </div>
  );
}

function Header({ th, onOpen, onDelete }: { th: SupportThread; onOpen: (open: boolean) => void; onDelete: () => void }) {
  const { t, lang, n } = useI18n();
  const u = th.user;
  return (
    <header className="shrink-0 border-b border-hair px-3 sm:px-4 py-2.5">
      <div className="flex items-center gap-2.5">
        <IconButton label={t("spBack")} className="lg:hidden -ms-1" onClick={() => navigate("support")}><ArrowLeft className="size-4 rtl:rotate-180" /></IconButton>
        <Avatar name={avatarOf(th)} size={38} online={u ? u.online : undefined} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 min-w-0">
            <bdi className="truncate text-[15px] font-semibold">{whoOf(th)}</bdi>
            <span className="max-sm:hidden shrink-0">{u ? <StatusBadge status={u.status} /> : <Badge className="h-5 text-[10.5px] px-2" color="var(--color-ink3)">{t("spGuest")}</Badge>}</span>
          </div>
          <div className="flex items-center gap-1.5 text-[12px] text-ink3 min-w-0">
            <span className="sm:hidden shrink-0 inline-flex items-center gap-1" style={{ color: u ? statusMeta[u.status].color : undefined }}>
              <Dot color={u ? statusMeta[u.status].color : "var(--color-ink3)"} size={6} />{u ? t(statusMeta[u.status].key) : t("spGuest")}<span className="text-ink3">·</span>
            </span>
            <span className="truncate" dir="ltr">{[th.name, th.username && "@" + th.username].filter(Boolean).join(" · ") || "Telegram"}</span>
          </div>
        </div>
        <div className="flex items-center gap-0.5 shrink-0">
          {u && <IconButton label={t("spOpenUser")} onClick={() => navigate("users", String(u.id))}><UserRound className="size-4" /></IconButton>}
          {th.open
            ? <IconButton label={t("spClose")} onClick={() => onOpen(false)}><Lock className="size-4" /></IconButton>
            : <IconButton label={t("spReopen")} onClick={() => onOpen(true)}><LockOpen className="size-4" /></IconButton>}
          <IconButton label={t("spDelete")} tone="danger" onClick={onDelete}><Trash2 className="size-4" /></IconButton>
        </div>
      </div>
      {u && (
        <div className="2xl:hidden mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-ink2 ps-[calc(38px+.625rem)] max-lg:ps-0">
          <span className="inline-flex items-center gap-2 min-w-[150px]">
            <span className="num">{bytes(u.used, lang)}{u.dataLimit > 0 ? " / " + bytes(u.dataLimit, lang) : ""}</span>
            {u.dataLimit > 0 && <Meter className="w-20" value={u.used} max={u.dataLimit} height={4} />}
          </span>
          {u.daysLeft !== null && <span className="num">{n(u.daysLeft)} {t("days")}</span>}
          <span className="inline-flex items-center gap-1 num"><Smartphone className="size-3.5 text-ink3" />{n(u.devices)}{u.ipLimit > 0 ? " / " + n(u.ipLimit) : ""}</span>
          {u.owner && <span className="text-ink3">{t("spReseller")}: <bdi dir="ltr">{u.owner}</bdi></span>}
        </div>
      )}
    </header>
  );
}

function DaySep({ at }: { at: number }) {
  const { t, lang } = useI18n();
  const now = Date.now() / 1000;
  const label = sameDay(at, now) ? t("today") : sameDay(at, now - 86400) ? t("spYesterday") : date(at, lang);
  return (
    <div className="flex items-center gap-3 my-4 first:mt-0" role="separator">
      <span className="h-px flex-1 bg-hair" />
      <span className="text-[11.5px] text-ink3 num">{label}</span>
      <span className="h-px flex-1 bg-hair" />
    </div>
  );
}

function Bubble({ m, grouped }: { m: SupportMessage; grouped: boolean }) {
  const { t, lang } = useI18n();
  const staff = m.staff;
  const receipt = staff ? null : m.answered ? { icon: <CheckCheck className="size-3.5 text-online" />, label: t("spAnsweredR") }
    : m.seen ? { icon: <Eye className="size-3.5 text-primary2" />, label: t("spSeenR") }
      : { icon: <Check className="size-3.5" />, label: t("spArrived") };
  return (
    <div className={cx("flex", staff ? "justify-end" : "justify-start", grouped ? "mt-1" : "mt-3")}>
      <div className={cx("max-w-[min(560px,85%)] flex flex-col", staff ? "items-end" : "items-start")}>
        <div className={cx("rounded-2xl border overflow-hidden shadow-[0_8px_22px_-18px_rgba(0,0,0,.7)]",
          staff ? "bg-primary/[.14] border-primary/30 rounded-ee-md" : "bg-panel2 border-hair rounded-ss-md")}>
          {m.photo && (
            <a href={BASE + "support/photos/" + m.photo} target="_blank" rel="noreferrer" className="block">
              <img src={BASE + "support/photos/" + m.photo} alt={t("spPhoto")} decoding="async" className="block max-h-72 min-h-24 min-w-40 w-auto max-w-full object-contain bg-fg/[.04]" />
            </a>
          )}
          {m.text && <p dir="auto" className="px-3.5 py-2 text-[14px] leading-[1.75] whitespace-pre-wrap [overflow-wrap:anywhere]">{m.text}</p>}
        </div>
        <div className="mt-1 px-1 flex items-center gap-1.5 text-[11px] text-ink3">
          {staff && m.by && <><UserRound className="size-3" /><bdi dir="ltr">{m.by}</bdi><span>·</span></>}
          <span className="num">{clock(m.at, lang)}</span>
          {receipt && <span className="inline-flex items-center gap-1" title={receipt.label}>{receipt.icon}<span className="sr-only">{receipt.label}</span></span>}
        </div>
      </div>
    </div>
  );
}

function Composer({ id, maxAnswer, canSend, onSent }: { id: number; maxAnswer: number; canSend: boolean; onSent: () => void }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [text, setText] = useState("");
  const [photo, setPhoto] = useState<File | null>(null);
  const [preview, setPreview] = useState("");
  const [busy, setBusy] = useState(false);
  const area = useRef<HTMLTextAreaElement>(null);
  const file = useRef<HTMLInputElement>(null);
  const lastTyping = useRef(0);
  useEffect(() => {
    if (!photo) return setPreview("");
    const url = URL.createObjectURL(photo);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [photo]);
  // grow with the text, up to about eight lines
  useLayoutEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 220) + "px";
    el.style.overflowY = el.scrollHeight > 220 ? "auto" : "hidden";
  }, [text]);

  const typing = () => {
    const now = Date.now();
    if (now - lastTyping.current > 4500 && canSend) {
      lastTyping.current = now;
      api(`support/threads/${id}/typing`, { method: "POST" }).catch(() => {});
    }
  };
  const pick = (f?: File | null) => {
    if (!f) return;
    if (f.size > 5 << 20) return toast(t("spPhotoTooBig"), "error");
    setPhoto(f);
    area.current?.focus();
  };
  const ready = (text.trim() !== "" || !!photo) && text.length <= maxAnswer && canSend && !busy;
  const send = () => {
    if (!ready) return;
    setBusy(true);
    let req: Promise<unknown>;
    if (photo) {
      const form = new FormData();
      form.append("text", text);
      form.append("photo", photo);
      req = api(`support/threads/${id}/reply`, { form });
    } else {
      req = api(`support/threads/${id}/reply`, { body: { text } });
    }
    req.then(() => { setText(""); setPhoto(null); toast(t("spSent")); onSent(); requestAnimationFrame(() => area.current?.focus()); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };

  return (
    <div className="shrink-0 border-t border-hair p-2.5 sm:p-3 bg-fg/[.015]"
      onDragOver={(e) => { if (e.dataTransfer.types.includes("Files")) e.preventDefault(); }}
      onDrop={(e) => { e.preventDefault(); pick(e.dataTransfer.files?.[0]); }}>
      {preview && (
        <div className="mb-2 inline-flex items-center gap-2 rounded-xl border border-hair bg-fg/[.03] p-1.5 pe-2">
          <img src={preview} alt="" className="size-12 rounded-lg object-cover" />
          <span className="text-[12px] text-ink2 max-w-48 truncate" dir="ltr">{photo?.name}</span>
          <IconButton label={t("spRemovePhoto")} className="!size-7" onClick={() => setPhoto(null)}><X className="size-3.5" /></IconButton>
        </div>
      )}
      <div className="flex items-end gap-2">
        <input ref={file} type="file" accept="image/jpeg,image/png,image/webp,image/gif" className="hidden" onChange={(e) => { pick(e.target.files?.[0]); e.target.value = ""; }} />
        <IconButton label={t("spAttach")} className="shrink-0 mb-0.5" disabled={!canSend} onClick={() => file.current?.click()}><ImagePlus className="size-[18px]" /></IconButton>
        <Textarea ref={area} rows={1} dir="auto" value={text} placeholder={t("spReplyPlaceholder")} disabled={!canSend}
          onChange={(e) => { setText(e.target.value); typing(); }}
          onPaste={(e) => { const f = [...e.clipboardData.files].find((x) => x.type.startsWith("image/")); if (f) { e.preventDefault(); pick(f); } }}
          onKeyDown={(e) => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); send(); } }}
          className="!min-h-11 !h-11 resize-none leading-relaxed text-[14px]" />
        <Button variant="primary" className="shrink-0 mb-0.5" icon={<Send className="size-4 rtl:-scale-x-100" />} loading={busy} disabled={!ready} onClick={send}>
          <span className="max-sm:sr-only">{t("spSend")}</span>
        </Button>
      </div>
      <div className="mt-1.5 flex items-center justify-between gap-3 px-1 text-[11.5px] text-ink3">
        <span className="truncate max-sm:hidden">{t("spSendHint")}</span>
        <span className={cx("num ms-auto shrink-0", text.length > maxAnswer && "text-bad")}>{n(text.length)} / {n(maxAnswer)}</span>
      </div>
    </div>
  );
}

function Details({ th }: { th: SupportThread }) {
  const { t, lang, n } = useI18n();
  const u = th.user;
  const Row = ({ label, children }: { label: string; children: ReactNode }) => (
    <div className="flex items-baseline justify-between gap-3 py-2 border-b border-hair last:border-0 text-[12.5px]">
      <span className="text-ink3 shrink-0">{label}</span>
      <span className="text-ink text-end min-w-0 truncate">{children}</span>
    </div>
  );
  return (
    <aside className="hidden 2xl:flex flex-col min-h-0 overflow-y-auto border-s border-hair p-4 gap-5">
      <div>
        <h3 className="text-[12.5px] font-semibold text-ink2 mb-2">{t("spSubscription")}</h3>
        {u ? (
          <div className="rounded-2xl border border-hair bg-fg/[.025] p-3.5">
            <div className="flex items-center gap-2.5">
              <Avatar name={u.name} size={34} online={u.online} />
              <div className="min-w-0">
                <bdi dir="ltr" className="block truncate text-[14px] font-semibold">{u.name}</bdi>
                <StatusBadge status={u.status} />
              </div>
            </div>
            <div className="mt-3.5">
              <div className="flex items-baseline justify-between text-[12px]">
                <span className="text-ink3">{t("usage")}</span>
                <span className="num text-ink2">{bytes(u.used, lang)}{u.dataLimit > 0 ? " / " + bytes(u.dataLimit, lang) : ""}</span>
              </div>
              {u.dataLimit > 0 && <Meter className="mt-1.5" value={u.used} max={u.dataLimit} />}
            </div>
            <div className="mt-2">
              {u.daysLeft !== null && <Row label={t("spDaysLeft")}><span className="num">{n(u.daysLeft)}</span></Row>}
              <Row label={t("devices")}><span className="num">{n(u.devices)}{u.ipLimit > 0 ? " / " + n(u.ipLimit) : ""}</span></Row>
              {u.owner && <Row label={t("spReseller")}><bdi dir="ltr">{u.owner}</bdi></Row>}
              {u.note && <Row label="📝"><span dir="auto">{u.note}</span></Row>}
            </div>
            <Button size="sm" className="mt-3 w-full" icon={<UserRound className="size-4" />} onClick={() => navigate("users", String(u.id))}>{t("spOpenUser")}</Button>
          </div>
        ) : (
          <p className="text-[12.5px] text-ink3 leading-relaxed">{t("spNoUser")}</p>
        )}
      </div>
      <div>
        <h3 className="text-[12.5px] font-semibold text-ink2 mb-1">{t("spTelegram")}</h3>
        {th.name && <Row label={t("name")}><bdi>{th.name}</bdi></Row>}
        {th.username && <Row label={t("username")}><a className="text-primary2 hover:underline" dir="ltr" href={"https://t.me/" + th.username} target="_blank" rel="noreferrer">@{th.username}</a></Row>}
        <Row label={t("spChatId")}><span className="mono text-[12px]" dir="ltr">{th.chat}</span></Row>
        <Row label={t("spStarted")}>{date(th.createdAt, lang)}</Row>
        <Row label={t("spMessages")}><span className="num">{n(th.messages)}</span></Row>
      </div>
    </aside>
  );
}
