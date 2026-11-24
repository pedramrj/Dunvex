import { useEffect, useState } from "react";
import { Fingerprint, Plus, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import { ago } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { addPasskey, passkeyErrorKey, passkeysPossible } from "../lib/passkey";
import { Button, ConfirmButton, Dialog, Input, Label, Panel, useToast } from "./ui";

type Passkey = { id: number; name: string; rpId: string; createdAt: number; lastUsedAt: number };
type State = { passkeys: Passkey[]; supported: boolean; reason?: string; rpId?: string; max: number };

/** The signed-in admin's passkeys: add one (with the current password), see when each was used, remove one. */
export function PasskeysPanel({ className }: { className?: string }) {
  const { t, lang, sep } = useI18n();
  const toast = useToast();
  const [s, setS] = useState<State | null>(null);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const load = () => api<State>("account/passkeys").then(setS).catch(() => {});
  useEffect(() => { load(); }, []);
  const browserOK = passkeysPossible();
  const can = !!s?.supported && browserOK;

  const add = async () => {
    setBusy(true);
    setErr("");
    try {
      await addPasskey(password, name);
      toast(t("pkAdded"));
      setOpen(false);
      setPassword("");
      setName("");
      load();
    } catch (e) {
      const k = passkeyErrorKey(e);
      setErr(k ? t(k) : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel className={className} icon={<Fingerprint className="size-4" />} title={t("passkeys")}
      action={can ? <Button size="sm" variant="soft" icon={<Plus className="size-4" />} onClick={() => { setErr(""); setOpen(true); }}
        disabled={(s?.passkeys.length ?? 0) >= (s?.max ?? 20)}>{t("pkAdd")}</Button> : undefined}>
      <p className="text-[12.5px] text-ink2 leading-6 mb-3">{t("pkIntro")}</p>
      {s && !s.supported && <p className="text-[12.5px] text-warn leading-6 mb-3">{t("pkNeedsDomain")}</p>}
      {s?.supported && !browserOK && <p className="text-[12.5px] text-warn leading-6 mb-3">{t("pkNoBrowser")}</p>}
      <ul className="flex flex-col">
        {(s?.passkeys ?? []).map((p) => (
          <li key={p.id} className="flex items-center gap-3 py-2.5 border-b border-hair last:border-0">
            <span className="led on" aria-hidden />
            <div className="flex-1 min-w-0">
              <div className="truncate text-[13.5px]">{p.name}</div>
              <div className="text-[11.5px] text-ink3">
                <bdi dir="ltr">{p.rpId}</bdi>{sep}{t("pkMade")} {ago(p.createdAt, lang)}{sep}{p.lastUsedAt ? `${t("pkUsed")} ${ago(p.lastUsedAt, lang)}` : t("pkNeverUsed")}
              </div>
            </div>
            <ConfirmButton icon={<Trash2 className="size-4" />} title={t("pkRemoveQ")} body={t("pkRemoveBody")}
              onConfirm={() => api(`account/passkeys/${p.id}`, { method: "DELETE" }).then(() => { toast(t("pkRemoved")); load(); }).catch((e) => toast((e as Error).message))}>
              {t("delete")}
            </ConfirmButton>
          </li>
        ))}
        {s && s.passkeys.length === 0 && <li className="text-ink3 text-[13px] py-4 text-center">{t("pkNone")}</li>}
      </ul>

      <Dialog open={open} onClose={() => !busy && setOpen(false)} title={t("pkAdd")}
        footer={<>
          <Button variant="ghost" onClick={() => setOpen(false)} disabled={busy}>{t("cancel")}</Button>
          <Button variant="primary" loading={busy} disabled={!password} onClick={add} icon={<Fingerprint className="size-4" />}>{t("pkCreate")}</Button>
        </>}>
        <p className="text-[12.5px] text-ink2 leading-6 mb-4">{t("pkAddHint")}</p>
        <label className="block mb-4">
          <Label>{t("pkName")}</Label>
          <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={40} placeholder={t("pkNamePh")} />
        </label>
        <label className="block mb-2">
          <Label>{t("currentPassword")}</Label>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" dir="ltr"
            onKeyDown={(e) => { if (e.key === "Enter" && password && !busy) add(); }} />
        </label>
        {err && <div role="alert" className="mt-3 rounded-xl border border-bad/40 bg-bad/[.08] px-3 py-2.5 text-sm text-bad">{err}</div>}
      </Dialog>
    </Panel>
  );
}
