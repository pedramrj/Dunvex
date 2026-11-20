import { useCallback, useEffect, useRef, useState } from "react";
import {
  Bot, Cpu, Download, Globe, HardDriveDownload, KeyRound, LayoutPanelLeft, Link2, Plus, RefreshCw, ScrollText, Send, ShieldCheck, ShieldOff, Trash2, Upload,
  UserCog, Users as UsersIcon,
} from "lucide-react";
import { api, BASE, VERSION, type Admin, type CoreStatus, type Role } from "../lib/api";
import { GB, bytes, cx, date, duration } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import {
  Avatar, Badge, Button, ConfirmButton, Dot, Empty, IconButton, Input, Label, Panel, Segmented, Select, Sheet, Skeleton, Tabs, Toggle, useToast,
} from "../components/ui";
import { PageHead, can, roleColor, roleKey, useSession } from "../App";

type S = Record<string, string>;
type Info = { publicIp: string; effectiveHost: string; selfSigned: boolean; certExpiry?: number; telegram?: { running: boolean; configured: boolean; username: string; error: string; admins: number } };
type TabId = "server" | "sub" | "panel" | "security" | "admins" | "telegram" | "cores" | "backup";

const tabs: { id: TabId; key: Key; icon: typeof Globe; need: Role }[] = [
  { id: "server", key: "serverAddress", icon: Globe, need: "owner" },
  { id: "sub", key: "subscription", icon: Link2, need: "owner" },
  { id: "panel", key: "panel", icon: LayoutPanelLeft, need: "owner" },
  { id: "security", key: "security", icon: ShieldCheck, need: "reseller" },
  { id: "admins", key: "admins", icon: UsersIcon, need: "owner" },
  { id: "telegram", key: "telegram", icon: Bot, need: "owner" },
  { id: "cores", key: "cores", icon: Cpu, need: "admin" },
  { id: "backup", key: "backup", icon: HardDriveDownload, need: "owner" },
];

export function SettingsPage() {
  const { t } = useI18n();
  const { me } = useSession();
  const role = me.admin.role;
  const mine = tabs.filter((x) => can(role, x.need));
  const [tab, setTab] = useState<TabId>(mine[0].id);
  const owner = role === "owner";

  return (
    <div>
      <PageHead title={t("settings")} />
      <div className="lg:grid lg:grid-cols-[230px_1fr] gap-6 items-start">
        <nav className="hidden lg:flex flex-col gap-1 glass rounded-[20px] p-2 sticky top-6">
          {mine.map(({ id, key, icon: Icon }) => (
            <button key={id} onClick={() => setTab(id)}
              className={cx("flex items-center gap-3 h-11 px-3.5 rounded-xl text-[13.5px] text-start transition-all", tab === id ? "bg-white/[.08] text-white font-semibold" : "text-ink2 hover:text-ink hover:bg-white/[.04]")}>
              <Icon className={cx("size-[18px]", tab === id ? "text-[#7cc0ff]" : "text-ink3")} />
              {t(key)}
            </button>
          ))}
        </nav>
        <Tabs className="lg:hidden mb-5" value={tab} onChange={setTab} items={mine.map((x) => ({ id: x.id, label: t(x.key) }))} />
        <div className="min-w-0 max-w-4xl flex flex-col gap-4 rise" key={tab}>
          {owner && ["server", "sub", "panel", "telegram"].includes(tab) ? <OwnerSettings tab={tab} /> : null}
          {tab === "security" && <SecurityTab />}
          {tab === "admins" && <AdminsTab />}
          {tab === "cores" && <CoresPanel />}
          {tab === "backup" && <BackupPanel />}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- owner settings

function OwnerSettings({ tab }: { tab: TabId }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<S | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => api<{ settings: S; info: Info }>("settings").then((r) => { setS(r.settings); setInfo(r.info); }), []);
  useEffect(() => { load(); }, [load]);
  if (!s || !info) return <Skeleton className="h-64" />;
  const set = (k: string, v: string) => setS({ ...s, [k]: v });

  const save = (keys: string[]) => {
    setBusy(true);
    const body: S = {};
    keys.forEach((k) => (body[k] = s[k] ?? ""));
    return api<{ restarting?: boolean }>("settings", { method: "PUT", body })
      .then((r) => {
        toast(r.restarting ? t("restartPanel") : t("saved"));
        if (body.panel_path || body.panel_port) setTimeout(() => {
          const url = new URL(window.location.href);
          if (body.panel_tls) url.protocol = body.panel_tls === "true" ? "https:" : "http:";
          if (body.panel_port) url.port = body.panel_port;
          url.pathname = body.panel_path || url.pathname;
          url.hash = "#/settings";
          window.location.href = url.toString();
        }, r.restarting ? 3000 : 600);
        load();
      })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  const SaveBar = ({ keys }: { keys: string[] }) => (
    <div className="flex justify-end pt-5"><Button variant="primary" loading={busy} onClick={() => save(keys)}>{t("save")}</Button></div>
  );

  if (tab === "server")
    return (
      <>
        <Panel icon={<Globe className="size-4" />} title={t("serverAddress")} sub={<span className="mono" dir="ltr">{info.publicIp}</span>}>
          <div className="grid sm:grid-cols-2 gap-4">
            <label><Label>{t("domain")}</Label><Input dir="ltr" value={s.domain ?? ""} placeholder="vpn.example.com" onChange={(e) => set("domain", e.target.value)} /></label>
            <label><Label hint={<span className="mono">{info.effectiveHost}</span>}>{t("host")}</Label><Input dir="ltr" value={s.host ?? ""} placeholder={info.publicIp} onChange={(e) => set("host", e.target.value)} /></label>
          </div>
          <div className="mt-5 flex flex-col gap-4">
            <Toggle checked={s.block_torrent !== "false"} onChange={(v) => set("block_torrent", String(v))} label={t("blockTorrent")} />
            <label className="max-w-xs"><Label>{t("pauseMinutes")}</Label><Input type="number" min={1} className="num" value={s.ip_pause_minutes ?? "10"} onChange={(e) => set("ip_pause_minutes", e.target.value)} /></label>
          </div>
          <SaveBar keys={["domain", "host", "block_torrent", "ip_pause_minutes"]} />
        </Panel>
        <Panel icon={<ShieldCheck className="size-4" />} title={t("certificate")}
          action={<Badge color={info.selfSigned ? "var(--color-ink2)" : "var(--color-online)"}>{info.selfSigned ? (lang === "fa" ? "خودامضا" : "Self-signed") : "TLS ✓"}</Badge>}>
          <p className="text-[13.5px] text-ink2 mb-4">
            {info.selfSigned ? (lang === "fa" ? "کلاینت‌ها با پین گواهی وصل می‌شوند." : "Clients pin the certificate.") : "Let's Encrypt / custom"}
            {info.certExpiry ? <> · <span className="num">{date(info.certExpiry, lang)}</span></> : null}
          </p>
          <div className="grid sm:grid-cols-2 gap-4">
            <label><Label>Email (ACME)</Label><Input dir="ltr" value={s.acme_email ?? ""} onChange={(e) => set("acme_email", e.target.value)} /></label>
            <label><Label>{lang === "fa" ? "نوع گواهی" : "Certificate type"}</Label><Select value={s.cert_mode || "self"} onChange={(v) => set("cert_mode", v)} options={[{ value: "self", label: lang === "fa" ? "خودامضا" : "Self-signed" }, { value: "acme", label: "Let's Encrypt" }, { value: "custom", label: lang === "fa" ? "فایل دلخواه" : "Custom files" }]} /></label>
          </div>
          {s.cert_mode === "custom" && (
            <div className="grid sm:grid-cols-2 gap-4 mt-4">
              <label><Label>Certificate (.pem)</Label><Input dir="ltr" value={s.cert_file ?? ""} placeholder="/root/cert/fullchain.pem" onChange={(e) => set("cert_file", e.target.value)} /></label>
              <label><Label>Key (.pem)</Label><Input dir="ltr" value={s.key_file ?? ""} placeholder="/root/cert/privkey.pem" onChange={(e) => set("key_file", e.target.value)} /></label>
            </div>
          )}
          <div className="flex flex-wrap justify-end gap-2 pt-5">
            <Button onClick={() => save(["acme_email", "cert_mode", "cert_file", "key_file"])}>{t("save")}</Button>
            <Button variant="primary" disabled={!s.domain} loading={busy} onClick={() => {
              setBusy(true);
              api("settings", { method: "PUT", body: { acme_email: s.acme_email ?? "" } })
                .then(() => api("settings/cert", { method: "POST", body: {} }))
                .then(() => { toast(t("saved")); load(); })
                .catch((e) => toast(e.message, "error"))
                .finally(() => setBusy(false));
            }}>{t("getCert")}</Button>
          </div>
        </Panel>
      </>
    );

  if (tab === "sub")
    return (
      <Panel icon={<Link2 className="size-4" />} title={t("subscription")}>
        <div className="grid sm:grid-cols-2 gap-4">
          <label><Label>{t("subTitle")}</Label><Input value={s.sub_title ?? ""} onChange={(e) => set("sub_title", e.target.value)} /></label>
          <label><Label>{t("updateHours")}</Label><Input type="number" className="num" value={s.sub_update_hours ?? "12"} onChange={(e) => set("sub_update_hours", e.target.value)} /></label>
          <label className="sm:col-span-2"><Label>{t("supportUrl")}</Label><Input dir="ltr" value={s.support_url ?? ""} placeholder="https://t.me/yourchannel" onChange={(e) => set("support_url", e.target.value)} /></label>
          <label className="sm:col-span-2"><Label>{t("announce")}</Label><Input value={s.announce ?? ""} onChange={(e) => set("announce", e.target.value)} /></label>
          <label className="sm:col-span-2"><Label>Base URL</Label><Input dir="ltr" value={s.sub_base_url ?? ""} placeholder="https://sub.example.com:2096" onChange={(e) => set("sub_base_url", e.target.value)} /></label>
        </div>
        <div className="mt-5"><Toggle checked={s.sub_info_entry !== "false"} onChange={(v) => set("sub_info_entry", String(v))} label={t("infoEntry")} /></div>
        <SaveBar keys={["sub_title", "sub_update_hours", "support_url", "announce", "sub_base_url", "sub_info_entry"]} />
      </Panel>
    );

  if (tab === "panel")
    return (
      <Panel icon={<LayoutPanelLeft className="size-4" />} title={t("panel")}>
        <div className="grid sm:grid-cols-3 gap-4">
          <label><Label>{t("panelPort")}</Label><Input type="number" className="num" value={s.panel_port ?? ""} onChange={(e) => set("panel_port", e.target.value)} /></label>
          <label><Label>{t("panelPath")}</Label><Input dir="ltr" className="mono" value={s.panel_path ?? ""} onChange={(e) => set("panel_path", e.target.value)} /></label>
          <label><Label>{t("subPath")}</Label><Input dir="ltr" className="mono" value={s.sub_path ?? ""} onChange={(e) => set("sub_path", e.target.value)} /></label>
        </div>
        <div className="mt-5"><Toggle checked={s.panel_tls === "true"} onChange={(v) => set("panel_tls", String(v))} label={lang === "fa" ? "پنل روی HTTPS (با همان گواهی TLS)" : "Serve the panel over HTTPS (same TLS certificate)"} /></div>
        <SaveBar keys={["panel_port", "panel_path", "sub_path", "panel_tls"]} />
      </Panel>
    );

  // telegram
  const tg = info.telegram;
  return (
    <Panel icon={<Bot className="size-4" />} title={t("telegram")} sub={t("botFeatures")}
      action={tg?.configured ? <Badge color={tg.running ? "var(--color-online)" : "var(--color-bad)"}><Dot color={tg.running ? "var(--color-online)" : "var(--color-bad)"} size={6} pulse={tg.running} />{tg.running ? (tg.username ? "@" + tg.username : t("botRunning")) : t("botStopped")}</Badge> : undefined}>
      {tg?.error && <div className="mb-4 rounded-xl border border-bad/40 bg-bad/[.07] px-3 py-2 text-[12.5px] mono" dir="ltr">{tg.error}</div>}
      <div className="grid sm:grid-cols-2 gap-4">
        <label className="sm:col-span-2"><Label hint={t("botTokenHint")}>{t("botToken")}</Label><Input dir="ltr" className="mono text-[12.5px]" value={s.tg_token ?? ""} placeholder="123456789:AA…" onChange={(e) => set("tg_token", e.target.value)} autoComplete="off" /></label>
        <label className="sm:col-span-2"><Label>{t("adminIds")}</Label><Input dir="ltr" className="mono" value={s.tg_admins ?? ""} placeholder="123456789, 987654321" onChange={(e) => set("tg_admins", e.target.value)} />
          <span className="block text-[12px] text-ink3 mt-1.5">{t("adminIdsHint")}</span></label>
        <label><Label>{t("botLang")}</Label><Select value={s.tg_lang || "fa"} onChange={(v) => set("tg_lang", v)} options={[{ value: "fa", label: "فارسی" }, { value: "en", label: "English" }]} /></label>
        <label><Label>{t("dailyHour")}</Label><Input type="number" min={0} max={23} className="num" value={s.tg_daily_hour ?? "9"} onChange={(e) => set("tg_daily_hour", e.target.value)} /></label>
        <label className="sm:col-span-2"><Label>{t("apiBase")}</Label><Input dir="ltr" className="mono text-[12.5px]" value={s.tg_api ?? ""} placeholder="https://api.telegram.org" onChange={(e) => set("tg_api", e.target.value)} /></label>
      </div>
      <div className="mt-5 flex flex-col gap-3">
        <Toggle checked={s.tg_backup !== "false"} onChange={(v) => set("tg_backup", String(v))} label={t("sendBackup")} />
        <Toggle checked={s.tg_user_bot !== "false"} onChange={(v) => set("tg_user_bot", String(v))} label={t("userBot")} />
      </div>
      <div className="flex flex-wrap justify-end gap-2 pt-5">
        <Button icon={<Send className="size-4" />} onClick={() => save(["tg_token", "tg_admins", "tg_lang", "tg_daily_hour", "tg_api", "tg_backup", "tg_user_bot"]).then(() =>
          api("settings/telegram/test", { method: "POST", body: {} }).then(() => toast("✓ Telegram")).catch((e) => toast(e.message, "error")))}>{t("testBot")}</Button>
        <Button variant="primary" loading={busy} onClick={() => save(["tg_token", "tg_admins", "tg_lang", "tg_daily_hour", "tg_api", "tg_backup", "tg_user_bot"])}>{t("save")}</Button>
      </div>
    </Panel>
  );
}

// ---------------------------------------------------------------- security: account + two-step login

function SecurityTab() {
  const { t } = useI18n();
  const toast = useToast();
  const { me, reload } = useSession();
  const [f, setF] = useState({ username: "", currentPassword: "", newPassword: "" });
  const [busy, setBusy] = useState(false);
  const [setup, setSetup] = useState<{ secret: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [off, setOff] = useState({ password: "", code: "" });
  const on = me.admin.totp;
  return (
    <>
      <Panel icon={<UserCog className="size-4" />} title={t("account")} sub={<span dir="ltr">{me.admin.username}</span>}>
        <div className="grid sm:grid-cols-3 gap-4">
          <label><Label>{t("username")}</Label><Input dir="ltr" value={f.username} placeholder={me.admin.username} onChange={(e) => setF({ ...f, username: e.target.value })} autoComplete="username" /></label>
          <label><Label>{t("currentPassword")}</Label><Input type="password" dir="ltr" value={f.currentPassword} onChange={(e) => setF({ ...f, currentPassword: e.target.value })} autoComplete="current-password" /></label>
          <label><Label>{t("newPassword")}</Label><Input type="password" dir="ltr" value={f.newPassword} onChange={(e) => setF({ ...f, newPassword: e.target.value })} autoComplete="new-password" /></label>
        </div>
        <div className="flex justify-end pt-5">
          <Button variant="primary" loading={busy} disabled={!f.currentPassword} onClick={() => {
            setBusy(true);
            api("account", { method: "PUT", body: f }).then(() => { toast(t("saved")); setF({ username: "", currentPassword: "", newPassword: "" }); reload(); })
              .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
          }}>{t("save")}</Button>
        </div>
      </Panel>

      <Panel icon={<ShieldCheck className="size-4" />} title={t("twoFactor")} sub={t("twoFactorHint")}
        action={<Badge color={on ? "var(--color-online)" : "var(--color-ink3)"}>{on ? t("on") : t("off")}</Badge>}>
        {!on && !setup && (
          <Button variant="primary" icon={<ShieldCheck className="size-4" />} onClick={() => api<{ secret: string; qr: string }>("account/2fa/setup", { method: "POST", body: {} }).then(setSetup).catch((e) => toast(e.message, "error"))}>{t("enable2fa")}</Button>
        )}
        {!on && setup && (
          <div className="grid sm:grid-cols-[auto_1fr] gap-5 items-start">
            <img src={"data:image/svg+xml;charset=utf-8," + encodeURIComponent(setup.qr)} alt="QR" className="rounded-2xl bg-white p-2.5" width={176} height={176} />
            <div className="flex flex-col gap-3 min-w-0">
              <p className="text-[13.5px] text-ink2">{t("scanQr")}</p>
              <bdi dir="ltr" className="mono text-[12px] text-ink3 break-all select-all">{setup.secret}</bdi>
              <div className="flex gap-2">
                <Input value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" dir="ltr" placeholder="123456" className="mono text-center tracking-[.3em] max-w-44" aria-label={t("code")} />
                <Button variant="primary" disabled={code.length !== 6} onClick={() => api("account/2fa/enable", { method: "POST", body: { code } }).then(() => { toast(t("saved")); setSetup(null); setCode(""); reload(); }).catch((e) => toast(e.message, "error"))}>{t("enable2fa")}</Button>
              </div>
            </div>
          </div>
        )}
        {on && (
          <div className="grid sm:grid-cols-[1fr_1fr_auto] gap-3 items-end">
            <label><Label>{t("currentPassword")}</Label><Input type="password" dir="ltr" value={off.password} onChange={(e) => setOff({ ...off, password: e.target.value })} /></label>
            <label><Label>{t("code")}</Label><Input dir="ltr" inputMode="numeric" className="mono" value={off.code} onChange={(e) => setOff({ ...off, code: e.target.value.replace(/\D/g, "").slice(0, 6) })} /></label>
            <Button variant="danger" icon={<ShieldOff className="size-4" />} disabled={!off.password} onClick={() => api("account/2fa/disable", { method: "POST", body: off }).then(() => { toast(t("saved")); setOff({ password: "", code: "" }); reload(); }).catch((e) => toast(e.message, "error"))}>{t("disable2fa")}</Button>
          </div>
        )}
      </Panel>
    </>
  );
}

// ---------------------------------------------------------------- admins

function AdminsTab() {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const { me } = useSession();
  const [list, setList] = useState<Admin[] | null>(null);
  const [edit, setEdit] = useState<Admin | "new" | null>(null);
  const load = useCallback(() => api<Admin[]>("admins").then(setList).catch((e) => toast(e.message, "error")), [toast]);
  useEffect(() => { load(); }, [load]);
  return (
    <Panel icon={<UsersIcon className="size-4" />} title={t("admins")} sub={list ? n(list.length) : ""}
      action={<Button size="sm" variant="soft" icon={<Plus className="size-4" />} onClick={() => setEdit("new")}>{t("newAdmin")}</Button>}>
      {!list ? <Skeleton className="h-24" /> : list.length === 0 ? <Empty icon={<UsersIcon className="size-7" />} title="—" /> : (
        <ul className="flex flex-col gap-2">
          {list.map((a) => (
            <li key={a.id} className={cx("flex flex-wrap items-center gap-3 rounded-2xl bg-white/[.03] border border-white/[.06] p-3", !a.enabled && "opacity-60")}>
              <Avatar name={a.username} size={40} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 font-semibold">
                  <span dir="ltr" className="truncate">{a.username}</span>
                  {a.id === me.admin.id && <span className="text-[11px] text-ink3">({lang === "fa" ? "شما" : "you"})</span>}
                  {a.totp && <ShieldCheck className="size-4 text-online" aria-label={t("twoFactor")} />}
                </div>
                <div className="text-[12px] text-ink3 num">
                  {n(a.userCount ?? 0)} {t("users2")}
                  {a.role === "reseller" && <> · {bytes(a.allocated ?? 0, lang)} / {a.maxData ? bytes(a.maxData, lang) : "∞"}</>}
                </div>
              </div>
              <Badge color={roleColor[a.role]}>{t(roleKey[a.role])}</Badge>
              <IconButton label={t("edit")} onClick={() => setEdit(a)}><UserCog className="size-4" /></IconButton>
            </li>
          ))}
        </ul>
      )}
      {edit && <AdminSheet admin={edit === "new" ? undefined : edit} onClose={() => setEdit(null)} onDone={() => { setEdit(null); load(); }} />}
    </Panel>
  );
}

function AdminSheet({ admin, onClose, onDone }: { admin?: Admin; onClose: () => void; onDone: () => void }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const { me } = useSession();
  const [f, setF] = useState({
    username: admin?.username ?? "", password: "", role: (admin?.role ?? "reseller") as Role,
    maxUsers: admin?.maxUsers ?? 50, maxGB: admin ? Math.round((admin.maxData ?? 0) / GB) : 500, enabled: admin?.enabled ?? true, resetTotp: false,
  });
  const [busy, setBusy] = useState(false);
  const submit = () => {
    setBusy(true);
    const body: Record<string, unknown> = { username: f.username, role: f.role, maxUsers: f.maxUsers, maxData: f.maxGB * GB, enabled: f.enabled, resetTotp: f.resetTotp };
    if (f.password) body.password = f.password;
    (admin ? api(`admins/${admin.id}`, { method: "PUT", body }) : api("admins", { body }))
      .then(() => { toast(t("saved")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  const roles: { value: Role; label: string; hint: Key }[] = [
    { value: "owner", label: t("owner"), hint: "ownerHint" },
    { value: "admin", label: t("adminRole"), hint: "adminHint" },
    { value: "reseller", label: t("reseller"), hint: "resellerHint" },
  ];
  return (
    <Sheet open onClose={onClose} title={admin ? t("edit") : t("newAdmin")} sub={admin ? <span dir="ltr">{admin.username}</span> : undefined}
      footer={<>
        {admin && admin.id !== me.admin.id && <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => api(`admins/${admin.id}`, { method: "DELETE" }).then(onDone).catch((e) => toast(e.message, "error"))}>{t("delete")}</ConfirmButton>}
        <span className="me-auto" />
        <Button variant="quiet" onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" loading={busy} onClick={submit}>{admin ? t("save") : t("create")}</Button>
      </>}>
      <div className="flex flex-col gap-5">
        <div className="grid grid-cols-2 gap-3">
          <label><Label>{t("username")}</Label><Input dir="ltr" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} autoComplete="off" /></label>
          <label><Label hint={admin ? "…" : undefined}>{t(admin ? "newPassword" : "password")}</Label><Input type="password" dir="ltr" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} autoComplete="new-password" /></label>
        </div>
        <div>
          <Label>{t("role")}</Label>
          <div className="grid gap-2">
            {roles.map((r) => (
              <button key={r.value} type="button" onClick={() => setF({ ...f, role: r.value })}
                className={cx("flex items-center gap-3 text-start rounded-2xl border px-4 py-3 transition-all", f.role === r.value ? "border-cdn/60 bg-cdn/[.1] shadow-[0_0_0_3px_rgba(47,140,255,.12)]" : "border-white/10 bg-white/[.02] hover:border-white/20")}>
                <span className="size-3 rounded-full" style={{ background: roleColor[r.value] }} />
                <span className="flex-1">
                  <span className="block font-semibold text-[14px]">{r.label}</span>
                  <span className="block text-[12.5px] text-ink3">{t(r.hint)}</span>
                </span>
              </button>
            ))}
          </div>
        </div>
        {f.role === "reseller" && (
          <div className="grid grid-cols-2 gap-3">
            <label><Label hint={t("zeroUnlimited")}>{t("maxUsers")}</Label><Input type="number" min={0} className="num" value={f.maxUsers} onChange={(e) => setF({ ...f, maxUsers: Math.max(0, +e.target.value || 0) })} /></label>
            <label><Label hint={`${n(f.maxGB)} ${t("gb")}`}>{t("maxData")}</Label><Input type="number" min={0} className="num" value={f.maxGB} onChange={(e) => setF({ ...f, maxGB: Math.max(0, +e.target.value || 0) })} /></label>
          </div>
        )}
        <Toggle checked={f.enabled} onChange={(v) => setF({ ...f, enabled: v })} label={t("enable")} />
        {admin?.totp && <Toggle checked={f.resetTotp} onChange={(v) => setF({ ...f, resetTotp: v })} label={`${t("twoFactor")} · ${t("disable2fa")}`} />}
      </div>
    </Sheet>
  );
}

// ---------------------------------------------------------------- cores

function CoresPanel() {
  const { t, lang } = useI18n();
  const toast = useToast();
  const { me } = useSession();
  const owner = me.admin.role === "owner";
  const [d, setD] = useState<{ cores: CoreStatus[]; errors: Record<string, string>; stats: Record<string, boolean> } | null>(null);
  const [upd, setUpd] = useState<{ current: string; latest: string; xray: string; singbox: string } | null>(null);
  const [busy, setBusy] = useState("");
  const [logs, setLogs] = useState<{ name: string; lines: string[] } | null>(null);
  const load = useCallback(() => api<NonNullable<typeof d>>("cores").then(setD), []);
  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, [load]);
  if (!d) return <Skeleton className="h-48" />;
  const install = (name: string) => {
    setBusy(name);
    api<{ version: string }>(`cores/${name}/install`, { method: "POST", body: {} })
      .then((r) => { toast(`${name} ${r.version}`); load(); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  return (
    <>
      <Panel icon={<Cpu className="size-4" />} title={t("cores")} action={<Button size="sm" icon={<RefreshCw className="size-4" />} loading={busy === "apply"} onClick={() => { setBusy("apply"); api("cores/apply", { method: "POST", body: {} }).then(() => { toast(t("saved")); load(); }).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("restart")}</Button>}>
        <ul className="flex flex-col gap-2">
          {d.cores.map((c) => (
            <li key={c.name} className="rounded-2xl bg-white/[.03] border border-white/[.06] p-3.5 flex flex-wrap items-center gap-3">
              <Dot color={c.running ? "var(--color-online)" : c.installed ? "var(--color-ink3)" : "var(--color-bad)"} pulse={c.running} size={9} />
              <div className="min-w-0">
                <div className="font-semibold" dir="ltr">{c.name} <span className="text-ink3 font-normal mono text-[12px]">{c.version}</span></div>
                <div className="text-[12.5px] text-ink3">
                  {c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.installed ? t("stopped") : t("notInstalled")}
                  {c.name === "sing-box" && c.installed && <> · {t("perUserStats")}: {d.stats["sing-box"] ? "✓" : "✕"}</>}
                </div>
                {(d.errors[c.name] || c.error) && <div className="text-[12px] text-[#ffc2cc] mt-1 break-all mono" dir="ltr">{d.errors[c.name] || c.error}</div>}
              </div>
              <div className="ms-auto flex gap-2">
                <Button size="sm" variant="quiet" icon={<ScrollText className="size-4" />} onClick={() => api<string[]>(`cores/${c.name}/logs`).then((lines) => setLogs({ name: c.name, lines }))}>{t("logs")}</Button>
                {owner && <Button size="sm" loading={busy === c.name} onClick={() => install(c.name)}>{c.installed ? t("update") : t("install")}</Button>}
              </div>
            </li>
          ))}
        </ul>
      </Panel>
      {owner && (
        <Panel icon={<KeyRound className="size-4" />} title={t("version")} action={<Button size="sm" onClick={() => api<NonNullable<typeof upd>>("update").then(setUpd).catch((e) => toast(e.message, "error"))}>{t("checkUpdate")}</Button>}>
          <div className="text-[13.5px] grid grid-cols-2 gap-y-2 num" dir="ltr">
            <span className="text-ink2">Dunvex</span><span className="mono">{VERSION}{upd && upd.latest && upd.latest !== VERSION ? ` → ${upd.latest}` : ""}</span>
            {upd && <><span className="text-ink2">Xray (latest)</span><span className="mono">{upd.xray}</span><span className="text-ink2">sing-box (latest)</span><span className="mono">{upd.singbox}</span></>}
          </div>
          {upd && upd.latest && upd.latest !== VERSION && (
            <div className="flex justify-end pt-4"><Button variant="primary" loading={busy === "self"} onClick={() => { setBusy("self"); api("update", { method: "POST", body: {} }).then(() => toast(t("restartPanel"))).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("update")}</Button></div>
          )}
        </Panel>
      )}
      <Sheet open={!!logs} onClose={() => setLogs(null)} wide title={`${logs?.name} · ${t("logs")}`}>
        <pre dir="ltr" className="text-[11.5px] leading-relaxed text-ink2 whitespace-pre-wrap break-all mono">{logs?.lines.join("\n") || "—"}</pre>
      </Sheet>
    </>
  );
}

function BackupPanel() {
  const { t } = useI18n();
  const toast = useToast();
  const file = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  return (
    <Panel icon={<HardDriveDownload className="size-4" />} title={t("backup")}>
      <div className="flex flex-wrap gap-3">
        <a href={BASE + "backup"} download><Button variant="primary" icon={<Download className="size-4" />}>{t("downloadBackup")}</Button></a>
        <Button icon={<Upload className="size-4" />} loading={busy} onClick={() => file.current?.click()}>{t("restore")}</Button>
        <input ref={file} type="file" accept=".db" className="hidden" onChange={(e) => {
          const f = e.target.files?.[0];
          if (!f) return;
          const form = new FormData();
          form.append("file", f);
          setBusy(true);
          api("restore", { form }).then(() => toast(t("restartPanel"))).catch((err) => toast(err.message, "error")).finally(() => setBusy(false));
        }} />
      </div>
    </Panel>
  );
}

export const _segmented = Segmented;
