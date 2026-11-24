import { useCallback, useEffect, useState } from "react";
import {
  ArrowDownUp, Bot, EyeOff, Layers, Palette, Sparkles, Cpu, Globe, HardDriveDownload, KeyRound, LayoutPanelLeft, Link2, Plus, RefreshCw, Scissors, ScrollText, Send, ShieldCheck, ShieldOff, Trash2,
  UserCog, Users as UsersIcon, Waves, Webhook,
} from "lucide-react";
import { api, VERSION, type Admin, type CoreStatus, type Role } from "../lib/api";
import { GB, bytes, cx, date, duration } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import {
  Avatar, Badge, Button, ConfirmButton, Dot, Empty, IconButton, Input, Label, Panel, Segmented, Select, Sheet, Skeleton, Tabs, Toggle, useToast,
} from "../components/ui";
import { PageHead, can, navigate, roleColor, roleKey, useSession } from "../App";
import { allWidgets, usePrefs, type Widget } from "../lib/prefs";
import { BackupsTab } from "../components/Backups";
import { TrialPanel } from "../components/Trial";
import { SupportSettingsPanel } from "../components/SupportSettings";
import { Logo } from "../components/Logo";
import { IntegrationsTab } from "./Integrations";

type S = Record<string, string>;
type Tricks = {
  brutal?: { ready: boolean; why: string };
  ech?: { publicName: string };
  echCdn?: { domain: string; on: boolean; publicName?: string; source?: string };
};
type Info = {
  publicIp: string; effectiveHost: string; selfSigned: boolean; certExpiry?: number;
  telegram?: { running: boolean; configured: boolean; username: string; error: string; admins: number };
  tricks?: Tricks;
};
type TabId = "appearance" | "server" | "sub" | "brand" | "panel" | "security" | "admins" | "telegram" | "integrations" | "cores" | "backup";

const tabs: { id: TabId; key: Key; icon: typeof Globe; need: Role }[] = [
  { id: "appearance", key: "appearance", icon: Palette, need: "reseller" },
  { id: "server", key: "serverAddress", icon: Globe, need: "owner" },
  { id: "sub", key: "subscription", icon: Link2, need: "owner" },
  { id: "brand", key: "brandStudio", icon: Sparkles, need: "owner" },
  { id: "panel", key: "panel", icon: LayoutPanelLeft, need: "owner" },
  { id: "security", key: "security", icon: ShieldCheck, need: "reseller" },
  { id: "admins", key: "admins", icon: UsersIcon, need: "owner" },
  { id: "telegram", key: "telegram", icon: Bot, need: "owner" },
  { id: "integrations", key: "integrations", icon: Webhook, need: "owner" },
  { id: "cores", key: "cores", icon: Cpu, need: "admin" },
  { id: "backup", key: "backup", icon: HardDriveDownload, need: "owner" },
];

/** The translation key of a settings tab, for the breadcrumb. */
export const settingsTabKey = (id?: string): Key | undefined => tabs.find((x) => x.id === id)?.key;

export function SettingsPage({ arg }: { arg?: string }) {
  const { t } = useI18n();
  const { me } = useSession();
  const role = me.admin.role;
  const mine = tabs.filter((x) => can(role, x.need));
  const [tab, setTab] = useState<TabId>(mine.some((x) => x.id === arg) ? (arg as TabId) : mine[0].id);
  const owner = role === "owner";

  return (
    <div>
      <PageHead title={t("settings")} />
      <div className="lg:grid lg:grid-cols-[230px_1fr] gap-6 items-start">
        <nav className="hidden lg:flex flex-col gap-1 glass rounded-[20px] p-2 sticky top-6">
          {mine.map(({ id, key, icon: Icon }) => (
            <button key={id} onClick={() => setTab(id)}
              className={cx("flex items-center gap-3 h-11 px-3.5 rounded-xl text-[13.5px] text-start transition-[background-color,color]", tab === id ? "bg-fg/[.08] text-ink font-semibold" : "text-ink2 hover:text-ink hover:bg-fg/[.04]")}>
              <Icon className={cx("size-[18px]", tab === id ? "text-primary2" : "text-ink3")} />
              {t(key)}
            </button>
          ))}
        </nav>
        <Tabs className="lg:hidden mb-5" value={tab} onChange={setTab} items={mine.map((x) => ({ id: x.id, label: t(x.key) }))} />
        <div className="min-w-0 max-w-4xl flex flex-col gap-4 rise" key={tab}>
          {owner && ["server", "sub", "panel", "telegram"].includes(tab) ? <OwnerSettings tab={tab} /> : null}
          {tab === "appearance" && <AppearanceTab />}
          {tab === "brand" && <BrandTab />}
          {tab === "security" && <SecurityTab />}
          {tab === "admins" && <AdminsTab />}
          {tab === "cores" && <CoresPanel />}
          {tab === "backup" && <BackupsTab />}
          {tab === "integrations" && <IntegrationsTab />}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- owner settings

function OwnerSettings({ tab }: { tab: TabId }) {
  const { t, lang, n } = useI18n();
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
            <div>
              <Label>{t("domain")}</Label>
              <button type="button" onClick={() => navigate("domains")}
                className="w-full h-11 rounded-xl bg-rail border border-hair px-3.5 flex items-center justify-between gap-3 text-start hover:border-hair2 transition-colors">
                {s.domain ? <bdi dir="ltr" className="mono truncate">{s.domain}</bdi> : <span className="text-ink3 truncate">{t("dmNoMain")}</span>}
                <span className="text-[12.5px] text-primary2 shrink-0">{t("domains")}</span>
              </button>
            </div>
            <label><Label hint={<span className="mono">{info.effectiveHost}</span>}>{t("host")}</Label><Input dir="ltr" value={s.host ?? ""} placeholder={info.publicIp} onChange={(e) => set("host", e.target.value)} /></label>
          </div>
          <div className="mt-5 flex flex-col gap-4">
            <label className="max-w-xs"><Label>{t("pauseMinutes")}</Label><Input type="number" min={1} className="num" value={s.ip_pause_minutes ?? "10"} onChange={(e) => set("ip_pause_minutes", e.target.value)} /></label>
            <label className="max-w-md">
              <Label>{t("healthEvery")}</Label>
              <Select className="max-w-xs" value={s.health_hours || "3"} onChange={(v) => set("health_hours", v)}
                options={[{ value: "0", label: t("healthOff") }, ...[1, 2, 3, 6, 12, 24].map((h) => ({ value: String(h), label: t("everyHours").replace("{h}", n(h)) }))]} />
              <span className="block mt-1.5 text-[12px] text-ink3 leading-relaxed">{t("healthEveryHint")}</span>
            </label>
          </div>
          <SaveBar keys={["host", "ip_pause_minutes", "health_hours"]} />
        </Panel>
        <Panel icon={<ShieldCheck className="size-4" />} title={t("dmMovedTitle")}
          action={<Badge color={info.selfSigned ? "var(--color-ink2)" : "var(--color-online)"}>{info.selfSigned ? (lang === "fa" ? "خودامضا" : "Self-signed") : "TLS ✓"}</Badge>}>
          <p className="text-[13.5px] text-ink2 mb-4">
            {t("dmMovedBody")}
            {info.certExpiry ? <> · <span className="num">{date(info.certExpiry, lang)}</span></> : null}
          </p>
          <div className="flex justify-end"><Button variant="primary" icon={<Globe className="size-4" />} onClick={() => navigate("domains")}>{t("dmOpen")}</Button></div>
        </Panel>
      </>
    );

  if (tab === "sub")
    return (
      <>
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
      <Panel icon={<Scissors className="size-4" />} title={t("fragment")} sub={t("fragmentSub")}
        action={<Badge color={s.fragment === "true" ? "var(--color-online)" : "var(--color-ink3)"}>{s.fragment === "true" ? t("on") : t("off")}</Badge>}>
        <p className="text-[13.5px] text-ink2 leading-relaxed max-w-[70ch]">{t("fragmentHelp")}</p>
        <div className="mt-4"><Toggle checked={s.fragment === "true"} onChange={(v) => set("fragment", String(v))} label={t("fragmentOn")} /></div>
        {s.fragment === "true" && (
          <div className="mt-5 grid sm:grid-cols-3 gap-4">
            <label>
              <Label>{t("fragPackets")}</Label>
              <Select value={s.fragment_packets || "tlshello"} onChange={(v) => set("fragment_packets", v)}
                options={[{ value: "tlshello", label: t("fragHello") }, { value: "1-1", label: t("fragFirst") }, { value: "1-3", label: t("fragFirst3") },
                  ...(["tlshello", "1-1", "1-3"].includes(s.fragment_packets || "tlshello") ? [] : [{ value: s.fragment_packets, label: s.fragment_packets }])]} />
            </label>
            <label><Label hint={<span className="mono">bytes</span>}>{t("fragLength")}</Label><Input dir="ltr" className="mono" value={s.fragment_length ?? "100-200"} placeholder="100-200" onChange={(e) => set("fragment_length", e.target.value)} /></label>
            <label><Label hint={<span className="mono">ms</span>}>{t("fragDelay")}</Label><Input dir="ltr" className="mono" value={s.fragment_delay ?? "10-20"} placeholder="10-20" onChange={(e) => set("fragment_delay", e.target.value)} /></label>
          </div>
        )}
        <p className="mt-4 text-[12px] text-ink3 leading-relaxed max-w-[70ch]">{t("fragmentNote")}</p>
        <SaveBar keys={["fragment", "fragment_packets", "fragment_length", "fragment_delay"]} />
      </Panel>
      <TrickPanels s={s} set={set} info={info} SaveBar={SaveBar} />
      </>
    );

  if (tab === "panel")
    return (
      <Panel icon={<LayoutPanelLeft className="size-4" />} title={t("panel")}>
        <div className="grid sm:grid-cols-3 gap-4">
          <label><Label>{t("panelPort")}</Label><Input type="number" className="num" value={s.panel_port ?? ""} onChange={(e) => set("panel_port", e.target.value)} /></label>
          <label><Label>{t("panelPath")}</Label><Input dir="ltr" className="mono" value={s.panel_path ?? ""} onChange={(e) => set("panel_path", e.target.value)} /></label>
          <label><Label>{t("subPath")}</Label><Input dir="ltr" className="mono" value={s.sub_path ?? ""} onChange={(e) => set("sub_path", e.target.value)} /></label>
        </div>
        <p className="mt-3 text-[12.5px] text-ink3 leading-relaxed">{t("panelPortShared")}</p>
        <div className="mt-5"><Toggle checked={s.panel_tls === "true"} onChange={(v) => set("panel_tls", String(v))} label={lang === "fa" ? "پنل روی HTTPS (با همان گواهی TLS)" : "Serve the panel over HTTPS (same TLS certificate)"} /></div>
        <SaveBar keys={["panel_port", "panel_path", "sub_path", "panel_tls"]} />
      </Panel>
    );

  // telegram
  const tg = info.telegram;
  return (
    <>
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
    <TrialPanel />
    <SupportSettingsPanel />
    </>
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
            <img src={"data:image/svg+xml;charset=utf-8," + encodeURIComponent(setup.qr)} alt="QR" className="rounded-2xl bg-white p-2.5 sensitive" width={176} height={176} />
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
            <li key={a.id} className={cx("flex flex-wrap items-center gap-3 rounded-2xl bg-fg/[.03] border border-hair p-3", !a.enabled && "opacity-60")}>
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
                className={cx("flex items-center gap-3 text-start rounded-2xl border px-4 py-3 transition-[background-color,border-color,box-shadow]", f.role === r.value ? "border-primary/60 bg-primary/[.1] shadow-[0_0_0_3px_color-mix(in_oklab,var(--primary)_14%,transparent)]" : "border-hair bg-fg/[.02] hover:border-hair2")}>
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
            <li key={c.name} className="rounded-2xl bg-fg/[.03] border border-hair p-3.5 flex flex-wrap items-center gap-3">
              <Dot color={c.running ? "var(--color-online)" : c.installed ? "var(--color-ink3)" : "var(--color-bad)"} pulse={c.running} size={9} />
              <div className="min-w-0">
                <div className="font-semibold" dir="ltr">{c.name} <span className="text-ink3 font-normal mono text-[12px]">{c.version}</span></div>
                <div className="text-[12.5px] text-ink3">
                  {c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.idle ? t("notInUse") : c.installed ? t("stopped") : t("notInstalled")}
                  {c.name === "sing-box" && c.installed && <> · {t("perUserStats")}: {d.stats["sing-box"] ? "✓" : "✕"}</>}
                </div>
                {(d.errors[c.name] || c.error) && <div className="text-[12px] text-bad mt-1 break-all mono" dir="ltr">{d.errors[c.name] || c.error}</div>}
              </div>
              <div className="ms-auto flex gap-2">
                <Button size="sm" variant="quiet" icon={<ScrollText className="size-4" />} onClick={() => api<string[]>(`cores/${c.name}/logs`).then((lines) => setLogs({ name: c.name, lines }))}>{t("logs")}</Button>
                {owner && !c.builtIn && <Button size="sm" loading={busy === c.name} onClick={() => install(c.name)}>{c.installed ? t("update") : t("install")}</Button>}
              </div>
            </li>
          ))}
        </ul>
      </Panel>
      {owner && (
        <Panel icon={<KeyRound className="size-4" />} title={t("version")} action={<Button size="sm" onClick={() => api<NonNullable<typeof upd>>("update").then(setUpd).catch((e) => toast(e.message, "error"))}>{t("checkUpdate")}</Button>}>
          <div className="text-[13.5px] grid grid-cols-2 gap-y-2 num" dir="ltr">
            <span className="text-ink2" translate="no">Dunvex</span><bdi dir="ltr" className="mono">{VERSION}{upd && upd.latest && upd.latest !== VERSION ? ` → ${upd.latest}` : ""}</bdi>
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




// ---------------------------------------------------------------- appearance (this browser)

function AppearanceTab() {
  const { t, n, lang, setLang } = useI18n();
  const { prefs, set } = usePrefs();
  const presets: { id: string; key: Key; widgets: Widget[]; favorites: string[] }[] = [
    { id: "super", key: "presetSuper", widgets: allWidgets, favorites: ["overview", "users", "security"] },
    { id: "noc", key: "presetNoc", widgets: ["live", "health", "configs", "network", "cores", "events", "kpis"], favorites: ["network", "logs", "analytics"] },
    { id: "support", key: "presetSupport", widgets: ["kpis", "top", "events", "usage"], favorites: ["users", "alerts"] },
    { id: "reseller", key: "presetReseller", widgets: ["kpis", "usage", "top"], favorites: ["users"] },
  ];
  const themes = [
    { id: "navy", key: "themeNavy", bg: "#080c13", acc: "#397bff" },
    { id: "oled", key: "themeOled", bg: "#000000", acc: "#24cfd2" },
    { id: "light", key: "themeLight", bg: "#f3f6fb", acc: "#2f6bf0" },
    { id: "system", key: "themeSystem", bg: "linear-gradient(135deg,#080c13 50%,#f3f6fb 50%)", acc: "#397bff" },
  ] as const;
  const fonts = [
    { id: "estedad", name: "Estedad", fam: "DVX Estedad" },
    { id: "vazirmatn", name: "Vazirmatn", fam: "DVX Vazirmatn" },
  ] as const;
  return (
    <>
      <Panel icon={<Palette className="size-4" />} title={t("theme")} sub={t("appearanceSub")}>
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          {themes.map((x) => (
            <button key={x.id} onClick={() => set("theme", x.id)} aria-pressed={prefs.theme === x.id}
              className={cx("rounded-2xl border p-2 text-start transition-[border-color,box-shadow] duration-150", prefs.theme === x.id ? "border-primary shadow-[0_0_0_3px_color-mix(in_oklab,var(--primary)_18%,transparent)]" : "border-hair hover:border-hair2")}>
              <div className="h-16 rounded-xl mb-2 relative overflow-hidden border border-hair" style={{ background: x.bg }}>
                <span className="absolute bottom-2 [inset-inline-start:8px] h-1.5 w-10 rounded-full" style={{ background: x.acc }} />
              </div>
              <span className="text-[13px] font-medium px-1">{t(x.key)}</span>
            </button>
          ))}
        </div>
      </Panel>
      <Panel title={t("density")}>
        <Segmented value={prefs.density} onChange={(v) => set("density", v)} options={[{ value: "compact", label: t("compact") }, { value: "comfortable", label: t("comfortable") }, { value: "spacious", label: t("spacious") }]} />
      </Panel>
      <Panel title={t("persianFont")} sub={t("persianFontSub")}>
        <div className="grid sm:grid-cols-2 gap-3">
          {fonts.map((f) => (
            <button key={f.id} onClick={() => set("font", f.id)} aria-pressed={prefs.font === f.id}
              className={cx("rounded-2xl border p-4 text-start transition-[border-color,box-shadow] duration-150", prefs.font === f.id ? "border-primary shadow-[0_0_0_3px_color-mix(in_oklab,var(--primary)_18%,transparent)]" : "border-hair hover:border-hair2")}>
              <div className="text-[22px] font-bold" style={{ fontFamily: `"${f.fam}"` }} dir="rtl">نمای کلی سرور ۱۲۸۴</div>
              <div className="text-[12px] text-ink3 mt-1" dir="ltr">{f.name}</div>
            </button>
          ))}
        </div>
      </Panel>
      <Panel title={t("motionAndIntro")}>
        <div className="flex flex-col gap-4">
          <Toggle checked={prefs.motion === "reduced"} onChange={(v) => set("motion", v ? "reduced" : "full")} label={t("reduceMotion")} hint={t("reduceMotionHint")} />
          <div>
            <Label>{t("introMode")}</Label>
            <Segmented value={prefs.intro} onChange={(v) => set("intro", v)} options={[{ value: "session", label: t("introSession") }, { value: "always", label: t("introAlways") }, { value: "off", label: t("introOff") }]} />
          </div>
          <Toggle checked={prefs.present} onChange={(v) => set("present", v)} label={t("presentOn")} hint={t("presentHint")} />
          <div>
            <Label>{t("language")}</Label>
            <Segmented value={lang} onChange={setLang} options={[{ value: "fa", label: "فارسی" }, { value: "en", label: "English" }]} />
          </div>
        </div>
      </Panel>
      <Panel title={t("workspacePresets")} sub={t("workspacePresetsSub")}>
        <div className="grid sm:grid-cols-2 gap-3">
          {presets.map((p) => (
            <button key={p.id} onClick={() => { set("widgets", p.widgets); set("favorites", p.favorites); }}
              className="rounded-2xl border border-hair hover:border-hair2 bg-fg/[.02] p-4 text-start transition-colors">
              <div className="font-semibold text-[14px]">{t(p.key)}</div>
              <div className="text-[12px] text-ink3 mt-1">{n(p.widgets.length)} {t("widgets")}</div>
            </button>
          ))}
        </div>
      </Panel>
    </>
  );
}

// ---------------------------------------------------------------- brand studio (subscription portal)

function BrandTab() {
  const { t } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<S | null>(null);
  useEffect(() => { api<{ settings: S }>("settings").then((r) => setS(r.settings)).catch(() => {}); }, []);
  if (!s) return <Skeleton className="h-64" />;
  const accent = s.sub_accent || "#397bff";
  const save = () => api("settings", { method: "PUT", body: { sub_title: s.sub_title ?? "", sub_accent: s.sub_accent ?? "", support_url: s.support_url ?? "", announce: s.announce ?? "" } })
    .then(() => toast(t("saved"))).catch((e) => toast(e.message, "error"));
  return (
    <div className="grid lg:grid-cols-2 gap-[var(--gap)] items-start">
      <Panel icon={<Sparkles className="size-4" />} title={t("brandStudio")} sub={t("brandSub")}>
        <div className="flex flex-col gap-4">
          <label><Label>{t("subTitle")}</Label><Input value={s.sub_title ?? ""} onChange={(e) => setS({ ...s, sub_title: e.target.value })} /></label>
          <label><Label>{t("accentColor")}</Label>
            <div className="flex gap-2">
              <input type="color" value={accent} onChange={(e) => setS({ ...s, sub_accent: e.target.value })} className="h-11 w-14 rounded-xl border border-hair bg-rail p-1" aria-label={t("accentColor")} />
              <Input dir="ltr" className="mono" value={s.sub_accent ?? ""} placeholder="#397bff" onChange={(e) => setS({ ...s, sub_accent: e.target.value })} spellCheck={false} />
            </div>
          </label>
          <label><Label>{t("supportUrl")}</Label><Input dir="ltr" value={s.support_url ?? ""} placeholder="https://t.me/yourchannel" onChange={(e) => setS({ ...s, support_url: e.target.value })} /></label>
          <label><Label>{t("announce")}</Label><Input value={s.announce ?? ""} onChange={(e) => setS({ ...s, announce: e.target.value })} /></label>
          <p className="text-[12px] text-ink3">{t("logoNote")}</p>
          <div className="flex justify-end"><Button variant="primary" onClick={save}>{t("save")}</Button></div>
        </div>
      </Panel>
      <Panel title={t("preview")}>
        <div className="rounded-[22px] p-5 bg-[#080c13] text-[#f4f7ff] border border-[#26344a] max-w-sm mx-auto">
          <div className="flex flex-col items-center gap-1 mb-4"><Logo part="emblem" height={40} tone={false} /><Logo part="wordmark" width={110} tone={false} /></div>
          <div className="rounded-2xl p-4 border border-[#26344a] bg-[#111c30]">
            <div className="flex items-center gap-3">
              <div className="size-16 rounded-full grid place-items-center text-[13px] font-bold" style={{ background: `conic-gradient(${accent} 62%, #ffffff14 0)` }}><div className="size-12 rounded-full bg-[#111c30] grid place-items-center">62%</div></div>
              <div><div className="font-bold" dir="ltr">ali-phone</div><div className="text-[12px] text-[#9aaac2]">{s.sub_title || "Dunvex"}</div></div>
            </div>
            <div className="mt-4 h-10 rounded-xl grid place-items-center text-[13px] font-semibold text-white" style={{ background: accent }}>{t("copy")}</div>
          </div>
        </div>
      </Panel>
    </div>
  );
}

/** ECH, multiplexing with TCP Brutal, WireGuard noise and XHTTP's split: the
 * anti-censorship options next to the fragment (Settings › Subscription). */
function TrickPanels({ s, set, info, SaveBar }: { s: S; set: (k: string, v: string) => void; info: Info; SaveBar: (p: { keys: string[] }) => JSX.Element }) {
  const { t } = useI18n();
  const tr = info.tricks ?? {};
  const on = (k: string) => s[k] === "true";
  const badge = (v: boolean) => <Badge color={v ? "var(--color-online)" : "var(--color-ink3)"}>{v ? t("on") : t("off")}</Badge>;
  const help = (k: Key) => <p className="text-[13.5px] text-ink2 leading-relaxed max-w-[70ch]">{t(k)}</p>;
  const note = (k: Key) => <p className="mt-4 text-[12px] text-ink3 leading-relaxed max-w-[70ch]">{t(k)}</p>;
  const range = (k: string, def: string, label: Key, unit: string) => (
    <label><Label hint={<span className="mono">{unit}</span>}>{t(label)}</Label>
      <Input dir="ltr" className="mono" value={s[k] ?? def} placeholder={def} onChange={(e) => set(k, e.target.value)} /></label>
  );
  const cdn = tr.echCdn;
  const cdnState = !cdn ? null : !cdn.on || !cdn.domain ? t("echCdnOff")
    : cdn.publicName ? t("echCdnFrom").replace("{name}", cdn.source ?? cdn.domain).replace("{pub}", cdn.publicName)
    : t("echCdnNone").replace("{name}", cdn.domain);

  return (
    <>
      <Panel icon={<EyeOff className="size-4" />} title={t("echTitle")} sub={t("echSub")} action={badge(on("ech_cdn") || on("ech_direct"))}>
        {help("echHelp")}
        <div className="mt-4 grid gap-4">
          <Toggle checked={on("ech_cdn")} onChange={(v) => set("ech_cdn", String(v))} label={t("echCdn")}
            hint={<>{t("echCdnHint")}{cdnState && <span className="block mt-1 text-ink2" dir="auto">{cdnState}</span>}</>} />
          <Toggle checked={on("ech_direct")} onChange={(v) => set("ech_direct", String(v))} label={t("echDirect")} hint={t("echDirectHint")} />
        </div>
        {on("ech_direct") && (
          <div className="mt-5 grid sm:grid-cols-2 gap-4">
            <label><Label hint={tr.ech ? <span className="mono" dir="ltr">{tr.ech.publicName}</span> : undefined}>{t("echName")}</Label>
              <Input dir="ltr" className="mono" value={s.ech_name ?? ""} placeholder="www.speedtest.net" onChange={(e) => set("ech_name", e.target.value)} /></label>
          </div>
        )}
        {note("echNote")}
        <SaveBar keys={["ech_cdn", "ech_direct", "ech_name"]} />
      </Panel>

      <Panel icon={<Layers className="size-4" />} title={t("muxTitle")} sub={t("muxSub")} action={badge(on("mux"))}>
        {help("muxHelp")}
        <div className="mt-4"><Toggle checked={on("mux")} onChange={(v) => set("mux", String(v))} label={t("muxOn")} /></div>
        {on("mux") && (
          <>
            <div className="mt-5 grid sm:grid-cols-2 gap-4">
              <label><Label>{t("muxProtocol")}</Label>
                <Select value={s.mux_protocol || "h2mux"} onChange={(v) => set("mux_protocol", v)}
                  options={["h2mux", "smux", "yamux"].map((v) => ({ value: v, label: v }))} /></label>
              <label><Label>{t("muxStreams")}</Label>
                <Input type="number" className="num" value={s.mux_streams ?? "8"} onChange={(e) => set("mux_streams", e.target.value)} /></label>
            </div>
            <div className="mt-4"><Toggle checked={s.mux_padding !== "false"} onChange={(v) => set("mux_padding", String(v))} label={t("muxPadding")} /></div>
            <div className="mt-5 pt-4 border-t border-hair">
              <Toggle checked={on("mux_brutal")} onChange={(v) => set("mux_brutal", String(v))} label={t("brutal")} hint={t("brutalHelp")} />
              {on("mux_brutal") && (
                <>
                  <div className="mt-4 grid sm:grid-cols-2 gap-4">
                    <label><Label hint={<span className="mono">Mbps</span>}>{t("brutalUp")}</Label>
                      <Input type="number" className="num" value={s.mux_up ?? "20"} onChange={(e) => set("mux_up", e.target.value)} /></label>
                    <label><Label hint={<span className="mono">Mbps</span>}>{t("brutalDown")}</Label>
                      <Input type="number" className="num" value={s.mux_down ?? "100"} onChange={(e) => set("mux_down", e.target.value)} /></label>
                  </div>
                  {tr.brutal && (
                    <p className="mt-3 text-[12.5px] leading-relaxed" dir="auto"
                      style={{ color: tr.brutal.ready ? "var(--color-online)" : "var(--color-warn)" }}>
                      {tr.brutal.ready ? t("brutalReady") : tr.brutal.why === "not-linux" ? t("brutalNotLinux") : t("brutalNoModule")}
                      {!tr.brutal.ready && tr.brutal.why !== "not-linux" && (
                        <code className="block mt-1.5 mono text-[12px] text-ink2" dir="ltr">bash &lt;(curl -fsSL https://tcp.hy2.sh/)</code>
                      )}
                    </p>
                  )}
                </>
              )}
            </div>
          </>
        )}
        {note("muxNote")}
        <SaveBar keys={["mux", "mux_protocol", "mux_streams", "mux_padding", "mux_brutal", "mux_up", "mux_down"]} />
      </Panel>

      <Panel icon={<Waves className="size-4" />} title={t("noiseTitle")} sub={t("noiseSub")} action={badge(on("wg_noise"))}>
        {help("noiseHelp")}
        <div className="mt-4"><Toggle checked={on("wg_noise")} onChange={(v) => set("wg_noise", String(v))} label={t("noiseOn")} /></div>
        {on("wg_noise") && (
          <div className="mt-5 grid sm:grid-cols-3 gap-4">
            <label><Label>{t("noiseCount")}</Label>
              <Input type="number" className="num" value={s.wg_noise_count ?? "5"} onChange={(e) => set("wg_noise_count", e.target.value)} /></label>
            {range("wg_noise_size", "50-100", "noiseSize", "bytes")}
            {range("wg_noise_delay", "10-20", "noiseDelay", "ms")}
          </div>
        )}
        {note("noiseNote")}
        <SaveBar keys={["wg_noise", "wg_noise_count", "wg_noise_size", "wg_noise_delay"]} />
      </Panel>

      <Panel icon={<ArrowDownUp className="size-4" />} title={t("splitTitle")} sub={t("splitSub")} action={badge(!!s.xhttp_split)}>
        {help("splitHelp")}
        <div className="mt-4">
          <Segmented value={s.xhttp_split ?? ""} onChange={(v) => set("xhttp_split", v)}
            options={[{ value: "", label: t("splitOff") }, { value: "down", label: t("splitDown") }, { value: "up", label: t("splitUp") }]} />
        </div>
        {note("splitNote")}
        <SaveBar keys={["xhttp_split"]} />
      </Panel>
    </>
  );
}
