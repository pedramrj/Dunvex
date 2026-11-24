import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import {
  CalendarClock, CalendarPlus, Copy, Download, ExternalLink, FileText, Gauge, KeyRound, Pencil, Play, Power, QrCode, RotateCcw, Search, Smartphone, Trash2, UserPlus,
  Users as UsersIcon, Megaphone, Tags, Hourglass, Unplug, Infinity as InfinityIcon, FilterX, UserCog, ListFilter,
} from "lucide-react";
import { api, money, qrURL, saveText, type ConfigEntry, type Event, type Inbound, type DeviceInfo, type Plan, type Status, type TagCount, type User, type UserMap, type WalletInfo } from "../lib/api";
import { TagChip, TagInput, TagList, hasTag, normalizeTag, sameTag } from "../components/Tags";
import { GB, ago, bytes, copyText, cx, date, regionName, speed } from "../lib/format";
import { operatorName } from "../lib/operators";
import { useI18n } from "../lib/i18n";
import {  Avatar, Badge, Button, Chip, ChipSelect, ConfirmButton, CopyField, Dialog, Dot, Empty, IconButton, Input, KV, Label, Meter, Panel, Ring, ringColor, Segmented, Select, Sheet,
  Skeleton, StatusBadge, Tabs, Toggle, useToast, ContextMenu, useConfirm, type MenuItem, EventMsg } from "../components/ui";
import { UsageChart } from "../components/charts";
import { ShareSheet } from "../components/ShareSheet";
import { PageHead, can, eventMeta, navigate, useSession } from "../App";

type Filter = "" | "online" | Status;
type SortKey = "created" | "name" | "status" | "usage" | "expiry" | "devices" | "speed";
type Quick = "ending" | "never" | "unlimited";

// quick filters: the questions an owner asks most
const quickTest: Record<Quick, (u: User) => boolean> = {
  // still running, but 7 days or less, or 80% of the data, left
  ending: (u) => (u.status === "active" || u.status === "paused") && ((u.daysLeft !== null && u.daysLeft <= 7) || (u.dataLimit > 0 && u.used >= u.dataLimit * 0.8)),
  never: (u) => !u.online && u.lastOnlineAt === 0,
  unlimited: (u) => u.dataLimit === 0,
};
const quickDefs: { id: Quick; key: "qfEnding" | "qfNever" | "qfUnlimited"; hint: "qfEndingHint" | "qfNeverHint" | "qfUnlimitedHint"; icon: typeof Hourglass }[] = [
  { id: "ending", key: "qfEnding", hint: "qfEndingHint", icon: Hourglass },
  { id: "never", key: "qfNever", hint: "qfNeverHint", icon: Unplug },
  { id: "unlimited", key: "qfUnlimited", hint: "qfUnlimitedHint", icon: InfinityIcon },
];
const TAGS_SHOWN = 8;

export function UsersPage({ arg }: { arg?: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const { me, reload } = useSession();
  const [users, setUsers] = useState<User[] | null>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("");
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [qr, setQr] = useState<User | null>(null);
  const [quick, setQuick] = useState<Set<Quick>>(new Set());
  const [tagSel, setTagSel] = useState<string[]>([]);
  const [ownerSel, setOwnerSel] = useState("");
  const [allTags, setAllTags] = useState<TagCount[]>([]);
  const [tagsOpen, setTagsOpen] = useState(false);
  const [tagDlg, setTagDlg] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false); // phones: the bar folds away
  const [shared, setShared] = useState<User[] | null>(null); // the users just made, with their links
  const isAdmin = can(me.admin.role, "admin");

  const load = useCallback(() => {
    api<User[]>("users").then(setUsers).catch((e) => toast(e.message, "error"));
    api<TagCount[]>("users/tags").then(setAllTags).catch(() => {});
  }, [toast]);
  useEffect(() => {
    load();
    const id = setInterval(load, 8000);
    return () => clearInterval(id);
  }, [load]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { "": users?.length ?? 0, online: 0 };
    for (const u of users ?? []) {
      c[u.status] = (c[u.status] ?? 0) + 1;
      if (u.online) c.online++;
    }
    return c;
  }, [users]);
  const quickCounts = useMemo(() => {
    const c: Record<Quick, number> = { ending: 0, never: 0, unlimited: 0 };
    for (const u of users ?? []) for (const d of quickDefs) if (quickTest[d.id](u)) c[d.id]++;
    return c;
  }, [users]);
  // the resellers whose users this admin sees
  const owners = useMemo(() => {
    const m = new Map<number, string>();
    for (const u of users ?? []) if (u.ownerId) m.set(u.ownerId, u.owner || `#${u.ownerId}`);
    return [...m].sort((a, b) => a[1].localeCompare(b[1]));
  }, [users]);
  const toggleTag = (tag: string) => setTagSel((s) => (hasTag(s, tag) ? s.filter((x) => !sameTag(x, tag)) : [...s, tag]));
  const activeN = quick.size + tagSel.length + (ownerSel !== "" ? 1 : 0);
  const filtersOn = activeN > 0;
  const clearFilters = () => { setQuick(new Set()); setTagSel([]); setOwnerSel(""); };

  const [sort, setSort] = useState<{ k: SortKey; dir: 1 | -1 }>({ k: "created", dir: -1 });
  const list = useMemo(() => {
    const s = q.trim().toLowerCase();
    const val = (u: User): number | string => {
      switch (sort.k) {
        case "name": return u.name.toLowerCase();
        case "usage": return u.used;
        case "expiry": return u.daysLeft ?? 1e9;
        case "devices": return u.devices;
        case "speed": return u.speedDown + u.speedUp;
        case "status": return u.status;
        default: return u.id;
      }
    };
    return (users ?? [])
      .filter((u) => (!s || (u.name + " " + u.note + " " + (u.owner ?? "") + " " + (u.tags ?? []).join(" ")).toLowerCase().includes(s))
        && (filter === "" || (filter === "online" ? u.online : u.status === filter))
        && [...quick].every((k) => quickTest[k](u))
        && tagSel.every((tag) => hasTag(u.tags, tag))
        && (ownerSel === "" || String(u.ownerId) === ownerSel))
      .sort((a, b) => { const x = val(a), y = val(b); return (x < y ? -1 : x > y ? 1 : 0) * sort.dir; });
  }, [users, q, filter, sort, quick, tagSel, ownerSel]);
  // the tags to offer: the ones in use, plus any picked that is no longer on anyone
  const tagChoices = useMemo(() => [...allTags, ...tagSel.filter((x) => !allTags.some((tc) => sameTag(tc.tag, x))).map((tag) => ({ tag, count: 0 }))], [allTags, tagSel]);
  const shownTags = tagsOpen ? tagChoices : tagChoices.filter((tc, i) => i < TAGS_SHOWN || hasTag(tagSel, tc.tag));
  const selTags = useMemo(() => {
    const out: string[] = [];
    for (const u of users ?? []) if (sel.has(u.id)) for (const tag of u.tags ?? []) if (!hasTag(out, tag)) out.push(tag);
    return out;
  }, [users, sel]);
  const Sh = ({ k, children }: { k: SortKey; children: React.ReactNode }) => (
    <button onClick={() => setSort((o) => ({ k, dir: o.k === k ? (o.dir === 1 ? -1 : 1) : -1 }))} className={cx("inline-flex items-center gap-1 text-start hover:text-ink transition-colors", sort.k === k && "text-ink")}>
      {children}{sort.k === k && <span aria-hidden>{sort.dir === 1 ? "↑" : "↓"}</span>}
    </button>
  );

  const bulk = (action: string, extra: Record<string, unknown> = {}) =>
    api<{ affected: number }>("users/bulk", { body: { ids: [...sel], action, ...extra } })
      .then((r) => {
        toast(`${n(r.affected)} ✓`);
        setSel(new Set());
        load();
        reload();
      })
      .catch((e) => toast(e.message, "error"));

  const editing = arg && arg !== "new" ? users?.find((u) => String(u.id) === arg) : undefined;
  const confirm = useConfirm();
  const one = (_u: User, path: string, method = "POST", body: unknown = {}) =>
    api(path, { method, body }).then(() => { toast(t("saved")); load(); }).catch((e) => toast(e.message, "error"));
  const rowMenu = (u: User): MenuItem[] => [
    { label: t("open"), icon: <Pencil />, onClick: () => navigate("users", String(u.id)) },
    { label: t("copyLink"), icon: <Copy />, onClick: () => copyText(u.subUrl).then(() => toast(t("copied"))) },
    { label: "QR", icon: <QrCode />, onClick: () => setQr(u) },
    { divider: true, label: "" },
    { label: `${t("extend")} ${n(30)} ${t("days")}`, icon: <CalendarPlus />, onClick: () => api("users/bulk", { body: { ids: [u.id], action: "extend", days: 30 } }).then(() => { toast(t("saved")); load(); }) },
    { label: t("resetUsage"), icon: <RotateCcw />, onClick: () => one(u, `users/${u.id}/reset`) },
    { label: u.enabled ? t("disable") : t("enable"), icon: <Power />, onClick: () => one(u, `users/${u.id}`, "PUT", { enabled: !u.enabled }) },
    { divider: true, label: "" },
    { label: t("delete"), icon: <Trash2 />, danger: true, onClick: async () => { if (await confirm({ title: `${t("delete")} ${u.name}`, body: t("confirmDelete"), danger: true })) api(`users/${u.id}`, { method: "DELETE" }).then(() => { toast(t("deleted")); load(); }); } },
  ];
  const close = () => navigate("users");
  const totalUsed = (users ?? []).reduce((s, u) => s + u.used, 0);
  const speedNow = (users ?? []).reduce((s, u) => s + u.speedDown + u.speedUp, 0);

  const filters: { id: Filter; label: string }[] = [
    { id: "", label: t("all") },
    { id: "online", label: t("online") },
    { id: "active", label: t("active") },
    { id: "pending", label: t("pending") },
    { id: "paused", label: t("paused") },
    { id: "limited", label: t("limited") },
    { id: "expired", label: t("expired") },
    { id: "disabled", label: t("disabled") },
  ];

  return (
    <div>
      <PageHead
        title={t("users")}
        sub={
          <span className="inline-flex flex-wrap gap-x-4 gap-y-1">
            <span className="num">{n(counts[""])} {t("users2")}</span>
            <span className="inline-flex items-center gap-1.5 text-online num"><Dot color="var(--color-online)" pulse size={7} />{n(counts.online)} {t("online")}</span>
            <span className="num text-ink3">{t("usage")}: {bytes(totalUsed, lang)}</span>
            {speedNow > 0 && <span className="num text-ink3">{speed(speedNow, lang)}</span>}
          </span>
        }
        action={<Button variant="primary" icon={<UserPlus className="size-4" />} onClick={() => navigate("users", "new")}>{t("newUser")}</Button>}
      />

      {me.quota && <QuotaStrip />}

      <div className="flex flex-col xl:flex-row gap-3 xl:items-center mb-4">
        <div className="relative xl:w-80">
          <Search className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("searchUsers")} className="ps-10" aria-label={t("search")} />
        </div>
        <Tabs value={filter} onChange={setFilter} items={filters.filter((f) => f.id === "" || counts[f.id]).map((f) => ({ id: f.id, label: f.label, count: counts[f.id] ?? 0 }))} />
      </div>

      {users && users.length > 0 && (() => {
        const summary = (filtersOn || q.trim() || filter) ? (
          <span className="inline-flex items-center gap-1 text-[12.5px] text-ink3">
            <span className="num px-1" aria-live="polite">{t("shownOf").replace("{n}", n(list.length)).replace("{m}", n(users.length))}</span>
            {filtersOn && <Button size="sm" variant="quiet" icon={<FilterX className="size-4" />} onClick={clearFilters}>{t("clearFilters")}</Button>}
          </span>
        ) : null;
        return <>
        <div className="md:hidden mb-3 flex flex-wrap items-center justify-between gap-2">
          <Button size="sm" icon={<ListFilter className="size-4" />} onClick={() => setFiltersOpen((v) => !v)} aria-expanded={filtersOpen}>
            {t("filters")}
            {activeN > 0 && <Badge color="var(--primary2)" className="h-5 px-2 num">{n(activeN)}</Badge>}
          </Button>
          {summary}
        </div>
        <div className={cx("mb-4 flex-wrap items-center gap-x-2 gap-y-2", filtersOpen ? "flex" : "hidden md:flex")} role="group" aria-label={t("filters")}>
          {quickDefs.map((d) => (
            <Chip key={d.id} active={quick.has(d.id)} icon={<d.icon />} count={quickCounts[d.id]} title={t(d.hint)} disabled={!quick.has(d.id) && quickCounts[d.id] === 0}
              onClick={() => setQuick((s) => { const x = new Set(s); x.has(d.id) ? x.delete(d.id) : x.add(d.id); return x; })}>{t(d.key)}</Chip>
          ))}
          {tagChoices.length > 0 && (
            <>
              <span className="mx-1 h-5 w-px bg-hair" aria-hidden />
              <span className="inline-flex items-center gap-1 text-[12.5px] text-ink3"><Tags className="size-3.5" />{t("tags")}</span>
              {shownTags.map((tc) => (
                <TagChip key={tc.tag} tag={tc.tag} count={tc.count} active={hasTag(tagSel, tc.tag)} onClick={() => toggleTag(tc.tag)} title={t("tagFilterBy")} />
              ))}
              {tagChoices.length > TAGS_SHOWN && (
                <Button size="sm" variant="quiet" onClick={() => setTagsOpen((v) => !v)} aria-expanded={tagsOpen}>
                  {tagsOpen ? t("tagsLess") : t("tagsMore").replace("{n}", n(tagChoices.length - shownTags.length))}
                </Button>
              )}
            </>
          )}
          {isAdmin && owners.length > 0 && (
            <>
              <span className="mx-1 h-5 w-px bg-hair" aria-hidden />
              <ChipSelect value={ownerSel} onChange={setOwnerSel} label={t("reseller")} icon={<UserCog />} active={ownerSel !== ""}
                options={[{ value: "", label: t("allOwners") }, { value: "0", label: t("ownUsers") }, ...owners.map(([id, name]) => ({ value: String(id), label: name }))]} />
            </>
          )}
          {summary && <span className="hidden md:inline-flex ms-auto">{summary}</span>}
        </div>
        </>;
      })()}

      {sel.size > 0 && (
        <div className="rise sticky top-16 md:top-4 z-20 mb-3 flex flex-wrap items-center gap-2 rounded-2xl glass-strong px-3 py-2.5 shadow-[0_18px_40px_-16px_rgba(0,0,0,.8)]">
          <Badge color="var(--color-cdn)" className="h-7">{n(sel.size)} {t("selected")}</Badge>
          <Button size="sm" icon={<Megaphone className="size-4" />} onClick={() => navigate("broadcast", "u" + [...sel].join("-"))}>{t("bcMessageUsers")}</Button>
          <Button size="sm" icon={<Tags className="size-4" />} onClick={() => setTagDlg(true)}>{t("tagBulk")}</Button>
          <Button size="sm" icon={<CalendarPlus className="size-4" />} onClick={() => bulk("extend", { days: 30 })}>{t("extend")} {n(30)} {t("days")}</Button>
          <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => bulk("reset")}>{t("resetUsage")}</Button>
          <Button size="sm" icon={<Play className="size-4" />} onClick={() => bulk("enable")}>{t("enable")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => bulk("disable")}>{t("disable")}</Button>
          <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => bulk("delete")}>{t("delete")}</ConfirmButton>
          <button onClick={() => setSel(new Set())} className="ms-auto text-ink3 hover:text-ink text-[13px] px-2 h-8">{t("cancel")}</button>
        </div>
      )}

      {!users ? (
        <div className="flex flex-col gap-2">{[0, 1, 2, 3, 4].map((i) => <Skeleton key={i} className="h-16" />)}</div>
      ) : users.length === 0 ? (
        <div className="glass rounded-[20px]">
          <Empty icon={<UsersIcon className="size-7" />} title={t("emptyUsers")} hint={t("emptyUsersHint")} action={<Button variant="primary" icon={<UserPlus className="size-4" />} onClick={() => navigate("users", "new")}>{t("newUser")}</Button>} />
        </div>
      ) : list.length === 0 ? (
        <div className="glass rounded-[20px]"><Empty icon={<Search className="size-7" />} title={t("noMatch")}
          action={filtersOn ? <Button icon={<FilterX className="size-4" />} onClick={clearFilters}>{t("clearFilters")}</Button> : undefined} /></div>
      ) : (
        <div className="glass edge rounded-[20px] overflow-hidden">
          <div className="hidden lg:grid grid-cols-[32px_minmax(200px,1.6fr)_124px_minmax(180px,1.4fr)_84px_176px] items-center gap-3 px-5 h-12 text-[12px] text-ink3 font-medium border-b border-hair bg-fg/[.015]">
            <input type="checkbox" aria-label={t("all")} className="accent-[var(--primary)] size-4 cursor-pointer" checked={sel.size > 0 && sel.size === list.length}
              onChange={(e) => setSel(e.target.checked ? new Set(list.map((u) => u.id)) : new Set())} />
            <Sh k="name">{t("name")}</Sh>
            <Sh k="expiry">{t("ends")}</Sh>
            <Sh k="usage">{t("usage")}</Sh>
            <Sh k="devices">{t("devices")}</Sh>
            <span />
          </div>
          <div role="list">
            {list.map((u) => (
              <ContextMenu key={u.id} items={rowMenu(u)}>
                <UserRow u={u} checked={sel.has(u.id)} activeTags={tagSel} onTag={toggleTag}
                  onCheck={(v) => setSel((s) => { const x = new Set(s); v ? x.add(u.id) : x.delete(u.id); return x; })}
                  onQr={() => setQr(u)} onCopy={() => copyText(u.subUrl).then(() => toast(t("copied")))} />
              </ContextMenu>
            ))}
          </div>
        </div>
      )}

      <UserForm open={arg === "new"} onClose={close} tagSuggest={allTags.map((x) => x.tag)} onSaved={(u, all) => { load(); reload(); navigate("users"); setShared(all ?? [u]); }} />
      <ShareSheet users={shared} onClose={() => setShared(null)} onOpen={(id) => { setShared(null); navigate("users", String(id)); }} onNext={() => { setShared(null); navigate("users", "new"); }} />
      {editing && <UserDetail key={editing.id} u={editing} tagSuggest={allTags.map((x) => x.tag)} onClose={close} onChanged={() => { load(); reload(); }} />}
      <BulkTagDialog open={tagDlg} onClose={() => setTagDlg(false)} count={sel.size} all={allTags.map((x) => x.tag)} present={selTags}
        onApply={(action, tag) => { setTagDlg(false); bulk(action, { tag }); }} />
      <Sheet open={!!qr} onClose={() => setQr(null)} title={<span dir="ltr">{qr?.name}</span>} sub={t("subLink")}>
        {qr && (
          <div className="flex flex-col items-center gap-5">
            <div className="rounded-3xl p-4 bg-white shadow-[0_20px_60px_-24px_color-mix(in_oklab,var(--primary)_60%,transparent)]"><img src={qrURL(qr.subUrl)} alt="QR" className="w-64 h-64 sensitive" /></div>
            <CopyField value={qr.subUrl} className="w-full" />
            <a className="inline-flex items-center gap-1.5 text-[13px] text-primary2 hover:text-ink" href={qr.subUrl} target="_blank" rel="noreferrer">{t("openUserPage")} <ExternalLink className="size-3.5" /></a>
          </div>
        )}
      </Sheet>
    </div>
  );
}

/** puts one tag on the selected users, or takes it off */
function BulkTagDialog({ open, onClose, count, all, present, onApply }: {
  open: boolean; onClose: () => void; count: number; all: string[]; present: string[]; onApply: (action: "tag" | "untag", tag: string) => void;
}) {
  const { t, n } = useI18n();
  const [mode, setMode] = useState<"tag" | "untag">("tag");
  const [tag, setTag] = useState("");
  useEffect(() => { if (open) { setTag(""); setMode("tag"); } }, [open]);
  const clean = normalizeTag(tag);
  const pool = mode === "tag" ? all : present;
  const sugg = pool.filter((s) => !clean || s.toLowerCase().includes(clean.toLowerCase())).slice(0, 12);
  const go = () => clean && onApply(mode, clean);
  return (
    <Dialog open={open} onClose={onClose} title={t("tagBulkTitle").replace("{n}", n(count))}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" icon={<Tags className="size-4" />} disabled={!clean} onClick={go}>
          {(mode === "tag" ? t("tagAddTo") : t("tagRemoveFrom")).replace("{n}", n(count))}
        </Button></>}>
      <Segmented value={mode} onChange={setMode} options={[{ value: "tag", label: t("tagAdd") }, { value: "untag", label: t("tagTakeOff") }]} />
      <label className="block mt-4">
        <Label>{t("tag")}</Label>
        <Input value={tag} onChange={(e) => setTag(e.target.value)} maxLength={26} dir="auto" autoFocus placeholder={t("tagPlaceholder")}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); go(); } }} />
      </label>
      {sugg.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {sugg.map((s) => <TagChip key={s} tag={s} active={sameTag(s, clean)} onClick={() => setTag(s)} />)}
        </div>
      )}
      {mode === "untag" && present.length === 0 && <p className="mt-3 text-[12.5px] text-ink3">{t("tagNoneOnSelected")}</p>}
    </Dialog>
  );
}

function QuotaStrip() {
  const { t, lang, n } = useI18n();
  const { me } = useSession();
  const q = me.quota!;
  return (
    <div className="glass rounded-2xl px-4 py-3 mb-4 grid sm:grid-cols-2 gap-4">
      <div>
        <div className="flex justify-between text-[12.5px] mb-1.5"><span className="text-ink2">{t("users")}</span><span className="num">{n(q.users)} / {q.maxUsers ? n(q.maxUsers) : "∞"}</span></div>
        <Meter value={q.users} max={q.maxUsers} />
      </div>
      <div>
        <div className="flex justify-between text-[12.5px] mb-1.5"><span className="text-ink2">{t("allocated")}</span><span className="num">{bytes(q.allocated, lang)} / {q.maxData ? bytes(q.maxData, lang) : "∞"}</span></div>
        <Meter value={q.allocated} max={q.maxData} />
      </div>
    </div>
  );
}

function ExpiryCell({ u }: { u: User }) {
  const { t, lang, n } = useI18n();
  if (u.status === "pending" && u.daysLeft !== null) return <Badge color="var(--color-cdn)" icon={<CalendarClock className="size-3.5" />}>{n(u.daysLeft)} {t("days")}</Badge>;
  if (u.daysLeft === null) return <span className="text-ink3 text-[13px]">{t("never")}</span>;
  if (u.status === "expired") return <div><span className="text-bad text-[13px] font-medium">{t("expired")}</span><div className="text-[11.5px] text-ink3 num">{date(u.expireAt, lang)}</div></div>;
  const warn = u.daysLeft <= 3;
  return (
    <div>
      <span className={cx("num text-[13px] font-medium", warn && "text-warn", u.daysLeft === 0 && "text-bad")}>{n(u.daysLeft)} {u.daysLeft === 1 && lang === "en" ? "day left" : t("daysLeft")}</span>
      <div className="text-[11.5px] text-ink3 num">{date(u.expireAt, lang)}</div>
    </div>
  );
}

function UserRow({ u, checked, onCheck, onQr, onCopy, onTag, activeTags }: {
  u: User; checked: boolean; onCheck: (v: boolean) => void; onQr: () => void; onCopy: () => void; onTag?: (tag: string) => void; activeTags?: string[];
}) {
  const { t, lang, n, pc } = useI18n();
  const pct = u.dataLimit > 0 ? (u.used / u.dataLimit) * 100 : 0;
  return (
    <div role="listitem" style={{ contentVisibility: "auto", containIntrinsicSize: "0 var(--row)" }} className={cx("border-b border-hair transition-colors", checked ? "bg-primary/[.08]" : "hover:bg-fg/[.025]")}>
      <div className="grid grid-cols-[28px_1fr_auto] lg:grid-cols-[32px_minmax(200px,1.6fr)_124px_minmax(180px,1.4fr)_84px_176px] items-center gap-x-3 gap-y-2.5 px-4 lg:px-5 py-3.5">
        <input type="checkbox" aria-label={u.name} className="accent-[var(--primary)] size-4 cursor-pointer" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        <div className="min-w-0">
          <a href={`#/users/${u.id}`} className="min-w-0 flex items-center gap-3 group">
            <Avatar name={u.name} size={38} online={u.online} />
            <span className="min-w-0">
              <span className="flex items-center gap-2 min-w-0"><span className="font-semibold truncate group-hover:text-primary2" dir="ltr">{u.name}</span>{u.status !== "active" && <StatusBadge status={u.status} />}</span>
              <span className="block text-[12px] text-ink3 truncate">
                {u.note || (u.online ? <span className="text-online">{t("online")}</span> : ago(u.lastOnlineAt, lang))}
                {u.owner && <span className="text-violet"> · {u.owner}</span>}
              </span>
              {u.loc && (u.loc.city || u.loc.cc) && (
                <span className="flex items-center gap-1.5 text-[12px] text-ink2 truncate">
                  <span className="led on" style={{ width: 5, height: 5 }} />
                  <bdi className="truncate">{u.loc.city || regionName(u.loc.cc!, lang)}</bdi>
                  {u.loc.org && <bdi className="text-ink3 truncate">{operatorName(undefined, u.loc.org, lang)}</bdi>}
                  {/* no "·" before a count: beside a Persian digit it reads as a zero */}
                  {u.loc.places > 1 && <bdi className="text-warn shrink-0">{t("connPlaces").replace("{n}", n(u.loc.places))}</bdi>}
                </span>
              )}
            </span>
          </a>
          {(u.tags?.length ?? 0) > 0 && <div className="mt-1.5 ps-[50px]"><TagList tags={u.tags} onPick={onTag} active={activeTags} /></div>}
        </div>
        <div className="flex items-center gap-0.5 lg:hidden">
          <IconButton label={t("copy")} onClick={onCopy}><Copy className="size-4" /></IconButton>
          <IconButton label="QR" onClick={onQr}><QrCode className="size-4" /></IconButton>
        </div>
        <div className="col-start-2 lg:col-start-auto"><ExpiryCell u={u} /></div>
        <div className="col-span-3 lg:col-span-1 col-start-1 lg:col-start-auto min-w-0 ps-10 lg:ps-0">
          <div className="flex justify-between items-baseline gap-2 text-[12.5px] num mb-1.5">
            <span className="font-medium">{bytes(u.used, lang)}</span>
            {u.dataLimit > 0 ? (
              <span className="text-ink3 inline-flex gap-1.5"><bdi>{bytes(u.dataLimit, lang)}</bdi><bdi className={cx(pct >= 90 && "text-bad", pct >= 75 && pct < 90 && "text-warn")}>{pc(pct, 0)}</bdi></span>
            ) : (
              <span className="text-ink3">{t("unlimited")}</span>
            )}
          </div>
          <Meter value={u.used} max={u.dataLimit > 0 ? u.dataLimit : 0} />
          {u.speedDown + u.speedUp > 0 && <div className="mt-1 text-[11.5px] text-online num inline-flex items-center gap-1.5"><span className="led on live" style={{ width: 5, height: 5 }} />{speed(u.speedDown + u.speedUp, lang)}</div>}
        </div>
        <div className="hidden lg:flex items-center gap-1.5 text-[13px] num">
          <Smartphone className={cx("size-4", u.devices > 0 ? "text-primary2" : "text-ink3")} />
          <span className={cx(u.ipLimit > 0 && u.devices > u.ipLimit && "text-bad")}>{n(u.devices)}</span>
          <span className="text-ink3">{t("ofWord")} {u.ipLimit > 0 ? n(u.ipLimit) : "∞"}</span>
        </div>
        <div className="hidden lg:flex items-center justify-end gap-0.5">
          <Button size="sm" icon={<Copy className="size-4" />} onClick={onCopy}>{t("copyLink")}</Button>
          <IconButton label="QR" onClick={onQr}><QrCode className="size-4" /></IconButton>
          <IconButton label={t("edit")} onClick={() => navigate("users", String(u.id))}><Pencil className="size-4" /></IconButton>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- create / edit

interface FormState {
  name: string;
  count: number;
  gb: number;
  days: number;
  startOnFirstUse: boolean;
  resetPeriod: User["resetPeriod"];
  inboundIds: number[];
  note: string;
  ipLimit: number;
  tags: string[];
}

function UserForm({ open, onClose, onSaved, user, tagSuggest = [] }: { open: boolean; onClose: () => void; onSaved: (u: User, all?: User[]) => void; user?: User; tagSuggest?: string[] }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const { me } = useSession();
  const isAdmin = can(me.admin.role, "admin");
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [busy, setBusy] = useState(false);
  const init = (): FormState =>
    user
      ? {
          name: user.name, count: 1, gb: user.dataLimit > 0 ? +(user.dataLimit / GB).toFixed(2) : 0, days: user.daysLeft ?? 0,
          startOnFirstUse: user.status === "pending", resetPeriod: user.resetPeriod, inboundIds: user.inboundIds, note: user.note, ipLimit: user.ipLimit,
          tags: user.tags ?? [],
        }
      : { name: "", count: 1, gb: 30, days: 30, startOnFirstUse: true, resetPeriod: "none", inboundIds: [], note: "", ipLimit: 0, tags: [] };
  const [f, setF] = useState<FormState>(init);
  const [daysTouched, setDaysTouched] = useState(false);
  // plans: a new user can come from one; a reseller whose wallet is on must pick one
  const [plans, setPlans] = useState<Plan[]>([]);
  const [wallet, setWallet] = useState<WalletInfo | null>(null);
  const [planId, setPlanId] = useState(0);
  const mustPlan = !isAdmin && !!wallet?.enabled;
  useEffect(() => {
    if (open) {
      setF(init());
      setDaysTouched(false);
      setPlanId(0);
      if (isAdmin) api<Inbound[]>("inbounds").then(setInbounds).catch(() => {});
      if (!user) {
        api<Plan[]>("billing/plans").then((ps) => {
          const sale = isAdmin ? ps : ps.filter((p) => p.enabled);
          setPlans(sale);
          if (!isAdmin) api<WalletInfo>("wallet").then((w) => { setWallet(w); if (w.enabled && sale.length) setPlanId(sale[0].id); }).catch(() => {});
        }).catch(() => {});
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((x) => ({ ...x, [k]: v }));

  const chosen = plans.find((p) => p.id === planId);
  const submit = () => {
    setBusy(true);
    const body: Record<string, unknown> = { name: f.name, dataLimit: Math.round(f.gb * GB), resetPeriod: f.resetPeriod, note: f.note, ipLimit: f.ipLimit, tags: f.tags };
    if (isAdmin) body.inboundIds = f.inboundIds;
    if (!user || daysTouched) {
      body.durationDays = f.days;
      body.startOnFirstUse = f.startOnFirstUse;
    }
    if (!user) body.count = f.count;
    if (!user && planId) body.planId = planId;
    const req: Promise<[User, User[] | undefined]> = user
      ? api<User>(`users/${user.id}`, { method: "PUT", body }).then((u) => [u, undefined])
      : api<User[]>("users", { body }).then((list) => [list[0], list]);
    req.then(([u, all]) => { toast(t("saved")); onSaved(u, all); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };

  return (
    <Sheet open={open} onClose={onClose} wide title={user ? t("edit") : t("newUser")} sub={user ? <span dir="ltr">{user.name}</span> : undefined}
      footer={<>
        {!user && <span className="me-auto text-[12.5px] text-ink3 truncate"><bdi dir="ltr" className="text-ink">{f.name || "…"}</bdi> · {f.gb > 0 ? (f.gb >= 1024 ? `${n(f.gb / 1024)} ${t("tbUnit")}` : `${n(f.gb)} ${t("gb")}`) : t("unlimited")} · {f.days > 0 ? (f.days === 365 ? t("oneYear") : `${n(f.days)} ${t("days")}`) : t("never")}<span className="hidden sm:inline"> · <kbd className="mono">Ctrl ↵</kbd></span></span>}
        <Button variant="quiet" onClick={onClose}>{t("cancel")}</Button><Button variant="primary" loading={busy} onClick={submit}>{user ? t("save") : t("createShare")}</Button></>}>
      <div className="flex flex-col gap-5" onKeyDown={(e) => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); submit(); } }}>
        <FormSection title={t("general")}>
          <div className="grid grid-cols-[1fr_100px] gap-3">
            <label><Label>{t("name")}</Label><Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="ali-phone" dir="ltr" autoFocus /></label>
            {!user && <label><Label>{t("count")}</Label><Input type="number" min={1} max={500} value={f.count} onChange={(e) => set("count", Math.max(1, +e.target.value || 1))} className="num" /></label>}
          </div>
          <label className="block mt-3"><Label>{t("note")}</Label><Input value={f.note} onChange={(e) => set("note", e.target.value)} /></label>
          <div className="mt-3">
            <Label>{t("tags")}</Label>
            <TagInput value={f.tags} onChange={(v) => set("tags", v)} suggestions={tagSuggest} />
          </div>
        </FormSection>

        {!user && plans.length > 0 && (
          <FormSection title={t("choosePlan")}>
            <Select value={String(planId)} onChange={(v) => setPlanId(+v)}
              options={[...(mustPlan ? [] : [{ value: "0", label: t("planNone") }]), ...plans.map((p) => ({ value: String(p.id), label: p.name }))]} />
            {chosen && <PlanFacts p={chosen} />}
            {chosen && mustPlan && wallet && (
              <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
                <span>{t("price")}: <b className="num">{money((chosen.resellerPrice ?? chosen.price) * f.count)}</b> {wallet.currency}</span>
                <span className="text-ink3">{t("balance")}: <span className="num">{money(wallet.balance)}</span></span>
                <span className="text-ink3 basis-full">{t("planRequired")}</span>
              </div>
            )}
          </FormSection>
        )}
        {mustPlan && plans.length === 0 && <div className="rounded-2xl border border-warn/40 bg-warn/[.08] px-4 py-3 text-[13px]">{t("noPlans")}</div>}

        {!chosen && <FormSection title={t("volumeTime")}>
          <Label hint={f.gb > 0 ? `${n(f.gb)} ${t("gb")}` : t("unlimited")}>{t("dataLimit")}</Label>
          <Segmented value={[10, 30, 50, 100, 200, 500, 1024, 0].includes(f.gb) ? f.gb : -1} onChange={(v) => v >= 0 && set("gb", v)}
            options={[10, 30, 50, 100, 200, 500].map((g) => ({ value: g, label: `${n(g)} ${t("gb")}` })).concat([{ value: 1024, label: `${n(1)} ${t("tbUnit")}` }, { value: 0, label: t("unlimited") }])} />
          <Input type="number" min={0} step="0.5" value={f.gb} onChange={(e) => set("gb", Math.max(0, +e.target.value || 0))} className="num mt-2 max-w-40" aria-label={t("gb")} />

          <div className="mt-5">
            <Label hint={f.days > 0 ? `${n(f.days)} ${t("days")}` : t("never")}>{t("duration")}</Label>
            <Segmented value={[7, 30, 60, 90, 180, 365, 0].includes(f.days) ? f.days : -1} onChange={(v) => { if (v >= 0) { set("days", v); setDaysTouched(true); } }}
              options={[7, 30, 60, 90, 180].map((d) => ({ value: d, label: `${n(d)} ${t("days")}` })).concat([{ value: 365, label: t("oneYear") }, { value: 0, label: t("never") }])} />
            <Input type="number" min={0} value={f.days} onChange={(e) => { set("days", Math.max(0, +e.target.value || 0)); setDaysTouched(true); }} className="num mt-2 max-w-40" aria-label={t("days")} />
            {f.days > 0 && (!user || user.firstUseAt === 0) && (
              <div className="mt-3"><Toggle checked={f.startOnFirstUse} onChange={(v) => { set("startOnFirstUse", v); setDaysTouched(true); }} label={t("startOnFirst")} /></div>
            )}
          </div>
          <label className="block mt-5">
            <Label>{t("resetPeriod")}</Label>
            <Select value={f.resetPeriod} onChange={(v) => set("resetPeriod", v as FormState["resetPeriod"])} options={(["none", "daily", "weekly", "monthly"] as const).map((p) => ({ value: p, label: t(p) }))} />
          </label>
        </FormSection>}

        {!chosen && <FormSection title={t("limits")}>
          <Label hint={f.ipLimit > 0 ? n(f.ipLimit) : t("unlimited")}>{t("deviceLimit")}</Label>
          <Segmented value={[0, 1, 2, 3, 5].includes(f.ipLimit) ? f.ipLimit : -1} onChange={(v) => v >= 0 && set("ipLimit", v)}
            options={[{ value: 0, label: t("unlimited") }, ...[1, 2, 3, 5].map((d) => ({ value: d, label: n(d) }))]} />
          <p className="text-[12.5px] text-ink3 mt-2">{t("deviceLimitHint")}</p>
        </FormSection>}

        {isAdmin && !chosen && inbounds.length > 0 && (
          <FormSection title={t("access")}>
            <Segmented value={f.inboundIds.length ? "some" : "all"} onChange={(v) => set("inboundIds", v === "all" ? [] : inbounds.map((i) => i.id))}
              options={[{ value: "all", label: t("allProtocols") }, { value: "some", label: t("onlyThese") }]} />
            {f.inboundIds.length > 0 && (
              <div className="mt-3 flex flex-col gap-1 rounded-2xl border border-hair p-2">
                {inbounds.map((i) => (
                  <label key={i.id} className="flex items-center gap-3 h-10 px-2.5 rounded-xl hover:bg-fg/[.04] cursor-pointer">
                    <input type="checkbox" className="accent-[var(--primary)] size-4" checked={f.inboundIds.includes(i.id)}
                      onChange={(e) => set("inboundIds", e.target.checked ? [...f.inboundIds, i.id] : f.inboundIds.filter((x) => x !== i.id))} />
                    <span className="text-[13.5px]" dir="ltr">{i.remark || i.name}</span>
                    <bdi dir="ltr" className="ms-auto text-ink3 text-[12px] mono">:{i.port}</bdi>
                  </label>
                ))}
              </div>
            )}
          </FormSection>
        )}
      </div>
    </Sheet>
  );
}

/** gives a user a plan: days add to the time left, data left is kept;
 * a reseller with a wallet pays for it */
function RenewDialog({ open, user, plans, onClose, onDone }: { open: boolean; user: User; plans: Plan[]; onClose: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const { me } = useSession();
  const reseller = !can(me.admin.role, "admin");
  const [planId, setPlanId] = useState(0);
  const [wallet, setWallet] = useState<WalletInfo | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!open) return;
    setPlanId((id) => (plans.some((p) => p.id === id) ? id : plans[0]?.id ?? 0));
    if (reseller) api<WalletInfo>("wallet").then(setWallet).catch(() => {});
  }, [open, plans, reseller]);
  const p = plans.find((x) => x.id === planId);
  const pays = reseller && !!wallet?.enabled;
  const go = () => {
    setBusy(true);
    api<User>(`users/${user.id}/renew`, { method: "POST", body: { planId } })
      .then(() => { toast(t("renewed")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  return (
    <Dialog open={open} onClose={onClose} title={<>{t("renewWithPlan")} · <bdi dir="ltr">{user.name}</bdi></>}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button><Button variant="primary" loading={busy} disabled={!p} onClick={go}>{t("renewWithPlan")}</Button></>}>
      <label className="block"><Label>{t("choosePlan")}</Label>
        <Select value={String(planId)} onChange={(v) => setPlanId(+v)} options={plans.map((x) => ({ value: String(x.id), label: x.name }))} /></label>
      {p && <PlanFacts p={p} />}
      <p className="text-[12.5px] text-ink3 mt-3">{t("renewHint")}</p>
      {p && pays && wallet && (
        <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
          <span>{t("price")}: <b className="num">{money(p.resellerPrice ?? p.price)}</b> {wallet.currency}</span>
          <span className="text-ink3">{t("balance")}: <span className="num">{money(wallet.balance)}</span></span>
        </div>
      )}
    </Dialog>
  );
}

/** what a plan gives, in one line of chips */
function PlanFacts({ p }: { p: Plan }) {
  const { t, n } = useI18n();
  const chip = "inline-flex items-center rounded-full bg-fg/[.05] border border-hair px-2.5 h-7 text-[12.5px]";
  return (
    <div className="mt-3 flex flex-wrap gap-1.5">
      <span className={chip}>{p.dataLimit > 0 ? `${n(+(p.dataLimit / GB).toFixed(2))} ${t("gb")}` : t("unlimited")}</span>
      <span className={chip}>{p.days > 0 ? `${n(p.days)} ${t("days")}` : t("never")}</span>
      <span className={chip}>{t("deviceLimit")}: {p.ipLimit > 0 ? n(p.ipLimit) : t("unlimited")}</span>
      {p.days > 0 && p.startOnFirstUse && <span className={chip}>{t("startOnFirst")}</span>}
    </div>
  );
}

function FormSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl bg-fg/[.025] border border-hair p-4">
      <h3 className="text-[12px] font-semibold text-ink3 mb-3">{title}</h3>
      {children}
    </section>
  );
}

// ---------------------------------------------------------------- detail

interface Detail extends User {
  deviceList: DeviceInfo[];
}

// the small map of one user's places loads with the map's world data, only when shown
const EarthLazy = lazy(() => import("../components/Earth").then((m) => ({ default: m.Earth })));

/** Daily average of the last 7 days and, with a data limit, when it runs out at that pace. */
function pace(series: { t: number; up: number; down: number }[], u: User): { avg: number; days: number | null; soon: boolean } {
  const now = Date.now() / 1000, from = now - 7 * 86400;
  let sum = 0;
  for (const p of series) {
    const at = p.t < 1e8 ? p.t * 3600 : p.t; // hours or seconds
    if (at >= from) sum += p.up + p.down;
  }
  const avg = sum / 7;
  if (!avg || u.dataLimit <= 0) return { avg, days: null, soon: false };
  const left = u.dataLimit - u.used;
  if (left <= 0) return { avg, days: null, soon: false };
  const days = Math.ceil(left / avg);
  return { avg, days, soon: u.daysLeft != null && days < u.daysLeft };
}

/** Where a user is connected from: a small map, then each address with its city, network and server. */
function Connections({ u, det }: { u: User; det: Detail | null }) {
  const { t, lang, n } = useI18n();
  const [map, setMap] = useState<UserMap | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () => api<UserMap>("network/users").then((r) => alive && setMap(r)).catch(() => {});
    load();
    const id = setInterval(load, 15000);
    return () => { alive = false; clearInterval(id); };
  }, []);
  const list = det?.deviceList ?? [];
  const places = new Set(list.map((d) => (d.place ? d.place.cc + "|" + (d.place.city ?? "") : "?"))).size;
  return (
    <div className="flex flex-col gap-3">
      <div className="mach rounded-2xl overflow-hidden">
        <div className="flex items-center gap-2 px-4 pt-3 pb-2">
          <h4 className="font-semibold text-[15px]">{t("connNow")}</h4>
          <span className="ss">LIVE</span>{list.length > 0 && <span className="led on live" />}
          <span className="ms-auto text-[12.5px] text-ink2">{n(list.length)} {t("mapStatDevices")} · {t("connPlaces").replace("{n}", n(places))}{u.ipLimit > 0 ? ` · ${t("deviceLimit")}: ${n(u.ipLimit)}` : ""}</span>
        </div>
        <Suspense fallback={<Skeleton className="h-[240px] rounded-none" />}>
          <EarthLazy data={map} focusUser={u.id} height={240} compact />
        </Suspense>
      </div>
      {!det ? <Skeleton className="h-12" /> : list.length === 0 ? (
        <div className="text-ink3 text-[13.5px] py-2">{t("connNone")}</div>
      ) : (
        <ul className="mach rounded-2xl divide-y divide-[var(--border)]">
          {list.map((d) => (
            <li key={d.ip} className="grid grid-cols-[10px_minmax(0,1fr)_auto] items-center gap-3 px-4 py-3">
              <span className="led on" />
              <div className="min-w-0">
                <div className="font-semibold truncate"><bdi>{d.place?.city || (d.relay ? t("mapRelay") : t("connUnknown"))}</bdi>{d.place && <span className="text-ink3 font-normal"> · {regionName(d.place.cc, lang)}</span>}</div>
                <div className="text-[12.5px] text-ink3 truncate">
                  {d.net && <span>{operatorName(d.net.asn, d.net.org, lang)} <span className="mono" dir="ltr">AS{d.net.asn}</span></span>}
                  {d.servers.length > 0 && <span>{d.net ? " · " : ""}{d.relay ? t("connRelay").replace("{name}", d.servers[d.servers.length - 1]) : `${t("connServer")}: `}<bdi dir="ltr">{(d.relay ? d.servers.slice(0, -1) : d.servers).join(" · ")}</bdi></span>}
                </div>
              </div>
              <div className="text-end">
                <div className="text-[12px] text-ink2">{ago(d.lastSeen, lang)}</div>
                <bdi dir="ltr" className="mono text-[11.5px] text-ink3 sensitive">{d.ip}</bdi>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function UserDetail({ u, onClose, onChanged, tagSuggest }: { u: User; onClose: () => void; onChanged: () => void; tagSuggest: string[] }) {
  const { t, lang, n, pc } = useI18n();
  const toast = useToast();
  const [cfg, setCfg] = useState<{ subUrl: string; configs: ConfigEntry[] } | null>(null);
  const [series, setSeries] = useState<{ t: number; up: number; down: number }[]>([]);
  const [det, setDet] = useState<Detail | null>(null);
  const [edit, setEdit] = useState(false);
  const [openQr, setOpenQr] = useState<number | null>(null);
  const [tab, setTab] = useState<"overview" | "configs" | "devices" | "usage" | "activity">("overview");
  const [days, setDays] = useState(30);
  const [activity, setActivity] = useState<Event[] | null>(null);
  const confirm = useConfirm();
  const { me } = useSession();
  const [plans, setPlans] = useState<Plan[]>([]);
  const [renew, setRenew] = useState(false);
  useEffect(() => {
    api<Plan[]>("billing/plans").then((ps) => setPlans(can(me.admin.role, "admin") ? ps : ps.filter((p) => p.enabled))).catch(() => {});
  }, [me.admin.role]);
  useEffect(() => {
    api<{ subUrl: string; configs: ConfigEntry[] }>(`users/${u.id}/configs`).then(setCfg).catch(() => {});
  }, [u.id, u.subToken]);
  useEffect(() => {
    api<typeof series>(`users/${u.id}/usage?days=${days}`).then(setSeries).catch(() => {});
  }, [u.id, days]);
  useEffect(() => {
    if (tab === "activity" && can(me.admin.role, "admin")) api<Event[]>(`events?limit=300&q=${encodeURIComponent(u.name)}`).then(setActivity).catch(() => setActivity([]));
  }, [tab, u.name, me.admin.role]);
  useEffect(() => {
    const load = () => api<Detail>(`users/${u.id}`).then(setDet).catch(() => {});
    load();
    const id = setInterval(load, 6000);
    return () => clearInterval(id);
  }, [u.id]);
  const act = (path: string, method = "POST", body?: unknown) =>
    api(path, { method, body: body ?? {} }).then(() => { toast(t("saved")); onChanged(); }).catch((e) => toast(e.message, "error"));
  const cur = det ?? u;
  const pct = cur.dataLimit > 0 ? (cur.used / cur.dataLimit) * 100 : 0;

  if (edit) return <UserForm open user={cur} tagSuggest={tagSuggest} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); onChanged(); }} />;

  return (
    <Sheet open onClose={onClose} wide title={<span className="inline-flex items-center gap-3"><Avatar name={cur.name} size={34} online={cur.online} /><span dir="ltr">{cur.name}</span></span>}
      footer={
        <div className="flex flex-wrap gap-2 w-full">
          <Button size="sm" icon={<Pencil className="size-4" />} onClick={() => setEdit(true)}>{t("edit")}</Button>
          {plans.length > 0 && <Button size="sm" variant="soft" icon={<CalendarPlus className="size-4" />} onClick={() => setRenew(true)}>{t("renewWithPlan")}</Button>}
          <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => act(`users/${cur.id}/reset`)}>{t("resetUsage")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => act(`users/${cur.id}`, "PUT", { enabled: !cur.enabled })}>{cur.enabled ? t("disable") : t("enable")}</Button>
          <Button size="sm" icon={<KeyRound className="size-4" />} onClick={async () => { if (await confirm({ title: t("regenerate"), body: t("regenerateHint") })) act(`users/${cur.id}/regenerate`); }}>{t("regenerate")}</Button>
          <span className="ms-auto" />
          <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => api(`users/${cur.id}`, { method: "DELETE" }).then(() => { onChanged(); onClose(); })}>{t("delete")}</ConfirmButton>
        </div>
      }>
      <RenewDialog open={renew} user={cur} plans={plans} onClose={() => setRenew(false)} onDone={() => { setRenew(false); onChanged(); }} />
      <div className="flex flex-col gap-5">
        {cur.status === "paused" && (
          <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-violet/40 bg-violet/[.08] px-4 py-3 text-[13.5px]">
            <Smartphone className="size-5 text-violet shrink-0" />
            <span className="flex-1 min-w-0">{t("pausedUntil")} <b className="num">{new Date(cur.pausedUntil * 1000).toLocaleTimeString(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit" })}</b></span>
            <Button size="sm" variant="soft" icon={<Play className="size-4" />} onClick={() => act(`users/${cur.id}`, "PUT", { resume: true })}>{t("resume")}</Button>
          </div>
        )}

        <Tabs value={tab} onChange={setTab} items={[
          { id: "overview", label: t("overview") },
          { id: "configs", label: t("configs"), count: cfg?.configs.length },
          { id: "devices", label: t("connTab"), count: det?.deviceList.length },
          { id: "usage", label: t("usage") },
          ...(can(me.admin.role, "admin") ? [{ id: "activity" as const, label: t("activity") }] : []),
        ]} />

        {tab === "overview" && <>
        {(cur.tags?.length ?? 0) > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 -mb-1">
            <Tags className="size-4 text-ink3 me-0.5" aria-label={t("tags")} />
            {cur.tags.map((tag) => <TagChip key={tag} tag={tag} />)}
          </div>
        )}
        <div className="grid sm:grid-cols-[auto_1fr] gap-5 items-center rounded-2xl bg-fg/[.025] border border-hair p-4">
          <Ring value={cur.dataLimit > 0 ? pct : 100} size={128} stroke={11} color={cur.dataLimit > 0 ? ringColor(pct) : "url(#ring-ok)"}>
            <div>
              <div className="text-[19px] font-bold num leading-tight">{bytes(cur.used, lang)}</div>
              <div className="text-[11.5px] text-ink3 num">{cur.dataLimit > 0 ? <><bdi>{pc(pct, 0)}</bdi> · <bdi>{bytes(cur.dataLimit, lang)}</bdi></> : "∞"}</div>
            </div>
          </Ring>
          <div className="grid grid-cols-2 gap-x-5 gap-y-3.5">
            <KV label={t("status")}><StatusBadge status={cur.status} /></KV>
            <KV label={t("expiry")}>{cur.daysLeft === null ? t("never") : <span className="num">{n(cur.daysLeft)} {t("days")}{cur.expireAt ? <span className="text-ink3 text-[12px]"> · {date(cur.expireAt, lang)}</span> : null}</span>}</KV>
            <KV label={t("speedNow")}><span className="num">↓ {speed(cur.speedDown, lang)} · ↑ {speed(cur.speedUp, lang)}</span></KV>
            <KV label={t("lastOnline")}>{cur.online ? <span className="text-online inline-flex items-center gap-1.5"><Dot color="var(--color-online)" pulse size={7} />{t("online")}</span> : ago(cur.lastOnlineAt, lang)}</KV>
            <KV label={t("devices")}><span className="num">{n(det?.deviceList?.length ?? cur.devices)} / {cur.ipLimit > 0 ? n(cur.ipLimit) : "∞"}</span></KV>
            {(() => { const p = pace(series, cur); return p.avg > 0 ? (
              <KV label={t("connAvg")}><span className="num">{bytes(p.avg, lang)}</span>{p.days != null && <span className={cx("block text-[12px]", p.soon ? "text-warn" : "text-ink3")}>{t("connForecast").replace("{d}", n(p.days))}{p.soon ? t("connForecastSoon") : ""}</span>}</KV>
            ) : null; })()}
            <KV label={t("lifetime")}><span className="num">{bytes(cur.lifetimeUsed, lang)}</span></KV>
          </div>
        </div>

        <Panel icon={<QrCode className="size-4" />} title={t("subLink")} className="!rounded-2xl">
          <div className="flex gap-4 items-start">
            <div className="rounded-2xl bg-white p-2 shrink-0"><img src={qrURL(cur.subUrl)} alt="QR" className="size-28 sensitive" /></div>
            <div className="flex-1 min-w-0 flex flex-col gap-2.5">
              <CopyField value={cur.subUrl} />
              <a className="inline-flex items-center gap-1.5 text-[13px] text-primary2 hover:text-ink" href={cur.subUrl} target="_blank" rel="noreferrer">{t("openUserPage")} <ExternalLink className="size-3.5" /></a>
            </div>
          </div>
          <div className="mt-4 pt-3 border-t border-hair">
            <div className="flex items-baseline justify-between gap-2 mb-1.5 text-[12.5px]">
              <span className="font-medium text-ink2">{t("xrayJson")}</span>
              <span className="text-ink3">v2rayNG · v2rayN · Streisand</span>
            </div>
            <CopyField value={cur.subUrl + "/xray"} />
          </div>
        </Panel>

        <div className="grid grid-cols-2 gap-3 text-[12.5px] text-ink2">
          <div>{t("created")}: <span className="text-ink">{date(cur.createdAt, lang)}</span></div>
          <div>{t("resetPeriod")}: <span className="text-ink">{t(cur.resetPeriod === "none" ? "none" : cur.resetPeriod)}</span></div>
          <div className="col-span-2 truncate">UUID: <bdi dir="ltr" className="text-ink mono text-[11.5px] sensitive">{cur.uuid}</bdi></div>
        </div>
        </>}

        {tab === "devices" && <Connections u={cur} det={det} />}

        {tab === "configs" && <Panel icon={<Gauge className="size-4" />} title={t("configs")} sub={cfg ? n(cfg.configs.length) : ""} className="!rounded-2xl">
          {!cfg ? <Skeleton className="h-24" /> : (
            <ul className="flex flex-col gap-1.5">
              {cfg.configs.map((c, i) => (
                <li key={i} className="rounded-xl bg-fg/[.03] border border-hair">
                  <div className="flex items-center gap-2 ps-3.5 pe-1 h-12">
                    <span className="flex-1 min-w-0 truncate text-[13px]" dir="ltr">{c.name}</span>
                    <IconButton label="QR" onClick={() => setOpenQr(openQr === i ? null : i)}><QrCode className="size-4" /></IconButton>
                    {c.file && <IconButton label={`${t("download")} ${c.file}`} onClick={() => saveText(c.file!, c.link)}><Download className="size-4" /></IconButton>}
                    {c.note && <IconButton label={t("copyTunnelDetails")} onClick={() => copyText(c.note!).then(() => toast(t("copied")))}><FileText className="size-4" /></IconButton>}
                    <IconButton label={t("copy")} onClick={() => copyText(c.link).then(() => toast(t("copied")))}><Copy className="size-4" /></IconButton>
                  </div>
                  {openQr === i && <div className="px-3 pb-3 flex justify-center"><div className="rounded-2xl bg-white p-2">
                    <img src={c.qr ? "data:image/svg+xml;charset=utf-8," + encodeURIComponent(c.qr) : qrURL(c.link)} alt="QR" className={c.qr ? "w-72 h-72" : "w-56 h-56"} />
                  </div></div>}
                </li>
              ))}
              {cfg.configs.length === 0 && <li className="text-ink3 text-sm">—</li>}
            </ul>
          )}
        </Panel>}

        {tab === "usage" && <Panel title={t("usageHistory")} className="!rounded-2xl"
          action={<Segmented value={days} onChange={setDays} options={[{ value: 7, label: t("r7d") }, { value: 30, label: t("r30d") }, { value: 90, label: t("r90d") }]} />}>
          <UsageChart series={series} days={days} height={200} exportName={`dunvex-${cur.name}-${days}d`} />
        </Panel>}

        {tab === "activity" && <Panel title={t("activity")} className="!rounded-2xl">
          {!activity ? <Skeleton className="h-24" /> : activity.length === 0 ? <div className="text-ink3 text-[13px] py-4">{t("noEvents")}</div> : (
            <ol className="relative flex flex-col gap-4 ps-5 before:absolute before:top-1 before:bottom-1 before:[inset-inline-start:5px] before:w-px before:bg-hair">
              {activity.map((e) => (
                <li key={e.id} className="relative">
                  <span className="absolute [inset-inline-start:-19px] top-1.5 size-2.5 rounded-full border-2 border-panel" style={{ background: eventMeta(e.kind).color }} />
                  <EventMsg message={e.message} className="block text-[13px]" />
                  <div className="text-[11.5px] text-ink3">{ago(e.at, lang)}</div>
                </li>
              ))}
            </ol>
          )}
        </Panel>}
      </div>
    </Sheet>
  );
}

