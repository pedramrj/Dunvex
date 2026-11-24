import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Bot, CircleAlert, CircleCheck, CircleSlash, Megaphone, Send, Square, TestTube2, UserRound, UserX, Users as UsersIcon } from "lucide-react";
import { api, type BroadcastAudience, type BroadcastInfo, type BroadcastRun } from "../lib/api";
import { ago, cx } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, Empty, Input, Label, Meter, Panel, Skeleton, Textarea, Toggle, useConfirm, useToast } from "../components/ui";
import { PageHead, can, navigate, useSession } from "../App";

const MAX = 3500;
const audiences: { id: Exclude<BroadcastAudience, "selected">; key: Key; hint: Key; icon: typeof UsersIcon; color: string }[] = [
  { id: "all", key: "bcAll", hint: "bcAllHint", icon: UsersIcon, color: "var(--primary2)" },
  { id: "active", key: "bcActive", hint: "bcActiveHint", icon: CircleCheck, color: "var(--ok)" },
  { id: "ending", key: "bcEnding", hint: "bcEndingHint", icon: CircleAlert, color: "var(--warn)" },
  { id: "ended", key: "bcEnded", hint: "bcEndedHint", icon: UserX, color: "var(--bad)" },
];
const placeholders = ["{name}", "{days}", "{data}", "{link}"] as const;

/** Broadcast: one message to many users through the Telegram bot. */
export function BroadcastPage({ arg }: { arg?: string }) {
  const { t, n } = useI18n();
  const { me } = useSession();
  const toast = useToast();
  const confirm = useConfirm();
  const isAdmin = can(me.admin.role, "admin");
  const picked = useMemo(() => (arg?.startsWith("u") ? arg.slice(1).split("-").map(Number).filter((x) => x > 0) : []), [arg]);
  const [info, setInfo] = useState<BroadcastInfo | null>(null);
  const [err, setErr] = useState("");
  const [aud, setAud] = useState<BroadcastAudience>(picked.length ? "selected" : "all");
  const [days, setDays] = useState(7);
  const [text, setText] = useState("");
  const [html, setHtml] = useState(false);
  const [button, setButton] = useState(true);
  const [busy, setBusy] = useState("");
  const [sel, setSel] = useState<{ users: number; linked: number } | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);

  const load = useCallback(() => api<BroadcastInfo>(`broadcasts?days=${days}`).then((r) => { setInfo(r); setErr(""); }).catch((e) => setErr(e.message)), [days]);
  useEffect(() => { load(); }, [load]);
  const running = info?.history.find((x) => !x.done);
  useEffect(() => {
    if (!running) return;
    const id = setInterval(load, 1500);
    return () => clearInterval(id);
  }, [running, load]);
  useEffect(() => {
    if (!picked.length) return;
    api<{ users: number; linked: number }>("broadcasts/count", { body: { userIds: picked } }).then(setSel).catch(() => {});
  }, [picked]);

  const count = aud === "selected" ? sel : info?.counts[aud];
  const req = () => ({ text, html, audience: aud, userIds: aud === "selected" ? picked : [], days, button });
  const insert = (p: string) => {
    const el = area.current;
    if (!el) return setText(text + p);
    const s = el.selectionStart ?? text.length, e = el.selectionEnd ?? text.length;
    setText(text.slice(0, s) + p + text.slice(e));
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(s + p.length, s + p.length); });
  };
  const test = () => {
    setBusy("test");
    api<{ sent: number }>("broadcasts", { body: { ...req(), test: true } })
      .then((r) => toast(t("bcTestSent").replace("{n}", n(r.sent))))
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const send = async () => {
    const who = count?.linked ?? 0;
    if (!(await confirm({ title: t("bcConfirm").replace("{n}", n(who)), body: t("bcConfirmBody"), confirmLabel: t("bcSendNow") }))) return;
    setBusy("send");
    api<BroadcastRun>("broadcasts", { body: req() })
      .then(() => { toast(t("bcStarted")); setText(""); load(); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const stop = (id: number) => api(`broadcasts/${id}/cancel`, { method: "POST" }).then(load).catch((e) => toast(e.message, "error"));

  if (!info) return err ? <Empty icon={<Megaphone className="size-7" />} title={err} /> : <div className="grid gap-4"><Skeleton className="h-16" /><Skeleton className="h-96" /></div>;
  const configured = !!info.bot?.configured;
  const linkedAll = info.counts.all.linked, usersAll = info.counts.all.users;

  return (
    <div>
      <PageHead title={t("broadcast")}
        sub={configured ? (
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            {info.bot?.username && <span className="inline-flex items-center gap-1.5"><Bot className="size-3.5 text-ink3" /><bdi dir="ltr">@{info.bot.username}</bdi></span>}
            <span>{t("bcReach").replace("{linked}", n(linkedAll)).replace("{users}", n(usersAll))}</span>
          </span>
        ) : undefined} />

      {!configured ? (
        <div className="glass rounded-[20px]">
          <Empty icon={<Bot className="size-7" />} title={t("bcNoBot")} hint={t("bcNoBotHint")}
            action={can(me.admin.role, "owner") && <Button variant="primary" onClick={() => navigate("settings", "telegram")}>{t("bcSetUpBot")}</Button>} />
        </div>
      ) : (
        <div className="flex flex-col gap-[var(--gap)]">
          {running && <Progress run={running} onStop={() => stop(running.id)} />}

          <div className="grid gap-[var(--gap)] xl:grid-cols-12 items-start">
            <Panel className="xl:col-span-7" icon={<Megaphone className="size-4" />} title={t("bcCompose")}>
              <Label>{t("bcWho")}</Label>
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
                {picked.length > 0 && (
                  <AudienceCard on={aud === "selected"} onClick={() => setAud("selected")} icon={<UserRound className="size-4" />} color="var(--color-cyan)"
                    label={t("bcSelected")} count={sel} className="col-span-2 sm:col-span-4" />
                )}
                {audiences.map((a) => (
                  <AudienceCard key={a.id} on={aud === a.id} onClick={() => setAud(a.id)} icon={<a.icon className="size-4" />} color={a.color}
                    label={t(a.key)} count={info.counts[a.id]} title={t(a.hint)} />
                ))}
              </div>
              <p className="mt-2 text-[12px] text-ink3 leading-relaxed">
                {aud === "ending" ? (
                  <span className="inline-flex flex-wrap items-center gap-2">
                    {t("bcEndingHint")}
                    <span className="inline-flex items-center gap-1.5">{t("bcWithin")}
                      <Input type="number" min={1} max={60} value={days} onChange={(e) => setDays(Math.max(1, Math.min(60, +e.target.value || 7)))} className="num !h-8 !w-16 text-center" />
                      {t("days")}</span>
                  </span>
                ) : aud === "selected" ? t("bcSelectedHint") : t(audiences.find((a) => a.id === aud)!.hint)}
                {" "}{t("bcOnlyLinked")}
              </p>

              <div className="mt-5">
                <Label hint={<span className={cx("num", text.length > MAX && "text-bad")}>{n(text.length)} / {n(MAX)}</span>}>{t("bcMessage")}</Label>
                <Textarea ref={area} dir="auto" rows={8} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("bcPlaceholder")} className="leading-relaxed" />
                <div className="mt-2 flex flex-wrap items-center gap-1.5">
                  <span className="text-[12px] text-ink3 me-1">{t("bcInsert")}</span>
                  {placeholders.map((p) => (
                    <button key={p} type="button" onClick={() => insert(p)} title={t(("bcPh_" + p.slice(1, -1)) as Key)}
                      className="mono text-[12px] h-7 px-2.5 rounded-lg border border-hair bg-fg/[.03] hover:bg-fg/[.07] text-ink2" dir="ltr">{p}</button>
                  ))}
                </div>
              </div>

              <div className="mt-5 flex flex-col gap-3">
                <Toggle checked={html} onChange={setHtml} label={t("bcHtml")} hint={t("bcHtmlHint")} />
                <Toggle checked={button} onChange={setButton} label={t("bcButton")} hint={t("bcButtonHint")} />
              </div>

              <div className="mt-6 pt-4 border-t border-hair flex flex-wrap items-center justify-end gap-2">
                {isAdmin && <Button icon={<TestTube2 className="size-4" />} loading={busy === "test"} disabled={!text.trim() || !!busy} onClick={test}>{t("bcTest")}</Button>}
                <Button variant="primary" icon={<Send className="size-4" />} loading={busy === "send"}
                  disabled={!text.trim() || text.length > MAX || !count?.linked || !!running || !!busy} onClick={send}>
                  {t("bcSendTo").replace("{n}", n(count?.linked ?? 0))}
                </Button>
              </div>
              {!info.userBot && <p className="mt-3 text-[12px] text-warn">{t("bcUserBotOff")}</p>}
            </Panel>

            <Panel className="xl:col-span-5 xl:sticky xl:top-24" title={t("bcPreview")} sub={t("bcPreviewSub")}>
              <TelegramPreview text={text} html={html} button={button} />
            </Panel>
          </div>

          <History list={info.history} />
        </div>
      )}
    </div>
  );
}

function AudienceCard({ on, onClick, icon, color, label, count, title, className }: {
  on: boolean; onClick: () => void; icon: ReactNode; color: string; label: string; count?: { users: number; linked: number } | null; title?: string; className?: string;
}) {
  const { n } = useI18n();
  return (
    <button type="button" onClick={onClick} title={title} aria-pressed={on}
      className={cx("text-start rounded-2xl border px-3 py-2.5 transition-[background-color,border-color] duration-150", className,
        on ? "border-primary/50 bg-primary/12" : "border-hair bg-fg/[.02] hover:bg-fg/[.05]")}>
      <div className="flex items-center gap-2 text-[13px] font-medium">
        <span className="grid place-items-center size-7 rounded-lg shrink-0" style={{ color, background: `color-mix(in oklab, ${color} 14%, transparent)` }}>{icon}</span>
        <span className="truncate">{label}</span>
      </div>
      <div className="mt-2 num text-[20px] font-bold leading-none">{count ? n(count.linked) : "—"}
        <span className="text-[12px] font-normal text-ink3"> / {count ? n(count.users) : "—"}</span>
      </div>
    </button>
  );
}

// ---------------------------------------------------------------- preview

const SAMPLE: Record<string, string> = { "{name}": "ali", "{days}": "12", "{data}": "23.4 GiB", "{link}": "https://example.com/sub/a1b2c3" };

/** The message as a user's Telegram shows it: Telegram's HTML tags drawn, the rest as text. */
function TelegramPreview({ text, html, button }: { text: string; html: boolean; button: boolean }) {
  const { t } = useI18n();
  const filled = text.replace(/\{(name|days|data|link)\}/g, (m) => SAMPLE[m]);
  return (
    <div className="rounded-2xl p-4 min-h-48 bg-[linear-gradient(160deg,color-mix(in_oklab,var(--cyan)_10%,transparent),color-mix(in_oklab,var(--primary)_8%,transparent))] border border-hair">
      {!text.trim() ? (
        <p className="text-[13px] text-ink3 text-center py-12">{t("bcPreviewEmpty")}</p>
      ) : (
        <div className="max-w-[92%]">
          <div dir="auto" className="rounded-2xl rounded-ss-md bg-panel2 border border-hair px-3.5 py-2.5 text-[14px] leading-[1.75] whitespace-pre-wrap [overflow-wrap:anywhere] shadow-[0_8px_24px_-16px_rgba(0,0,0,.6)]">
            {html ? renderHTML(filled) : filled}
          </div>
          {button && (
            <div className="mt-1.5 rounded-xl bg-fg/[.07] border border-hair text-center text-[13px] py-2 text-ink2">{t("bcAccountButton")}</div>
          )}
        </div>
      )}
    </div>
  );
}

const entity = (s: string) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");
const tagRe = /<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s[^<>]*)?)>/g;

/** Draws Telegram's tags; anything else shows as written. */
function renderHTML(s: string): ReactNode {
  type Node = { tag: string; attrs: string; kids: ReactNode[] };
  const root: Node = { tag: "", attrs: "", kids: [] };
  const stack: Node[] = [root];
  let last = 0, key = 0;
  const top = () => stack[stack.length - 1];
  const close = (n: Node): ReactNode => {
    const k = key++;
    const href = /href\s*=\s*"([^"]*)"/.exec(n.attrs)?.[1] ?? "";
    switch (n.tag) {
      case "b": case "strong": return <strong key={k}>{n.kids}</strong>;
      case "i": case "em": return <em key={k}>{n.kids}</em>;
      case "u": case "ins": return <u key={k}>{n.kids}</u>;
      case "s": case "strike": case "del": return <s key={k}>{n.kids}</s>;
      case "code": return <code key={k} className="mono text-[12.5px] px-1 rounded bg-fg/[.08]">{n.kids}</code>;
      case "pre": return <pre key={k} className="mono text-[12.5px] p-2 rounded-lg bg-fg/[.08] whitespace-pre-wrap">{n.kids}</pre>;
      case "a": return /^(https?:|tg:)/i.test(href) ? <a key={k} href={href} target="_blank" rel="noreferrer" className="text-primary2 underline">{n.kids}</a> : <span key={k}>{n.kids}</span>;
      case "blockquote": return <blockquote key={k} className="border-s-[3px] border-primary2/60 ps-2.5 my-1 text-ink2">{n.kids}</blockquote>;
      case "tg-spoiler": case "span": return <span key={k} className="rounded bg-fg/20 text-transparent hover:text-inherit transition-colors">{n.kids}</span>;
    }
    return <span key={k}>{n.kids}</span>;
  };
  for (const m of s.matchAll(tagRe)) {
    if (m.index! > last) top().kids.push(entity(s.slice(last, m.index)));
    const [, slash, name] = m;
    if (!slash) stack.push({ tag: name.toLowerCase(), attrs: m[3] ?? "", kids: [] });
    else if (stack.length > 1 && top().tag === name.toLowerCase()) { const n = stack.pop()!; top().kids.push(close(n)); }
    else top().kids.push(m[0]);
    last = m.index! + m[0].length;
  }
  if (last < s.length) top().kids.push(entity(s.slice(last)));
  while (stack.length > 1) { const n = stack.pop()!; top().kids.push(close(n)); }
  return root.kids;
}

// ---------------------------------------------------------------- progress and history

function Progress({ run, onStop }: { run: BroadcastRun; onStop: () => void }) {
  const { t, n } = useI18n();
  const doneN = run.sent + run.failed + run.blocked;
  return (
    <Panel icon={<Send className="size-4" />} title={t("bcSending")} sub={t("bcSendingSub").replace("{done}", n(doneN)).replace("{total}", n(run.total))}
      action={<Button size="sm" icon={<Square className="size-3.5" />} onClick={onStop}>{t("bcStop")}</Button>}>
      <Meter value={doneN} max={run.total} color="var(--primary2)" height={8} />
      <Stats run={run} className="mt-3" />
    </Panel>
  );
}

function Stats({ run, className }: { run: BroadcastRun; className?: string }) {
  const { t, n } = useI18n();
  return (
    <div className={cx("flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]", className)}>
      <span className="inline-flex items-center gap-1.5 text-online"><CircleCheck className="size-3.5" />{t("bcDelivered")} <b className="num">{n(run.sent)}</b></span>
      {run.failed > 0 && <span className="inline-flex items-center gap-1.5 text-bad"><CircleAlert className="size-3.5" />{t("bcFailed")} <b className="num">{n(run.failed)}</b></span>}
      {run.blocked > 0 && <span className="inline-flex items-center gap-1.5 text-ink2" title={t("bcBlockedHint")}><CircleSlash className="size-3.5" />{t("bcBlocked")} <b className="num">{n(run.blocked)}</b></span>}
    </div>
  );
}

function History({ list }: { list: BroadcastRun[] }) {
  const { t, lang, n } = useI18n();
  const done = list.filter((x) => x.done);
  return (
    <Panel title={t("bcHistory")} sub={done.length ? n(done.length) : undefined}>
      {done.length === 0 ? (
        <p className="text-[13px] text-ink3 py-6 text-center">{t("bcHistoryEmpty")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-hair">
          {done.map((x) => {
            const aud = audiences.find((a) => a.id === x.audience);
            return (
              <li key={x.id} className="py-3.5 first:pt-1 last:pb-1">
                <div className="flex flex-wrap items-center gap-2 text-[12px] text-ink3">
                  <span>{ago(x.at, lang)}</span>
                  {x.by && <><span>·</span><bdi dir="ltr">{x.by}</bdi></>}
                  <Badge className="h-5 text-[10.5px] px-2" color={aud?.color ?? "var(--color-cyan)"}>{aud ? t(aud.key) : t("bcSelected")}</Badge>
                  {x.html && <Badge className="h-5 text-[10.5px] px-2" color="var(--color-ink2)">HTML</Badge>}
                  {x.canceled && <Badge className="h-5 text-[10.5px] px-2" color="var(--warn)">{t("bcStoppedBadge")}</Badge>}
                  {x.interrupted && <Badge className="h-5 text-[10.5px] px-2" color="var(--warn)">{t("bcInterrupted")}</Badge>}
                  <span className="ms-auto num text-ink2">{n(x.sent)} / {n(x.total)}</span>
                </div>
                <p dir="auto" className="mt-1.5 text-[13.5px] text-ink line-clamp-2 whitespace-pre-wrap [overflow-wrap:anywhere]">{x.html ? entity(x.text.replace(tagRe, "")) : x.text}</p>
                <Stats run={x} className="mt-1.5" />
                {x.errors && x.errors.length > 0 && (
                  <details className="mt-1.5 text-[12px] text-ink3">
                    <summary className="cursor-pointer">{t("bcErrors")}</summary>
                    <ul dir="ltr" className="mt-1 mono text-[11.5px] list-disc ps-5">{x.errors.map((e, i) => <li key={i}>{e}</li>)}</ul>
                  </details>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
