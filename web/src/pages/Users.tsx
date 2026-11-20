import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CalendarClock, CalendarPlus, Copy, ExternalLink, Gauge, KeyRound, Pencil, Play, Power, QrCode, RotateCcw, Search, Smartphone, Trash2, UserPlus,
  Users as UsersIcon, Wifi,
} from "lucide-react";
import { api, qrURL, type ConfigEntry, type Inbound, type Status, type User } from "../lib/api";
import { GB, ago, bytes, copyText, cx, date, speed, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import {
  Avatar, Badge, Button, ConfirmButton, CopyField, Dot, Empty, IconButton, Input, KV, Label, Meter, Panel, Ring, ringColor, Segmented, Select, Sheet,
  Skeleton, StatusBadge, Tabs, Toggle, useToast,
} from "../components/ui";
import { UsageChart } from "../components/charts";
import { PageHead, can, navigate, useSession } from "../App";

type Filter = "" | "online" | Status;

export function UsersPage({ arg }: { arg?: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const { me, reload } = useSession();
  const [users, setUsers] = useState<User[] | null>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("");
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [qr, setQr] = useState<User | null>(null);

  const load = useCallback(() => api<User[]>("users").then(setUsers).catch((e) => toast(e.message, "error")), [toast]);
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

  const list = useMemo(() => {
    const s = q.trim().toLowerCase();
    return (users ?? []).filter(
      (u) => (!s || (u.name + " " + u.note + " " + (u.owner ?? "")).toLowerCase().includes(s)) && (filter === "" || (filter === "online" ? u.online : u.status === filter)),
    );
  }, [users, q, filter]);

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

      {sel.size > 0 && (
        <div className="rise sticky top-16 md:top-4 z-20 mb-3 flex flex-wrap items-center gap-2 rounded-2xl glass-strong px-3 py-2.5 shadow-[0_18px_40px_-16px_rgba(0,0,0,.8)]">
          <Badge color="var(--color-cdn)" className="h-7">{n(sel.size)} {t("selected")}</Badge>
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
        <div className="glass rounded-[20px]"><Empty icon={<Search className="size-7" />} title={t("noMatch")} /></div>
      ) : (
        <div className="glass edge rounded-[20px] overflow-hidden">
          <div className="hidden lg:grid grid-cols-[32px_minmax(170px,1.5fr)_128px_minmax(160px,1.3fr)_124px_76px_92px_112px] items-center gap-3 px-5 h-12 text-[12px] text-ink3 font-medium border-b border-white/[.07] bg-white/[.015]">
            <input type="checkbox" aria-label={t("all")} className="accent-[#4c8dff] size-4 cursor-pointer" checked={sel.size > 0 && sel.size === list.length}
              onChange={(e) => setSel(e.target.checked ? new Set(list.map((u) => u.id)) : new Set())} />
            <span>{t("name")}</span>
            <span>{t("status")}</span>
            <span>{t("usage")}</span>
            <span>{t("expiry")}</span>
            <span>{t("devices")}</span>
            <span>{t("speed")}</span>
            <span />
          </div>
          <ul>
            {list.map((u) => (
              <UserRow key={u.id} u={u} checked={sel.has(u.id)}
                onCheck={(v) => setSel((s) => { const x = new Set(s); v ? x.add(u.id) : x.delete(u.id); return x; })}
                onQr={() => setQr(u)} onCopy={() => copyText(u.subUrl).then(() => toast(t("copied")))} />
            ))}
          </ul>
        </div>
      )}

      <UserForm open={arg === "new"} onClose={close} onSaved={(u) => { load(); reload(); navigate("users", String(u.id)); }} />
      {editing && <UserDetail key={editing.id} u={editing} onClose={close} onChanged={() => { load(); reload(); }} />}
      <Sheet open={!!qr} onClose={() => setQr(null)} title={<span dir="ltr">{qr?.name}</span>} sub={t("subLink")}>
        {qr && (
          <div className="flex flex-col items-center gap-5">
            <div className="rounded-3xl p-4 bg-white shadow-[0_20px_60px_-20px_rgba(76,141,255,.6)]"><img src={qrURL(qr.subUrl)} alt="QR" className="w-64 h-64" /></div>
            <CopyField value={qr.subUrl} className="w-full" />
            <a className="inline-flex items-center gap-1.5 text-[13px] text-[#8db6ff] hover:text-white" href={qr.subUrl} target="_blank" rel="noreferrer">{t("openUserPage")} <ExternalLink className="size-3.5" /></a>
          </div>
        )}
      </Sheet>
    </div>
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
      <span className={cx("num text-[13px] font-medium", warn && "text-warn", u.daysLeft === 0 && "text-bad")}>{n(u.daysLeft)} {t("daysLeft")}</span>
      <div className="text-[11.5px] text-ink3 num">{date(u.expireAt, lang)}</div>
    </div>
  );
}

function UserRow({ u, checked, onCheck, onQr, onCopy }: { u: User; checked: boolean; onCheck: (v: boolean) => void; onQr: () => void; onCopy: () => void }) {
  const { t, lang, n } = useI18n();
  const pct = u.dataLimit > 0 ? (u.used / u.dataLimit) * 100 : 0;
  return (
    <li className={cx("border-b border-white/[.05] last:border-b-0 transition-colors", checked ? "bg-cdn/[.08]" : "hover:bg-white/[.025]")}>
      <div className="grid grid-cols-[28px_1fr_auto] lg:grid-cols-[32px_minmax(170px,1.5fr)_128px_minmax(160px,1.3fr)_124px_76px_92px_112px] items-center gap-x-3 gap-y-2.5 px-4 lg:px-5 py-3.5">
        <input type="checkbox" aria-label={u.name} className="accent-[#4c8dff] size-4 cursor-pointer" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        <a href={`#/users/${u.id}`} className="min-w-0 flex items-center gap-3 group">
          <Avatar name={u.name} size={38} online={u.online} />
          <span className="min-w-0">
            <span className="block font-semibold truncate group-hover:text-white" dir="ltr">{u.name}</span>
            <span className="block text-[12px] text-ink3 truncate">
              {u.note || (u.online ? <span className="text-online">{t("online")}</span> : ago(u.lastOnlineAt, lang))}
              {u.owner && <span className="text-violet"> · {u.owner}</span>}
            </span>
          </span>
        </a>
        <div className="flex items-center gap-0.5 lg:hidden">
          <IconButton label={t("copy")} onClick={onCopy}><Copy className="size-4" /></IconButton>
          <IconButton label="QR" onClick={onQr}><QrCode className="size-4" /></IconButton>
        </div>
        <div className="col-start-2 lg:col-start-auto"><StatusBadge status={u.status} /></div>
        <div className="col-span-3 lg:col-span-1 col-start-1 lg:col-start-auto min-w-0 ps-10 lg:ps-0">
          <div className="flex justify-between items-baseline gap-2 text-[12.5px] num mb-1.5">
            <span className="font-medium">{bytes(u.used, lang)}</span>
            {u.dataLimit > 0 ? (
              <span className="text-ink3 inline-flex gap-1.5"><bdi>{bytes(u.dataLimit, lang)}</bdi><bdi className={cx(pct >= 90 && "text-bad", pct >= 75 && pct < 90 && "text-warn")}>{n(pct, 0)}%</bdi></span>
            ) : (
              <span className="text-ink3">{t("unlimited")}</span>
            )}
          </div>
          <Meter value={u.used} max={u.dataLimit > 0 ? u.dataLimit : 0} />
        </div>
        <div className="hidden lg:block"><ExpiryCell u={u} /></div>
        <div className="hidden lg:flex items-center gap-1.5 text-[13px] num">
          <Smartphone className={cx("size-4", u.devices > 0 ? "text-[#8db6ff]" : "text-ink3")} />
          <span className={cx(u.ipLimit > 0 && u.devices > u.ipLimit && "text-bad")}>{n(u.devices)}</span>
          <span className="text-ink3">/ {u.ipLimit > 0 ? n(u.ipLimit) : "∞"}</span>
        </div>
        <div className="hidden lg:block text-[12.5px] num text-ink2">{u.speedDown + u.speedUp > 0 ? <span className="text-ink">{speed(u.speedDown + u.speedUp, lang)}</span> : "—"}</div>
        <div className="hidden lg:flex items-center justify-end gap-0.5">
          <IconButton label={t("copy")} onClick={onCopy}><Copy className="size-4" /></IconButton>
          <IconButton label="QR" onClick={onQr}><QrCode className="size-4" /></IconButton>
          <IconButton label={t("edit")} onClick={() => navigate("users", String(u.id))}><Pencil className="size-4" /></IconButton>
        </div>
      </div>
    </li>
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
}

function UserForm({ open, onClose, onSaved, user }: { open: boolean; onClose: () => void; onSaved: (u: User) => void; user?: User }) {
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
        }
      : { name: "", count: 1, gb: 30, days: 30, startOnFirstUse: true, resetPeriod: "none", inboundIds: [], note: "", ipLimit: 0 };
  const [f, setF] = useState<FormState>(init);
  const [daysTouched, setDaysTouched] = useState(false);
  useEffect(() => {
    if (open) {
      setF(init());
      setDaysTouched(false);
      if (isAdmin) api<Inbound[]>("inbounds").then(setInbounds).catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((x) => ({ ...x, [k]: v }));

  const submit = () => {
    setBusy(true);
    const body: Record<string, unknown> = { name: f.name, dataLimit: Math.round(f.gb * GB), resetPeriod: f.resetPeriod, note: f.note, ipLimit: f.ipLimit };
    if (isAdmin) body.inboundIds = f.inboundIds;
    if (!user || daysTouched) {
      body.durationDays = f.days;
      body.startOnFirstUse = f.startOnFirstUse;
    }
    if (!user) body.count = f.count;
    const req = user ? api<User>(`users/${user.id}`, { method: "PUT", body }) : api<User[]>("users", { body }).then((list) => list[0]);
    req.then((u) => { toast(t("saved")); onSaved(u); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };

  return (
    <Sheet open={open} onClose={onClose} title={user ? t("edit") : t("newUser")} sub={user ? <span dir="ltr">{user.name}</span> : undefined}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button><Button variant="primary" loading={busy} onClick={submit}>{user ? t("save") : t("create")}</Button></>}>
      <div className="flex flex-col gap-5">
        <FormSection title={t("general")}>
          <div className="grid grid-cols-[1fr_100px] gap-3">
            <label><Label>{t("name")}</Label><Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="ali-phone" dir="ltr" autoFocus /></label>
            {!user && <label><Label>{t("count")}</Label><Input type="number" min={1} max={500} value={f.count} onChange={(e) => set("count", Math.max(1, +e.target.value || 1))} className="num" /></label>}
          </div>
          <label className="block mt-3"><Label>{t("note")}</Label><Input value={f.note} onChange={(e) => set("note", e.target.value)} /></label>
        </FormSection>

        <FormSection title={t("plan")}>
          <Label hint={f.gb > 0 ? `${n(f.gb)} ${t("gb")}` : t("unlimited")}>{t("dataLimit")}</Label>
          <Segmented value={[10, 30, 50, 100, 200, 0].includes(f.gb) ? f.gb : -1} onChange={(v) => v >= 0 && set("gb", v)}
            options={[10, 30, 50, 100, 200].map((g) => ({ value: g, label: `${n(g)} ${t("gb")}` })).concat([{ value: 0, label: t("unlimited") }])} />
          <Input type="number" min={0} step="0.5" value={f.gb} onChange={(e) => set("gb", Math.max(0, +e.target.value || 0))} className="num mt-2 max-w-40" aria-label={t("gb")} />

          <div className="mt-5">
            <Label hint={f.days > 0 ? `${n(f.days)} ${t("days")}` : t("never")}>{t("duration")}</Label>
            <Segmented value={[7, 30, 60, 90, 180, 0].includes(f.days) ? f.days : -1} onChange={(v) => { if (v >= 0) { set("days", v); setDaysTouched(true); } }}
              options={[7, 30, 60, 90, 180].map((d) => ({ value: d, label: `${n(d)} ${t("days")}` })).concat([{ value: 0, label: t("never") }])} />
            <Input type="number" min={0} value={f.days} onChange={(e) => { set("days", Math.max(0, +e.target.value || 0)); setDaysTouched(true); }} className="num mt-2 max-w-40" aria-label={t("days")} />
            {f.days > 0 && (!user || user.firstUseAt === 0) && (
              <div className="mt-3"><Toggle checked={f.startOnFirstUse} onChange={(v) => { set("startOnFirstUse", v); setDaysTouched(true); }} label={t("startOnFirst")} /></div>
            )}
          </div>
          <label className="block mt-5">
            <Label>{t("resetPeriod")}</Label>
            <Select value={f.resetPeriod} onChange={(v) => set("resetPeriod", v as FormState["resetPeriod"])} options={(["none", "daily", "weekly", "monthly"] as const).map((p) => ({ value: p, label: t(p) }))} />
          </label>
        </FormSection>

        <FormSection title={t("limits")}>
          <Label hint={f.ipLimit > 0 ? n(f.ipLimit) : t("unlimited")}>{t("deviceLimit")}</Label>
          <Segmented value={[0, 1, 2, 3, 5].includes(f.ipLimit) ? f.ipLimit : -1} onChange={(v) => v >= 0 && set("ipLimit", v)}
            options={[{ value: 0, label: t("unlimited") }, ...[1, 2, 3, 5].map((d) => ({ value: d, label: n(d) }))]} />
          <p className="text-[12.5px] text-ink3 mt-2">{t("deviceLimitHint")}</p>
        </FormSection>

        {isAdmin && inbounds.length > 0 && (
          <FormSection title={t("access")}>
            <Segmented value={f.inboundIds.length ? "some" : "all"} onChange={(v) => set("inboundIds", v === "all" ? [] : inbounds.map((i) => i.id))}
              options={[{ value: "all", label: t("allProtocols") }, { value: "some", label: t("onlyThese") }]} />
            {f.inboundIds.length > 0 && (
              <div className="mt-3 flex flex-col gap-1 rounded-2xl border border-white/10 p-2">
                {inbounds.map((i) => (
                  <label key={i.id} className="flex items-center gap-3 h-10 px-2.5 rounded-xl hover:bg-white/[.04] cursor-pointer">
                    <input type="checkbox" className="accent-[#4c8dff] size-4" checked={f.inboundIds.includes(i.id)}
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

function FormSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl bg-white/[.025] border border-white/[.07] p-4">
      <h3 className="text-[12px] font-semibold uppercase tracking-wide text-ink3 mb-3">{title}</h3>
      {children}
    </section>
  );
}

// ---------------------------------------------------------------- detail

interface Detail extends User {
  deviceList: { ip: string; lastSeen: number }[];
}

function UserDetail({ u, onClose, onChanged }: { u: User; onClose: () => void; onChanged: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [cfg, setCfg] = useState<{ subUrl: string; configs: ConfigEntry[] } | null>(null);
  const [series, setSeries] = useState<{ t: number; up: number; down: number }[]>([]);
  const [det, setDet] = useState<Detail | null>(null);
  const [edit, setEdit] = useState(false);
  const [openQr, setOpenQr] = useState<number | null>(null);
  useEffect(() => {
    api<{ subUrl: string; configs: ConfigEntry[] }>(`users/${u.id}/configs`).then(setCfg).catch(() => {});
    api<typeof series>(`users/${u.id}/usage?days=30`).then(setSeries).catch(() => {});
  }, [u.id, u.subToken]);
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

  if (edit) return <UserForm open user={cur} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); onChanged(); }} />;

  return (
    <Sheet open onClose={onClose} wide title={<span className="inline-flex items-center gap-3"><Avatar name={cur.name} size={34} online={cur.online} /><span dir="ltr">{cur.name}</span></span>}
      footer={
        <div className="flex flex-wrap gap-2 w-full">
          <Button size="sm" icon={<Pencil className="size-4" />} onClick={() => setEdit(true)}>{t("edit")}</Button>
          <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => act(`users/${cur.id}/reset`)}>{t("resetUsage")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => act(`users/${cur.id}`, "PUT", { enabled: !cur.enabled })}>{cur.enabled ? t("disable") : t("enable")}</Button>
          <Button size="sm" icon={<KeyRound className="size-4" />} onClick={() => act(`users/${cur.id}/regenerate`)}>{t("regenerate")}</Button>
          <span className="ms-auto" />
          <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => api(`users/${cur.id}`, { method: "DELETE" }).then(() => { onChanged(); onClose(); })}>{t("delete")}</ConfirmButton>
        </div>
      }>
      <div className="flex flex-col gap-5">
        {cur.status === "paused" && (
          <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-violet/40 bg-violet/[.08] px-4 py-3 text-[13.5px]">
            <Smartphone className="size-5 text-violet shrink-0" />
            <span className="flex-1 min-w-0">{t("pausedUntil")} <b className="num">{new Date(cur.pausedUntil * 1000).toLocaleTimeString(lang === "fa" ? "fa-IR" : "en-GB", { hour: "2-digit", minute: "2-digit" })}</b></span>
            <Button size="sm" variant="soft" icon={<Play className="size-4" />} onClick={() => act(`users/${cur.id}`, "PUT", { resume: true })}>{t("resume")}</Button>
          </div>
        )}

        <div className="grid sm:grid-cols-[auto_1fr] gap-5 items-center rounded-2xl bg-white/[.025] border border-white/[.07] p-4">
          <Ring value={cur.dataLimit > 0 ? pct : 100} size={128} stroke={11} color={cur.dataLimit > 0 ? ringColor(pct) : "url(#ring-ok)"}>
            <div>
              <div className="text-[19px] font-bold num leading-tight">{bytes(cur.used, lang)}</div>
              <div className="text-[11.5px] text-ink3 num">{cur.dataLimit > 0 ? <><bdi>{n(pct, 0)}%</bdi> · <bdi>{bytes(cur.dataLimit, lang)}</bdi></> : "∞"}</div>
            </div>
          </Ring>
          <div className="grid grid-cols-2 gap-x-5 gap-y-3.5">
            <KV label={t("status")}><StatusBadge status={cur.status} /></KV>
            <KV label={t("expiry")}>{cur.daysLeft === null ? t("never") : <span className="num">{n(cur.daysLeft)} {t("days")}{cur.expireAt ? <span className="text-ink3 text-[12px]"> · {date(cur.expireAt, lang)}</span> : null}</span>}</KV>
            <KV label={t("speedNow")}><span className="num">↓ {speed(cur.speedDown, lang)} · ↑ {speed(cur.speedUp, lang)}</span></KV>
            <KV label={t("lastOnline")}>{cur.online ? <span className="text-online inline-flex items-center gap-1.5"><Dot color="var(--color-online)" pulse size={7} />{t("online")}</span> : ago(cur.lastOnlineAt, lang)}</KV>
            <KV label={t("devices")}><span className="num">{n(det?.deviceList?.length ?? cur.devices)} / {cur.ipLimit > 0 ? n(cur.ipLimit) : "∞"}</span></KV>
            <KV label={t("lifetime")}><span className="num">{bytes(cur.lifetimeUsed, lang)}</span></KV>
          </div>
        </div>

        <Panel icon={<QrCode className="size-4" />} title={t("subLink")} className="!rounded-2xl">
          <div className="flex gap-4 items-start">
            <div className="rounded-2xl bg-white p-2 shrink-0"><img src={qrURL(cur.subUrl)} alt="QR" className="size-28" /></div>
            <div className="flex-1 min-w-0 flex flex-col gap-2.5">
              <CopyField value={cur.subUrl} />
              <a className="inline-flex items-center gap-1.5 text-[13px] text-[#8db6ff] hover:text-white" href={cur.subUrl} target="_blank" rel="noreferrer">{t("openUserPage")} <ExternalLink className="size-3.5" /></a>
            </div>
          </div>
        </Panel>

        <Panel icon={<Wifi className="size-4" />} title={t("devices")} sub={cur.ipLimit > 0 ? `${t("deviceLimit")}: ${n(cur.ipLimit)}` : undefined} className="!rounded-2xl">
          {!det ? <Skeleton className="h-12" /> : det.deviceList.length === 0 ? (
            <div className="text-ink3 text-[13px] py-2">{t("noDevices")}</div>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {det.deviceList.map((d) => (
                <li key={d.ip} className="flex items-center gap-3 h-11 px-3 rounded-xl bg-white/[.03] border border-white/[.05]">
                  <Smartphone className="size-4 text-[#8db6ff]" />
                  <bdi dir="ltr" className="mono text-[12.5px]">{d.ip}</bdi>
                  <span className="ms-auto text-[12px] text-ink3">{ago(d.lastSeen, lang)}</span>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <Panel icon={<Gauge className="size-4" />} title={t("configs")} sub={cfg ? n(cfg.configs.length) : ""} className="!rounded-2xl">
          {!cfg ? <Skeleton className="h-24" /> : (
            <ul className="flex flex-col gap-1.5">
              {cfg.configs.map((c, i) => (
                <li key={i} className="rounded-xl bg-white/[.03] border border-white/[.06]">
                  <div className="flex items-center gap-2 ps-3.5 pe-1 h-12">
                    <span className="flex-1 min-w-0 truncate text-[13px]" dir="ltr">{c.name}</span>
                    <IconButton label="QR" onClick={() => setOpenQr(openQr === i ? null : i)}><QrCode className="size-4" /></IconButton>
                    <IconButton label={t("copy")} onClick={() => copyText(c.link).then(() => toast(t("copied")))}><Copy className="size-4" /></IconButton>
                  </div>
                  {openQr === i && <div className="px-3 pb-3 flex justify-center"><div className="rounded-2xl bg-white p-2"><img src={qrURL(c.link)} alt="QR" className="w-56 h-56" /></div></div>}
                </li>
              ))}
              {cfg.configs.length === 0 && <li className="text-ink3 text-sm">—</li>}
            </ul>
          )}
        </Panel>

        <Panel title={t("usage30")} className="!rounded-2xl">
          <UsageChart series={series} height={180} />
        </Panel>

        <div className="grid grid-cols-2 gap-3 text-[12.5px] text-ink2">
          <div>{t("created")}: <span className="text-ink">{date(cur.createdAt, lang)}</span></div>
          <div>{t("resetPeriod")}: <span className="text-ink">{t(cur.resetPeriod === "none" ? "none" : cur.resetPeriod)}</span></div>
          <div className="col-span-2 truncate">UUID: <bdi dir="ltr" className="text-ink mono text-[11.5px]">{cur.uuid}</bdi></div>
        </div>
      </div>
    </Sheet>
  );
}

export const _statusMeta = statusMeta;
