import { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarPlus, Copy, KeyRound, Pencil, Power, QrCode, RotateCcw, Search, Trash2, UserPlus, Users as UsersIcon } from "lucide-react";
import { api, qrURL, type ConfigEntry, type Inbound, type Status, type User } from "../lib/api";
import { GB, ago, bytes, copyText, cx, date, speed, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Button, ConfirmButton, CopyField, Dot, Empty, IconButton, Input, Label, Meter, Segmented, Select, Sheet, Spinner, Toggle, useToast } from "../components/ui";
import { UsageBars } from "../components/charts";
import { PageHead, navigate } from "../App";

type Filter = "" | "online" | Status;

export function UsersPage({ arg }: { arg?: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [users, setUsers] = useState<User[] | null>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("");
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [qr, setQr] = useState<User | null>(null);

  const load = useCallback(() => api<User[]>("users").then(setUsers).catch((e) => toast(e.message, "error")), [toast]);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
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
      (u) => (!s || (u.name + " " + u.note).toLowerCase().includes(s)) && (filter === "" || (filter === "online" ? u.online : u.status === filter)),
    );
  }, [users, q, filter]);

  const bulk = (action: string, extra: Record<string, unknown> = {}) =>
    api<{ affected: number }>("users/bulk", { body: { ids: [...sel], action, ...extra } })
      .then((r) => {
        toast(`${n(r.affected)} ✓`);
        setSel(new Set());
        load();
      })
      .catch((e) => toast(e.message, "error"));

  const editing = arg && arg !== "new" ? users?.find((u) => String(u.id) === arg) : undefined;
  const close = () => navigate("users");

  const filters: { id: Filter; label: string }[] = [
    { id: "", label: t("all") },
    { id: "online", label: t("online") },
    { id: "active", label: t("active") },
    { id: "pending", label: t("pending") },
    { id: "limited", label: t("limited") },
    { id: "expired", label: t("expired") },
    { id: "disabled", label: t("disabled") },
  ];

  return (
    <div className="rise">
      <PageHead
        title={t("users")}
        sub={<span className="num">{n(counts[""])} · <span className="text-online">{n(counts.online)} {t("online")}</span></span>}
        action={<Button variant="primary" icon={<UserPlus className="size-4" />} onClick={() => navigate("users", "new")}>{t("newUser")}</Button>}
      />

      <div className="flex flex-col lg:flex-row gap-3 lg:items-center mb-4">
        <div className="relative lg:w-72">
          <Search className="size-4 text-ink3 absolute top-3 [inset-inline-start:12px]" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("search")} className="ps-9" />
        </div>
        <div className="flex gap-1.5 overflow-x-auto pb-1 -mb-1">
          {filters.map((f) =>
            f.id === "" || counts[f.id] ? (
              <button
                key={f.id}
                onClick={() => setFilter(f.id)}
                className={cx("h-8 px-3 rounded-lg text-[13px] border whitespace-nowrap", filter === f.id ? "bg-ink text-ground border-ink" : "border-hair2 text-ink2 hover:text-ink")}
              >
                {f.label} <span className="num opacity-70">{n(counts[f.id] ?? 0)}</span>
              </button>
            ) : null,
          )}
        </div>
      </div>

      {sel.size > 0 && (
        <div className="rise sticky top-16 md:top-3 z-20 mb-3 flex flex-wrap items-center gap-2 rounded-2xl bg-panel2 border border-hair2 px-3 py-2 shadow-[0_12px_30px_-14px_rgba(0,0,0,.7)]">
          <span className="text-[13px] px-1 num">{n(sel.size)} {t("selected")}</span>
          <Button size="sm" icon={<CalendarPlus className="size-4" />} onClick={() => bulk("extend", { days: 30 })}>{t("extend")} {n(30)}</Button>
          <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => bulk("reset")}>{t("resetUsage")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => bulk("enable")}>{t("enable")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => bulk("disable")}>{t("disable")}</Button>
          <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => bulk("delete")}>{t("delete")}</ConfirmButton>
          <button onClick={() => setSel(new Set())} className="ms-auto text-ink3 text-[13px] px-2">{t("cancel")}</button>
        </div>
      )}

      {!users ? (
        <Spinner />
      ) : users.length === 0 ? (
        <div className="rounded-2xl border border-hair bg-panel">
          <Empty icon={<UsersIcon className="size-6" />} title={t("emptyUsers")} hint={t("emptyUsersHint")} action={<Button variant="primary" icon={<UserPlus className="size-4" />} onClick={() => navigate("users", "new")}>{t("newUser")}</Button>} />
        </div>
      ) : list.length === 0 ? (
        <div className="rounded-2xl border border-hair bg-panel"><Empty icon={<Search className="size-6" />} title={t("noMatch")} /></div>
      ) : (
        <div className="rounded-2xl border border-hair bg-panel overflow-hidden">
          {/* header (desktop) */}
          <div className="hidden lg:grid grid-cols-[36px_minmax(170px,1.4fr)_130px_minmax(180px,1.2fr)_130px_120px_112px] items-center gap-3 px-4 h-11 text-[12.5px] text-ink3 border-b border-hair">
            <input
              type="checkbox"
              aria-label={t("all")}
              className="accent-[#eef1f6] size-4"
              checked={sel.size > 0 && sel.size === list.length}
              onChange={(e) => setSel(e.target.checked ? new Set(list.map((u) => u.id)) : new Set())}
            />
            <span>{t("name")}</span>
            <span>{t("status")}</span>
            <span>{t("usage")}</span>
            <span>{t("expiry")}</span>
            <span>{t("speed")}</span>
            <span />
          </div>
          <ul>
            {list.map((u) => (
              <UserRow
                key={u.id}
                u={u}
                checked={sel.has(u.id)}
                onCheck={(v) => setSel((s) => { const x = new Set(s); v ? x.add(u.id) : x.delete(u.id); return x; })}
                onQr={() => setQr(u)}
                onCopy={() => copyText(u.subUrl).then(() => toast(t("copied")))}
                lang={lang}
              />
            ))}
          </ul>
        </div>
      )}

      <UserForm open={arg === "new"} onClose={close} onSaved={(u) => { load(); navigate("users", String(u.id)); }} />
      {editing && <UserDetail key={editing.id} u={editing} onClose={close} onChanged={load} />}
      <Sheet open={!!qr} onClose={() => setQr(null)} title={qr?.name ?? ""}>
        {qr && (
          <div className="flex flex-col items-center gap-4">
            <img src={qrURL(qr.subUrl)} alt="QR" className="w-64 h-64 rounded-2xl bg-white p-3" />
            <CopyField value={qr.subUrl} className="w-full" />
          </div>
        )}
      </Sheet>
    </div>
  );
}

function UserRow({ u, checked, onCheck, onQr, onCopy, lang }: { u: User; checked: boolean; onCheck: (v: boolean) => void; onQr: () => void; onCopy: () => void; lang: "fa" | "en" }) {
  const { t, n } = useI18n();
  const meta = statusMeta[u.status];
  return (
    <li className={cx("border-b border-hair last:border-b-0 transition-colors", checked ? "bg-[#14234a]" : "hover:bg-[#0f1a35]")}>
      <div className="grid grid-cols-[28px_1fr_auto] lg:grid-cols-[36px_minmax(170px,1.4fr)_130px_minmax(180px,1.2fr)_130px_120px_112px] items-center gap-x-3 gap-y-2 px-4 py-3">
        <input type="checkbox" aria-label={u.name} className="accent-[#eef1f6] size-4" checked={checked} onChange={(e) => onCheck(e.target.checked)} />
        <a href={`#/users/${u.id}`} className="min-w-0 flex items-center gap-2.5">
          <Dot color={u.online ? "var(--color-online)" : "var(--color-hair2)"} pulse={u.online} />
          <span className="min-w-0">
            <span className="block font-semibold truncate" dir="ltr">{u.name}</span>
            {u.note && <span className="block text-[12px] text-ink3 truncate">{u.note}</span>}
          </span>
        </a>
        <div className="flex items-center gap-0.5 lg:hidden">
          <IconButton label={t("copy")} onClick={onCopy}><Copy className="size-4" /></IconButton>
          <IconButton label="QR" onClick={onQr}><QrCode className="size-4" /></IconButton>
        </div>
        <span className="col-start-2 lg:col-start-auto inline-flex items-center gap-1.5 text-[12.5px]" style={{ color: meta.color }}>
          <Dot color={meta.color} />
          {t(meta.key)}
        </span>
        <div className="col-span-3 lg:col-span-1 col-start-1 lg:col-start-auto min-w-0 ps-10 lg:ps-0">
          <div className="flex justify-between text-[12.5px] num mb-1">
            <span>{bytes(u.used, lang)}</span>
            <span className="text-ink3">{u.dataLimit > 0 ? bytes(u.dataLimit, lang) : t("unlimited")}</span>
          </div>
          <Meter value={u.used} max={u.dataLimit > 0 ? u.dataLimit : 0} />
        </div>
        <div className="hidden lg:block text-[13px]">
          {u.daysLeft === null ? (
            <span className="text-ink3">{t("never")}</span>
          ) : (
            <span className={cx("num", u.daysLeft <= 3 && u.status !== "pending" ? "text-warn" : "")}>
              {n(u.daysLeft)} {t("daysLeft")}
            </span>
          )}
          <div className="text-[11.5px] text-ink3">{u.online ? t("online") : ago(u.lastOnlineAt, lang)}</div>
        </div>
        <div className="hidden lg:block text-[12.5px] num text-ink2">{u.speedDown + u.speedUp > 0 ? speed(u.speedDown + u.speedUp, lang) : "—"}</div>
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
  gb: number; // 0 = unlimited
  days: number; // 0 = never
  startOnFirstUse: boolean;
  resetPeriod: User["resetPeriod"];
  inboundIds: number[];
  note: string;
}

function UserForm({ open, onClose, onSaved, user }: { open: boolean; onClose: () => void; onSaved: (u: User) => void; user?: User }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [busy, setBusy] = useState(false);
  const init = (): FormState =>
    user
      ? {
          name: user.name,
          count: 1,
          gb: user.dataLimit > 0 ? +(user.dataLimit / GB).toFixed(2) : 0,
          days: user.daysLeft ?? 0,
          startOnFirstUse: user.status === "pending",
          resetPeriod: user.resetPeriod,
          inboundIds: user.inboundIds,
          note: user.note,
        }
      : { name: "", count: 1, gb: 30, days: 30, startOnFirstUse: true, resetPeriod: "none", inboundIds: [], note: "" };
  const [f, setF] = useState<FormState>(init);
  useEffect(() => {
    if (open) {
      setF(init());
      api<Inbound[]>("inbounds").then(setInbounds).catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((x) => ({ ...x, [k]: v }));
  const [daysTouched, setDaysTouched] = useState(false);

  const submit = () => {
    setBusy(true);
    const body: Record<string, unknown> = {
      name: f.name,
      dataLimit: Math.round(f.gb * GB),
      resetPeriod: f.resetPeriod,
      inboundIds: f.inboundIds,
      note: f.note,
    };
    if (!user || daysTouched) {
      body.durationDays = f.days;
      body.startOnFirstUse = f.startOnFirstUse;
    }
    if (!user) body.count = f.count;
    const req = user ? api<User>(`users/${user.id}`, { method: "PUT", body }) : api<User[]>("users", { body }).then((list) => list[0]);
    req
      .then((u) => {
        toast(t("saved"));
        onSaved(u);
      })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };

  return (
    <Sheet
      open={open}
      onClose={onClose}
      title={user ? `${t("edit")} · ${user.name}` : t("newUser")}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>{t("cancel")}</Button>
          <Button variant="primary" loading={busy} onClick={submit}>{user ? t("save") : t("create")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-6">
        <div className="grid grid-cols-[1fr_96px] gap-3">
          <label>
            <Label>{t("name")}</Label>
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="ali-phone" dir="ltr" autoFocus />
          </label>
          {!user && (
            <label>
              <Label>{t("count")}</Label>
              <Input type="number" min={1} max={500} value={f.count} onChange={(e) => set("count", Math.max(1, +e.target.value || 1))} className="num" />
            </label>
          )}
        </div>

        <div>
          <Label hint={f.gb > 0 ? `${n(f.gb)} ${t("gb")}` : t("unlimited")}>{t("dataLimit")}</Label>
          <Segmented value={[10, 30, 50, 100, 200, 0].includes(f.gb) ? f.gb : -1} onChange={(v) => v >= 0 && set("gb", v)}
            options={[10, 30, 50, 100, 200].map((g) => ({ value: g, label: `${n(g)} ${t("gb")}` })).concat([{ value: 0, label: t("unlimited") }])} />
          <Input type="number" min={0} step="0.5" value={f.gb} onChange={(e) => set("gb", Math.max(0, +e.target.value || 0))} className="num mt-2 max-w-40" aria-label={t("gb")} />
        </div>

        <div>
          <Label hint={f.days > 0 ? `${n(f.days)} ${t("days")}` : t("never")}>{t("duration")}</Label>
          <Segmented value={[7, 30, 60, 90, 180, 0].includes(f.days) ? f.days : -1} onChange={(v) => { if (v >= 0) { set("days", v); setDaysTouched(true); } }}
            options={[7, 30, 60, 90, 180].map((d) => ({ value: d, label: `${n(d)}` })).concat([{ value: 0, label: t("never") }])} />
          <Input type="number" min={0} value={f.days} onChange={(e) => { set("days", Math.max(0, +e.target.value || 0)); setDaysTouched(true); }} className="num mt-2 max-w-40" aria-label={t("days")} />
          {f.days > 0 && (!user || user.firstUseAt === 0) && (
            <div className="mt-3"><Toggle checked={f.startOnFirstUse} onChange={(v) => { set("startOnFirstUse", v); setDaysTouched(true); }} label={t("startOnFirst")} /></div>
          )}
        </div>

        <label>
          <Label>{t("resetPeriod")}</Label>
          <Select value={f.resetPeriod} onChange={(v) => set("resetPeriod", v as FormState["resetPeriod"])}
            options={(["none", "daily", "weekly", "monthly"] as const).map((p) => ({ value: p, label: t(p) }))} />
        </label>

        {inbounds.length > 0 && (
          <div>
            <Label>{t("protocols")}</Label>
            <Segmented value={f.inboundIds.length ? "some" : "all"} onChange={(v) => set("inboundIds", v === "all" ? [] : inbounds.map((i) => i.id))}
              options={[{ value: "all", label: t("allProtocols") }, { value: "some", label: t("onlyThese") }]} />
            {f.inboundIds.length > 0 && (
              <div className="mt-3 flex flex-col gap-1 rounded-xl border border-hair2 p-2">
                {inbounds.map((i) => (
                  <label key={i.id} className="flex items-center gap-3 h-9 px-2 rounded-lg hover:bg-panel2 cursor-pointer">
                    <input type="checkbox" className="accent-[#eef1f6] size-4" checked={f.inboundIds.includes(i.id)}
                      onChange={(e) => set("inboundIds", e.target.checked ? [...f.inboundIds, i.id] : f.inboundIds.filter((x) => x !== i.id))} />
                    <span className="text-[13.5px]" dir="ltr">{i.remark || i.name}</span>
                    <bdi dir="ltr" className="ms-auto text-ink3 text-[12px] num">:{i.port}</bdi>
                  </label>
                ))}
              </div>
            )}
          </div>
        )}

        <label>
          <Label>{t("note")}</Label>
          <Input value={f.note} onChange={(e) => set("note", e.target.value)} />
        </label>
      </div>
    </Sheet>
  );
}

// ---------------------------------------------------------------- detail

function UserDetail({ u, onClose, onChanged }: { u: User; onClose: () => void; onChanged: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [cfg, setCfg] = useState<{ subUrl: string; configs: ConfigEntry[] } | null>(null);
  const [series, setSeries] = useState<{ t: number; up: number; down: number }[]>([]);
  const [edit, setEdit] = useState(false);
  const [openQr, setOpenQr] = useState<number | null>(null);
  useEffect(() => {
    api<{ subUrl: string; configs: ConfigEntry[] }>(`users/${u.id}/configs`).then(setCfg).catch(() => {});
    api<typeof series>(`users/${u.id}/usage?days=30`).then(setSeries).catch(() => {});
  }, [u.id, u.subToken]);
  const act = (path: string, method = "POST", body?: unknown) =>
    api(path, { method, body: body ?? {} })
      .then(() => { toast(t("saved")); onChanged(); })
      .catch((e) => toast(e.message, "error"));
  const meta = statusMeta[u.status];

  if (edit) return <UserForm open user={u} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); onChanged(); }} />;

  return (
    <Sheet open onClose={onClose} wide title={<span dir="ltr">{u.name}</span>}
      footer={
        <div className="flex flex-wrap gap-2 w-full">
          <Button size="sm" icon={<Pencil className="size-4" />} onClick={() => setEdit(true)}>{t("edit")}</Button>
          <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => act(`users/${u.id}/reset`)}>{t("resetUsage")}</Button>
          <Button size="sm" icon={<Power className="size-4" />} onClick={() => act(`users/${u.id}`, "PUT", { enabled: !u.enabled })}>{u.enabled ? t("disable") : t("enable")}</Button>
          <Button size="sm" icon={<KeyRound className="size-4" />} onClick={() => act(`users/${u.id}/regenerate`)}>{t("regenerate")}</Button>
          <span className="ms-auto" />
          <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => api(`users/${u.id}`, { method: "DELETE" }).then(() => { onChanged(); onClose(); })}>{t("delete")}</ConfirmButton>
        </div>
      }
    >
      <div className="flex flex-col gap-6">
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-px rounded-2xl overflow-hidden border border-hair bg-hair">
          <Fact label={t("status")}><span className="inline-flex items-center gap-1.5" style={{ color: meta.color }}><Dot color={meta.color} />{t(meta.key)}</span></Fact>
          <Fact label={t("usage")}><span className="num">{bytes(u.used, lang)}</span><span className="text-ink3 text-[12px] num"> / {u.dataLimit ? bytes(u.dataLimit, lang) : "∞"}</span></Fact>
          <Fact label={t("expiry")}>{u.daysLeft === null ? t("never") : <span className="num">{n(u.daysLeft)} {t("days")}</span>}<div className="text-[11.5px] text-ink3">{u.expireAt ? date(u.expireAt, lang) : ""}</div></Fact>
          <Fact label={t("lastOnline")}>{u.online ? <span className="text-online">{t("online")}</span> : ago(u.lastOnlineAt, lang)}</Fact>
        </div>
        {u.dataLimit > 0 && <Meter value={u.used} max={u.dataLimit} className="-mt-3" />}

        <div>
          <Label>{t("subLink")}</Label>
          <div className="flex gap-4 items-start">
            <img src={qrURL(u.subUrl)} alt="QR" className="size-28 shrink-0 rounded-xl bg-white p-1.5" />
            <div className="flex-1 min-w-0 flex flex-col gap-2">
              <CopyField value={u.subUrl} />
              <a className="text-[13px] text-ink2 hover:text-ink underline" href={u.subUrl} target="_blank" rel="noreferrer">{lang === "fa" ? "صفحهٔ کاربر" : "User page"} ↗</a>
            </div>
          </div>
        </div>

        <div>
          <Label hint={cfg ? n(cfg.configs.length) : ""}>{t("configs")}</Label>
          {!cfg ? <Spinner /> : (
            <ul className="flex flex-col gap-1.5">
              {cfg.configs.map((c, i) => (
                <li key={i} className="rounded-xl border border-hair2">
                  <div className="flex items-center gap-2 ps-3 pe-1 h-11">
                    <span className="flex-1 min-w-0 truncate text-[13px]" dir="ltr">{c.name}</span>
                    <IconButton label="QR" onClick={() => setOpenQr(openQr === i ? null : i)}><QrCode className="size-4" /></IconButton>
                    <IconButton label={t("copy")} onClick={() => copyText(c.link).then(() => toast(t("copied")))}><Copy className="size-4" /></IconButton>
                  </div>
                  {openQr === i && <div className="px-3 pb-3 flex justify-center"><img src={qrURL(c.link)} alt="QR" className="w-56 h-56 rounded-xl bg-white p-2" /></div>}
                </li>
              ))}
              {cfg.configs.length === 0 && <li className="text-ink3 text-sm">—</li>}
            </ul>
          )}
        </div>

        <div>
          <Label>{t("usage30")}</Label>
          <UsageBars series={series} />
        </div>

        <div className="grid grid-cols-2 gap-3 text-[12.5px] text-ink2">
          <div>{t("created")}: <span className="text-ink">{date(u.createdAt, lang)}</span></div>
          <div>{t("total")}: <span className="text-ink num">{bytes(u.lifetimeUsed, lang)}</span></div>
          <div>{t("resetPeriod")}: <span className="text-ink">{t(u.resetPeriod === "none" ? "none" : u.resetPeriod)}</span></div>
          <div className="truncate">UUID: <bdi dir="ltr" className="text-ink font-mono text-[11.5px]">{u.uuid}</bdi></div>
        </div>
      </div>
    </Sheet>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="bg-panel px-3.5 py-3 min-w-0">
      <div className="text-[12px] text-ink3 mb-0.5">{label}</div>
      <div className="text-[14px] font-medium">{children}</div>
    </div>
  );
}
