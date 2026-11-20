import { useCallback, useEffect, useState } from "react";
import { KeyRound, LogOut, Monitor, Plus, Smartphone, Terminal } from "lucide-react";
import { api, BASE, type AdminSession, type ApiKey } from "../lib/api";
import { ago, date } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Button, ConfirmButton, CopyField, Dialog, IconButton, Input, Label, Panel, Select, Toggle, useConfirm, useToast } from "../components/ui";

function DeviceIcon({ device }: { device: string }) {
  const cls = "size-4";
  if (/Android|iOS/.test(device)) return <Smartphone className={cls} />;
  if (device === "Script") return <Terminal className={cls} />;
  return <Monitor className={cls} />;
}

/** Signed-in browsers; the owner sees every admin's. */
export function SessionsPanel({ me, className }: { me: string; className?: string }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const confirm = useConfirm();
  const [list, setList] = useState<AdminSession[] | null>(null);
  const load = useCallback(() => api<AdminSession[]>("account/sessions").then(setList).catch(() => setList([])), []);
  useEffect(() => { load(); }, [load]);
  const mineOthers = (list ?? []).filter((s) => s.admin === me && !s.current).length;

  const end = async (s: AdminSession) => {
    await api(`account/sessions/${s.id}`, { method: "DELETE" });
    toast(t("signedOutN"));
    load();
  };
  const endOthers = async () => {
    if (!(await confirm({ title: t("signOutOthers"), body: t("signOutOthersBody"), confirmLabel: t("signOutOthers") }))) return;
    await api("account/sessions/others", { method: "POST" });
    toast(t("signedOutN"));
    load();
  };

  return (
    <Panel className={className} icon={<Monitor className="size-4" />} title={t("sessions")} sub={t("sessionsSub")}
      action={<Button size="sm" variant="soft" icon={<LogOut className="size-3.5" />} disabled={!mineOthers} onClick={endOthers}>{t("signOutOthers")}</Button>}>
      <ul className="flex flex-col">
        {(list ?? []).map((s) => (
          <li key={s.id} className="flex items-center gap-3 py-2.5 border-b border-hair last:border-0">
            <span className="grid place-items-center size-8 rounded-lg shrink-0 bg-primary/10 text-primary2"><DeviceIcon device={s.device} /></span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-[13.5px] font-medium" dir="ltr">{s.device}</span>
                {s.current && <Badge color="var(--ok)">{t("thisDevice")}</Badge>}
                {s.admin !== me && <Badge><bdi dir="ltr">{s.admin}</bdi></Badge>}
              </div>
              <div className="text-[12px] text-ink3 mt-0.5 flex gap-2 flex-wrap">
                <bdi dir="ltr" className="mono sensitive">{s.ip}</bdi>
                <span>·</span>
                <span>{t("lastActive")} {ago(s.lastSeen, lang)}</span>
              </div>
            </div>
            {!s.current && (
              <IconButton label={t("signOut")} tone="danger" onClick={() => end(s)}><LogOut className="size-4" /></IconButton>
            )}
          </li>
        ))}
        {list && list.length <= 1 && <li className="text-ink3 text-[13px] pt-2">{t("noOtherSessions")}</li>}
      </ul>
    </Panel>
  );
}

/** API keys: made here, shown once, used as "Authorization: Bearer …". */
export function ApiKeysPanel({ me, className }: { me: string; className?: string }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [list, setList] = useState<ApiKey[] | null>(null);
  const [name, setName] = useState("");
  const [readOnly, setReadOnly] = useState(true);
  const [days, setDays] = useState("90");
  const [busy, setBusy] = useState(false);
  const [made, setMade] = useState<{ key: ApiKey; token: string } | null>(null);
  const load = useCallback(() => api<ApiKey[]>("account/keys").then(setList).catch(() => setList([])), []);
  useEffect(() => { load(); }, [load]);

  const create = async () => {
    setBusy(true);
    try {
      const r = await api<{ key: ApiKey; token: string }>("account/keys", { body: { name: name.trim(), readOnly, days: Number(days) } });
      setMade(r);
      setName("");
      load();
    } catch (e: any) {
      toast(e.message, "error");
    } finally {
      setBusy(false);
    }
  };
  const remove = async (k: ApiKey) => {
    await api(`account/keys/${k.id}`, { method: "DELETE" });
    load();
  };
  const example = made ? `curl -H "Authorization: Bearer ${made.token}" ${location.origin}${BASE}users` : "";

  return (
    <Panel className={className} icon={<KeyRound className="size-4" />} title={t("apiKeys")} sub={t("apiKeysSub")}>
      <div className="grid sm:grid-cols-[1fr_auto] gap-3 items-end">
        <label><Label>{t("keyName")}</Label><Input value={name} maxLength={40} placeholder="billing-bot" onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && name.trim()) create(); }} /></label>
        <label><Label>{t("keyExpiry")}</Label>
          <Select value={days} onChange={setDays} options={[
            { value: "30", label: t("days30") }, { value: "90", label: t("days90") }, { value: "365", label: t("days365") }, { value: "0", label: t("never") },
          ]} />
        </label>
      </div>
      <div className="flex items-center justify-between gap-3 mt-3 flex-wrap">
        <Toggle checked={readOnly} onChange={setReadOnly} label={t("readOnly")} hint={t("readOnlyHint")} />
        <Button variant="primary" size="sm" icon={<Plus className="size-3.5" />} loading={busy} disabled={!name.trim()} onClick={create}>{t("createKey")}</Button>
      </div>

      <ul className="flex flex-col mt-4">
        {(list ?? []).map((k) => (
          <li key={k.id} className="flex items-center gap-3 py-2.5 border-t border-hair">
            <span className="grid place-items-center size-8 rounded-lg shrink-0 bg-violet/10 text-violet"><KeyRound className="size-4" /></span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-[13.5px] font-medium truncate">{k.name}</span>
                {k.readOnly ? <Badge color="var(--ok)">{t("readOnly")}</Badge> : <Badge color="var(--warn)">{t("fullAccess")}</Badge>}
                {k.admin !== me && <Badge><bdi dir="ltr">{k.admin}</bdi></Badge>}
              </div>
              <div className="text-[12px] text-ink3 mt-0.5 flex gap-2 flex-wrap">
                <bdi dir="ltr" className="mono">{k.prefix}…</bdi>
                <span>·</span>
                <span>{k.lastUsed ? `${t("lastUsed")} ${ago(k.lastUsed, lang)}` : t("neverUsed")}</span>
                <span>·</span>
                <span>{k.expiresAt ? `${t("keyExpires")} ${date(k.expiresAt, lang)}` : t("never")}</span>
              </div>
            </div>
            <ConfirmButton title={t("deleteKey")} body={t("deleteKeyBody")} onConfirm={() => remove(k)}>{t("delete")}</ConfirmButton>
          </li>
        ))}
        {list && list.length === 0 && <li className="text-ink3 text-[13px] pt-3 border-t border-hair">{t("noKeys")}</li>}
      </ul>
      <p className="text-[12px] text-ink3 mt-3">{t("keyRules")}</p>

      <Dialog open={!!made} onClose={() => setMade(null)} title={t("keyCreated")} wide
        footer={<Button variant="primary" onClick={() => setMade(null)}>{t("close")}</Button>}>
        {made && (
          <div className="flex flex-col gap-3">
            <p className="text-[13.5px] text-warn leading-relaxed">{t("keyOnce")}</p>
            <CopyField value={made.token} />
            <Label>{t("keyExample")}</Label>
            <CopyField value={example} />
          </div>
        )}
      </Dialog>
    </Panel>
  );
}
