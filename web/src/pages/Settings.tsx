import { useCallback, useEffect, useRef, useState } from "react";
import { Download, RefreshCw, ScrollText, ShieldCheck, Upload } from "lucide-react";
import { api, BASE, VERSION, type CoreStatus } from "../lib/api";
import { cx, date, duration } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Button, Dot, Input, Label, Panel, Select, Sheet, Spinner, Toggle, useToast } from "../components/ui";
import { PageHead } from "../App";

type S = Record<string, string>;
type Info = { publicIp: string; effectiveHost: string; selfSigned: boolean; certExpiry?: number };

const tabs: { id: string; key: Key }[] = [
  { id: "server", key: "serverAddress" },
  { id: "sub", key: "subscription" },
  { id: "panel", key: "panel" },
  { id: "cores", key: "cores" },
  { id: "backup", key: "backup" },
];

export function SettingsPage() {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<S | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [tab, setTab] = useState("server");
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => api<{ settings: S; info: Info }>("settings").then((r) => { setS(r.settings); setInfo(r.info); }), []);
  useEffect(() => { load(); }, [load]);
  if (!s || !info) return <Spinner />;
  const set = (k: string, v: string) => setS({ ...s, [k]: v });

  const save = (keys: string[]) => {
    setBusy(true);
    const body: S = {};
    keys.forEach((k) => (body[k] = s[k] ?? ""));
    api<{ restarting?: boolean }>("settings", { method: "PUT", body })
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
    <div className="flex justify-end pt-2"><Button variant="primary" loading={busy} onClick={() => save(keys)}>{t("save")}</Button></div>
  );

  return (
    <div className="rise">
      <PageHead title={t("settings")} />
      <div className="flex gap-1.5 overflow-x-auto mb-5 pb-1">
        {tabs.map((x) => (
          <button key={x.id} onClick={() => setTab(x.id)}
            className={cx("h-9 px-4 rounded-xl text-[13.5px] whitespace-nowrap border", tab === x.id ? "bg-ink text-ground border-ink font-semibold" : "border-hair2 text-ink2 hover:text-ink")}>
            {t(x.key)}
          </button>
        ))}
      </div>

      <div className="max-w-3xl flex flex-col gap-4">
        {tab === "server" && (
          <>
            <Panel title={t("serverAddress")}>
              <div className="grid sm:grid-cols-2 gap-4">
                <label><Label hint={info.publicIp}>{t("domain")}</Label><Input dir="ltr" value={s.domain ?? ""} placeholder="vpn.example.com" onChange={(e) => set("domain", e.target.value)} /></label>
                <label><Label hint={info.effectiveHost}>{t("host")}</Label><Input dir="ltr" value={s.host ?? ""} placeholder={info.publicIp} onChange={(e) => set("host", e.target.value)} /></label>
              </div>
              <div className="mt-4"><Toggle checked={s.block_torrent !== "false"} onChange={(v) => set("block_torrent", String(v))} label={t("blockTorrent")} /></div>
              <SaveBar keys={["domain", "host", "block_torrent"]} />
            </Panel>
            <Panel title={t("certificate")} action={<ShieldCheck className={cx("size-5", info.selfSigned ? "text-ink3" : "text-online")} />}>
              <p className="text-[13.5px] text-ink2 mb-4">
                {info.selfSigned ? (lang === "fa" ? "خودامضا (کلاینت‌ها با پین گواهی وصل می‌شوند)" : "Self-signed (clients pin the certificate)") : "Let's Encrypt / custom"}
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
              <div className="flex flex-wrap justify-end gap-2 pt-4">
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
        )}

        {tab === "sub" && (
          <Panel title={t("subscription")}>
            <div className="grid sm:grid-cols-2 gap-4">
              <label><Label>{t("subTitle")}</Label><Input value={s.sub_title ?? ""} onChange={(e) => set("sub_title", e.target.value)} /></label>
              <label><Label>{t("updateHours")}</Label><Input type="number" className="num" value={s.sub_update_hours ?? "12"} onChange={(e) => set("sub_update_hours", e.target.value)} /></label>
              <label className="sm:col-span-2"><Label>{t("supportUrl")}</Label><Input dir="ltr" value={s.support_url ?? ""} placeholder="https://t.me/yourchannel" onChange={(e) => set("support_url", e.target.value)} /></label>
              <label className="sm:col-span-2"><Label>{t("announce")}</Label><Input value={s.announce ?? ""} onChange={(e) => set("announce", e.target.value)} /></label>
              <label className="sm:col-span-2"><Label>Base URL</Label><Input dir="ltr" value={s.sub_base_url ?? ""} placeholder="https://sub.example.com:2096" onChange={(e) => set("sub_base_url", e.target.value)} /></label>
            </div>
            <div className="mt-4"><Toggle checked={s.sub_info_entry !== "false"} onChange={(v) => set("sub_info_entry", String(v))} label={t("infoEntry")} /></div>
            <SaveBar keys={["sub_title", "sub_update_hours", "support_url", "announce", "sub_base_url", "sub_info_entry"]} />
          </Panel>
        )}

        {tab === "panel" && (
          <>
            <Panel title={t("panel")}>
              <div className="grid sm:grid-cols-3 gap-4">
                <label><Label>{t("panelPort")}</Label><Input type="number" className="num" value={s.panel_port ?? ""} onChange={(e) => set("panel_port", e.target.value)} /></label>
                <label><Label>{t("panelPath")}</Label><Input dir="ltr" value={s.panel_path ?? ""} onChange={(e) => set("panel_path", e.target.value)} /></label>
                <label><Label>{t("subPath")}</Label><Input dir="ltr" value={s.sub_path ?? ""} onChange={(e) => set("sub_path", e.target.value)} /></label>
              </div>
              <div className="mt-4"><Toggle checked={s.panel_tls === "true"} onChange={(v) => set("panel_tls", String(v))} label={lang === "fa" ? "پنل روی HTTPS (با همان گواهی TLS)" : "Serve the panel over HTTPS (same TLS certificate)"} /></div>
              <SaveBar keys={["panel_port", "panel_path", "sub_path", "panel_tls"]} />
            </Panel>
            <AccountPanel />
          </>
        )}

        {tab === "cores" && <CoresPanel />}
        {tab === "backup" && <BackupPanel />}
      </div>
    </div>
  );
}

function AccountPanel() {
  const { t } = useI18n();
  const toast = useToast();
  const [f, setF] = useState({ username: "", currentPassword: "", newPassword: "" });
  const [busy, setBusy] = useState(false);
  return (
    <Panel title={t("account")}>
      <div className="grid sm:grid-cols-3 gap-4">
        <label><Label>{t("username")}</Label><Input dir="ltr" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} autoComplete="username" /></label>
        <label><Label>{t("currentPassword")}</Label><Input type="password" dir="ltr" value={f.currentPassword} onChange={(e) => setF({ ...f, currentPassword: e.target.value })} autoComplete="current-password" /></label>
        <label><Label>{t("newPassword")}</Label><Input type="password" dir="ltr" value={f.newPassword} onChange={(e) => setF({ ...f, newPassword: e.target.value })} autoComplete="new-password" /></label>
      </div>
      <div className="flex justify-end pt-4">
        <Button variant="primary" loading={busy} disabled={!f.currentPassword} onClick={() => {
          setBusy(true);
          api("account", { method: "PUT", body: f }).then(() => { toast(t("saved")); setF({ username: "", currentPassword: "", newPassword: "" }); })
            .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
        }}>{t("save")}</Button>
      </div>
    </Panel>
  );
}

function CoresPanel() {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [d, setD] = useState<{ cores: CoreStatus[]; errors: Record<string, string>; stats: Record<string, boolean> } | null>(null);
  const [upd, setUpd] = useState<{ current: string; latest: string; xray: string; singbox: string } | null>(null);
  const [busy, setBusy] = useState("");
  const [logs, setLogs] = useState<{ name: string; lines: string[] } | null>(null);
  const load = useCallback(() => api<NonNullable<typeof d>>("cores").then(setD), []);
  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, [load]);
  if (!d) return <Spinner />;
  const install = (name: string) => {
    setBusy(name);
    api<{ version: string }>(`cores/${name}/install`, { method: "POST", body: {} })
      .then((r) => { toast(`${name} ${r.version}`); load(); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  return (
    <>
      <Panel title={t("cores")} action={<Button size="sm" icon={<RefreshCw className="size-4" />} loading={busy === "apply"} onClick={() => { setBusy("apply"); api("cores/apply", { method: "POST", body: {} }).then(() => { toast(t("saved")); load(); }).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("restart")}</Button>}>
        <ul className="flex flex-col divide-y divide-hair">
          {d.cores.map((c) => (
            <li key={c.name} className="py-3 first:pt-0 flex flex-wrap items-center gap-3">
              <Dot color={c.running ? "var(--color-online)" : c.installed ? "var(--color-ink3)" : "var(--color-bad)"} pulse={c.running} />
              <div className="min-w-0">
                <div className="font-semibold" dir="ltr">{c.name} <span className="text-ink3 font-normal num">{c.version}</span></div>
                <div className="text-[12.5px] text-ink3">
                  {c.running ? `${t("running")} · ${duration(c.uptime, lang)}` : c.installed ? t("stopped") : t("notInstalled")}
                  {c.name === "sing-box" && c.installed && <> · {t("perUserStats")}: {d.stats["sing-box"] ? "✓" : "✕"}</>}
                </div>
                {(d.errors[c.name] || c.error) && <div className="text-[12px] text-[#ffc2cc] mt-1 break-all" dir="ltr">{d.errors[c.name] || c.error}</div>}
              </div>
              <div className="ms-auto flex gap-2">
                <Button size="sm" variant="quiet" icon={<ScrollText className="size-4" />} onClick={() => api<string[]>(`cores/${c.name}/logs`).then((lines) => setLogs({ name: c.name, lines }))}>{t("logs")}</Button>
                <Button size="sm" loading={busy === c.name} onClick={() => install(c.name)}>{c.installed ? t("update") : t("install")}</Button>
              </div>
            </li>
          ))}
        </ul>
      </Panel>
      <Panel title={t("version")} action={<Button size="sm" onClick={() => api<NonNullable<typeof upd>>("update").then(setUpd).catch((e) => toast(e.message, "error"))}>{t("checkUpdate")}</Button>}>
        <div className="text-[13.5px] grid grid-cols-2 gap-y-1.5 num" dir="ltr">
          <span className="text-ink2">Dunvex</span><span>{VERSION}{upd && upd.latest && upd.latest !== VERSION ? ` → ${upd.latest}` : ""}</span>
          {upd && <><span className="text-ink2">Xray (latest)</span><span>{upd.xray}</span><span className="text-ink2">sing-box (latest)</span><span>{upd.singbox}</span></>}
        </div>
        {upd && upd.latest && upd.latest !== VERSION && (
          <div className="flex justify-end pt-4"><Button variant="primary" loading={busy === "self"} onClick={() => { setBusy("self"); api("update", { method: "POST", body: {} }).then(() => toast(t("restartPanel"))).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("update")}</Button></div>
        )}
      </Panel>
      <Sheet open={!!logs} onClose={() => setLogs(null)} wide title={`${logs?.name} · ${t("logs")}`}>
        <pre dir="ltr" className="text-[11.5px] leading-relaxed text-ink2 whitespace-pre-wrap break-all font-mono">{logs?.lines.join("\n") || "—"}</pre>
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
    <Panel title={t("backup")}>
      <div className="flex flex-wrap gap-3">
        <a href={BASE + "backup"} download><Button icon={<Download className="size-4" />}>{t("downloadBackup")}</Button></a>
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
