import { useEffect, useState } from "react";
import { RadioTower } from "lucide-react";
import { api, type SupportSettings } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { Badge, Button, Input, Label, Panel, Skeleton, Toggle, useToast } from "./ui";
import { navigate } from "../App";

type Info = { settings: SupportSettings; bot: { configured: boolean } };

/** Settings › Telegram: the support inbox in the bot. */
export function SupportSettingsPanel() {
  const { t, n } = useI18n();
  const toast = useToast();
  const [f, setF] = useState<SupportSettings | null>(null);
  const [saved, setSaved] = useState<SupportSettings | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api<Info>("support/settings").then((r) => { setF(r.settings); setSaved(r.settings); }).catch((e) => toast(e.message, "error"));
  }, []);
  if (!f || !saved) return <Skeleton className="h-64" />;
  const num = (k: keyof SupportSettings, v: string) => setF({ ...f, [k]: Math.max(0, Math.round(+v || 0)) });
  const save = () => {
    setBusy(true);
    api<Info>("support/settings", { method: "PUT", body: f })
      .then((r) => { setF(r.settings); setSaved(r.settings); toast(t("saved")); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };

  return (
    <Panel icon={<RadioTower className="size-4" />} title={t("spsTitle")} sub={t("spsSub")}
      action={<Badge color={saved.on ? "var(--ok)" : "var(--color-ink3)"}>{saved.on ? t("on") : t("off")}</Badge>}>
      <p className="text-[13px] text-ink2 leading-relaxed max-w-[72ch]">{t("spsHelp")}</p>
      <div className="mt-4 flex flex-col gap-3">
        <Toggle checked={f.on} onChange={(v) => setF({ ...f, on: v })} label={t("spsOn")} />
        <Toggle checked={f.alert} onChange={(v) => setF({ ...f, alert: v })} label={t("spsAlert")} hint={t("spsAlertHint")} />
        <Toggle checked={f.guests} onChange={(v) => setF({ ...f, guests: v })} label={t("spsGuests")} hint={t("spsGuestsHint")} />
      </div>
      <div className="mt-5 grid sm:grid-cols-2 gap-4">
        <label><Label hint={f.rate === 0 ? t("unlimited") : undefined}>{t("spsRate")}</Label>
          <Input type="number" min={0} max={1000} className="num" value={f.rate} onChange={(e) => num("rate", e.target.value)} /></label>
        <label><Label hint={t("spsChars")}>{t("spsMaxLen")}</Label>
          <Input type="number" min={50} max={4000} className="num" value={f.maxLen} onChange={(e) => num("maxLen", e.target.value)} /></label>
        <label><Label hint={f.keep === 0 ? t("spsKeepAll") : undefined}>{t("spsKeep")}</Label>
          <Input type="number" min={0} className="num" value={f.keep} onChange={(e) => num("keep", e.target.value)} /></label>
        <label><Label hint={f.days === 0 ? t("never") : n(f.days) + " " + t("days")}>{t("spsDays")}</Label>
          <Input type="number" min={0} max={3650} className="num" value={f.days} onChange={(e) => num("days", e.target.value)} /></label>
        <label className="sm:col-span-2"><Label hint={<span className="num">{n(f.note.length)} / {n(300)}</span>}>{t("spsNote")}</Label>
          <Input dir="auto" maxLength={300} value={f.note} placeholder={t("spsNotePh")} onChange={(e) => setF({ ...f, note: e.target.value })} /></label>
      </div>
      <p className="mt-3 text-[12px] text-ink3 leading-relaxed">{t("spsPhotos")}</p>
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button onClick={() => navigate("support")}>{t("spsOpenInbox")}</Button>
        <Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button>
      </div>
    </Panel>
  );
}
