import { useCallback, useEffect, useRef, useState } from "react";
import { CircleAlert, CircleCheck, Cloud, Download, HardDriveDownload, KeyRound, Lock, RotateCcw, Send, ShieldCheck, Timer, Trash2, Upload } from "lucide-react";
import { api, BASE, type BackupFile, type BackupInfo, type BackupRun } from "../lib/api";
import { ago, bytes, cx, until } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, ConfirmButton, Dialog, Input, Label, Panel, Select, Skeleton, Toggle, useConfirm, useToast } from "./ui";

const reasonKey: Record<string, Key> = { schedule: "bkReasonSchedule", manual: "bkReasonManual", update: "bkReasonUpdate" };

/** Settings › Backup: the schedule, where copies go, the backups kept here, restore. */
export function BackupsTab() {
  const { t } = useI18n();
  const [info, setInfo] = useState<BackupInfo | null>(null);
  const [err, setErr] = useState("");
  const load = useCallback(() => api<BackupInfo>("backups").then((r) => { setInfo(r); setErr(""); }).catch((e) => setErr(e.message)), []);
  useEffect(() => { load(); }, [load]);
  if (!info) return err ? <p className="text-bad text-[13px]">{err}</p> : <div className="grid gap-4"><Skeleton className="h-64" /><Skeleton className="h-48" /></div>;
  return (
    <div className="flex flex-col gap-[var(--gap)]">
      <Schedule info={info} onSaved={setInfo} />
      <BucketPanel info={info} onSaved={setInfo} />
      <Stored info={info} reload={load} />
      {info.runs.length > 0 && <Runs runs={info.runs} />}
      <p className="text-[12px] text-ink3 leading-relaxed px-1">{t("bkWhat")}</p>
    </div>
  );
}

function Schedule({ info, onSaved }: { info: BackupInfo; onSaved: (i: BackupInfo) => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const confirm = useConfirm();
  const s = info.settings;
  const [hours, setHours] = useState(String(s.hours));
  const [keep, setKeep] = useState(String(s.keep));
  const [tg, setTg] = useState(s.telegram);
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const save = (extra: Record<string, unknown> = {}) => {
    setBusy(true);
    return api<BackupInfo>("backups/settings", { method: "PUT", body: { hours: +hours, keep: +keep || 14, telegram: tg, ...(pass ? { passphrase: pass } : {}), ...extra } })
      .then((r) => { onSaved(r); setPass(""); toast(t("saved")); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  const last = info.runs[0];
  return (
    <Panel icon={<Timer className="size-4" />} title={t("bkAuto")}
      sub={s.hours > 0 && info.next ? t("bkNext").replace("{in}", until(info.next, lang)) : t("bkOff")}
      action={last && (last.error || !runOK(last)
        ? <Badge color="var(--bad)" icon={<CircleAlert className="size-3.5" />}>{t("bkLastFailed")}</Badge>
        : <Badge color="var(--ok)" icon={<CircleCheck className="size-3.5" />}>{t("bkLastOK").replace("{ago}", ago(last.at, lang))}</Badge>)}>
      <div className="grid sm:grid-cols-2 gap-4">
        <label>
          <Label>{t("bkEvery")}</Label>
          <Select value={hours} onChange={setHours} options={[{ value: "0", label: t("bkByHand") }, ...[1, 2, 3, 6, 12, 24].map((h) => ({ value: String(h), label: t("everyHours").replace("{h}", n(h)) }))]} />
        </label>
        <label><Label hint={t("bkKeepHint")}>{t("bkKeep")}</Label><Input type="number" min={1} max={500} className="num" value={keep} onChange={(e) => setKeep(e.target.value)} /></label>
      </div>
      <div className="mt-5">
        <Toggle checked={tg} onChange={setTg} label={t("bkTelegram")}
          hint={info.telegram.configured ? t("bkTelegramHint").replace("{n}", n(info.telegram.admins)) : t("bkTelegramNoBot")} />
      </div>

      <div className="mt-6 pt-5 border-t border-hair">
        <div className="flex flex-wrap items-center gap-2 mb-2">
          <Lock className="size-4 text-ink2" />
          <span className="text-[14px] font-medium">{t("bkEncrypt")}</span>
          <Badge color={s.encrypted ? "var(--ok)" : "var(--color-ink3)"}>{s.encrypted ? t("bkEncryptedOn") : t("bkEncryptedOff")}</Badge>
        </div>
        <p className="text-[12.5px] text-ink2 leading-relaxed max-w-[72ch]">{t("bkEncryptHelp")}</p>
        <div className="mt-3 flex flex-wrap items-end gap-3">
          <label className="flex-1 min-w-[220px] max-w-sm">
            <Label>{s.encrypted ? t("bkNewPass") : t("bkPass")}</Label>
            <Input type="password" autoComplete="new-password" dir="ltr" value={pass} onChange={(e) => setPass(e.target.value)} placeholder={t("bkPassMin")} />
          </label>
          {s.encrypted && (
            <Button variant="quiet" disabled={busy} onClick={async () => {
              if (await confirm({ title: t("bkRemovePass"), body: t("bkRemovePassBody"), confirmLabel: t("bkRemovePass") })) save({ clearPassphrase: true });
            }}>{t("bkRemovePass")}</Button>
          )}
        </div>
        {pass && <p className="mt-2 text-[12px] text-warn leading-relaxed max-w-[72ch]">{t("bkPassWarn")}</p>}
      </div>

      <div className="mt-6 flex justify-end"><Button variant="primary" loading={busy} onClick={() => save()}>{t("save")}</Button></div>
    </Panel>
  );
}

function BucketPanel({ info, onSaved }: { info: BackupInfo; onSaved: (i: BackupInfo) => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const b = info.settings.s3;
  const [f, setF] = useState({ endpoint: b.endpoint, region: b.region, bucket: b.bucket, prefix: b.prefix, access: b.access, secret: "" });
  const [busy, setBusy] = useState("");
  const on = !!b.endpoint && !!b.bucket;
  const field = (k: keyof typeof f, label: string, placeholder = "", type = "text") => (
    <label><Label>{label}</Label><Input dir="ltr" type={type} autoComplete="off" className="mono text-[13px]" value={f[k]} placeholder={placeholder} onChange={(e) => setF({ ...f, [k]: e.target.value })} /></label>
  );
  return (
    <Panel icon={<Cloud className="size-4" />} title={t("bkBucket")} sub={t("bkBucketSub")}
      action={<Badge color={on ? "var(--ok)" : "var(--color-ink3)"}>{on ? t("on") : t("off")}</Badge>}>
      <p className="text-[12.5px] text-ink2 leading-relaxed max-w-[72ch] mb-4">{t("bkBucketHelp")}</p>
      <div className="grid sm:grid-cols-2 gap-4">
        <div className="sm:col-span-2">{field("endpoint", t("bkEndpoint"), "https://<account>.r2.cloudflarestorage.com")}</div>
        {field("bucket", t("bkBucketName"), "dunvex-backups")}
        {field("region", t("bkRegion"), "auto / us-east-1")}
        {field("access", t("bkAccess"))}
        {field("secret", t("bkSecret"), b.secretSet ? t("bkSecretSaved") : "", "password")}
        <div className="sm:col-span-2">{field("prefix", t("bkPrefix"), "panel-1")}</div>
      </div>
      <div className="mt-6 flex flex-wrap justify-end gap-2">
        {on && <ConfirmButton icon={<Trash2 className="size-4" />} title={t("bkBucketRemove")} body={t("bkBucketRemoveBody")}
          onConfirm={() => api<BackupInfo>("backups/settings", { method: "PUT", body: { clearS3: true } }).then((r) => { onSaved(r); setF({ endpoint: "", region: "", bucket: "", prefix: "", access: "", secret: "" }); toast(t("saved")); }).catch((e) => toast(e.message, "error"))}>{t("bkBucketRemove")}</ConfirmButton>}
        <Button icon={<ShieldCheck className="size-4" />} loading={busy === "check"} disabled={!!busy || !f.endpoint || !f.bucket}
          onClick={() => { setBusy("check"); api("backups/check-bucket", { body: f }).then(() => toast(t("bkBucketOK"))).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("bkCheck")}</Button>
        <Button variant="primary" loading={busy === "save"} disabled={!!busy || !f.endpoint || !f.bucket}
          onClick={() => { setBusy("save"); api<BackupInfo>("backups/settings", { method: "PUT", body: { s3: f } }).then((r) => { onSaved(r); setF({ ...f, secret: "" }); toast(t("saved")); }).catch((e) => toast(e.message, "error")).finally(() => setBusy("")); }}>{t("save")}</Button>
      </div>
    </Panel>
  );
}

function Stored({ info, reload }: { info: BackupInfo; reload: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const confirm = useConfirm();
  const file = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState("");
  const [ask, setAsk] = useState<{ run: (pass: string) => Promise<unknown> } | null>(null);
  const offsiteOn = info.settings.telegram && info.telegram.configured || !!info.settings.s3.endpoint;

  const make = (offsite: boolean) => {
    setBusy(offsite ? "off" : "here");
    api<BackupRun>("backups", { body: { offsite } })
      .then((r) => { toast(runOK(r) ? t("bkMade") : t("bkMadeNotSent"), runOK(r) ? undefined : "error"); reload(); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  // a restore that needs the passphrase asks for it, then tries again
  const restoreWith = (run: (pass: string) => Promise<unknown>) =>
    run("").then(() => toast(t("restartPanel"))).catch((e: any) => {
      if (e?.data?.needPassphrase || /passphrase/i.test(e.message)) setAsk({ run });
      else toast(e.message, "error");
    });
  const restoreStored = async (f: BackupFile) => {
    if (!(await confirm({ title: t("bkRestoreTitle"), body: t("bkRestoreBody").replace("{name}", f.name), danger: true, confirmLabel: t("restore") }))) return;
    restoreWith((pass) => api(`backups/${encodeURIComponent(f.name)}/restore`, { body: { passphrase: pass } }));
  };
  const upload = (fl: File) => restoreWith((pass) => {
    const form = new FormData();
    form.append("file", fl);
    if (pass) form.append("passphrase", pass);
    return api("restore", { form });
  });

  return (
    <Panel icon={<HardDriveDownload className="size-4" />} title={t("bkStored")} sub={n(info.files.length)}>
      <div className="flex flex-wrap gap-2 mb-3">
        <Button size="sm" variant="primary" icon={<HardDriveDownload className="size-4" />} loading={busy === "here"} disabled={!!busy} onClick={() => make(false)}>{t("bkNow")}</Button>
        {offsiteOn && <Button size="sm" icon={<Send className="size-4" />} loading={busy === "off"} disabled={!!busy} onClick={() => make(true)}>{t("bkNowOffsite")}</Button>}
        <Button size="sm" icon={<Upload className="size-4" />} onClick={() => file.current?.click()}>{t("bkUpload")}</Button>
      </div>
      <input ref={file} type="file" accept=".db,.gz,.enc" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ""; if (f) upload(f); }} />
      {info.files.length === 0 ? (
        <p className="text-[13px] text-ink3 py-6 text-center">{t("bkNone")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-hair">
          {info.files.map((f) => (
            <li key={f.name} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5">
              <span className={cx("grid place-items-center size-9 rounded-xl shrink-0", f.safety ? "bg-warn/12 text-warn" : "bg-primary/10 text-primary2")}>
                {f.encrypted ? <Lock className="size-4" /> : f.safety ? <RotateCcw className="size-4" /> : <HardDriveDownload className="size-4" />}
              </span>
              <div className="min-w-0 flex-1">
                <bdi dir="ltr" className="block mono text-[12.5px] truncate">{f.name}</bdi>
                <div className="text-[12px] text-ink3 flex flex-wrap gap-x-2">
                  <span>{ago(f.at, lang)}</span><span>·</span><span className="num">{bytes(f.size, lang)}</span>
                  {f.safety && <><span>·</span><span className="text-warn">{t("bkSafety")}</span></>}
                </div>
              </div>
              <div className="flex items-center gap-1 ms-auto">
                <a href={BASE + "backups/" + encodeURIComponent(f.name)} download><Button size="sm" variant="quiet" icon={<Download className="size-4" />}>{t("download")}</Button></a>
                <Button size="sm" variant="quiet" icon={<RotateCcw className="size-4" />} onClick={() => restoreStored(f)}>{t("restore")}</Button>
                <ConfirmButton icon={<Trash2 className="size-4" />} body={t("bkDeleteBody")}
                  onConfirm={() => api(`backups/${encodeURIComponent(f.name)}`, { method: "DELETE" }).then(reload).catch((e) => toast(e.message, "error"))}>{t("delete")}</ConfirmButton>
              </div>
            </li>
          ))}
        </ul>
      )}
      <PassDialog open={!!ask} onClose={() => setAsk(null)} onSubmit={(pass) => ask!.run(pass).then(() => { setAsk(null); toast(t("restartPanel")); })} />
    </Panel>
  );
}

function PassDialog({ open, onClose, onSubmit }: { open: boolean; onClose: () => void; onSubmit: (pass: string) => Promise<unknown> }) {
  const { t } = useI18n();
  const [pass, setPass] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) { setPass(""); setErr(""); } }, [open]);
  const go = () => { setBusy(true); onSubmit(pass).catch((e) => setErr(e.message)).finally(() => setBusy(false)); };
  return (
    <Dialog open={open} onClose={onClose} title={t("bkNeedPass")}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button><Button variant="primary" loading={busy} disabled={!pass} onClick={go}>{t("restore")}</Button></>}>
      <p className="text-[13.5px] text-ink2 mb-3">{t("bkNeedPassBody")}</p>
      <div className="relative"><KeyRound className="size-4 text-ink3 absolute top-3 [inset-inline-start:12px]" />
        <Input type="password" dir="ltr" autoFocus className="ps-9" value={pass} onChange={(e) => setPass(e.target.value)} onKeyDown={(e) => e.key === "Enter" && pass && go()} /></div>
      {err && <p className="mt-2 text-[12.5px] text-bad">{err}</p>}
    </Dialog>
  );
}

const runOK = (r: BackupRun) => !r.error && (!r.telegram || r.telegram === "ok") && (!r.s3 || r.s3 === "ok");

function Dest({ label, v }: { label: string; v?: string }) {
  if (!v) return null;
  const ok = v === "ok";
  return (
    <span className={cx("inline-flex items-center gap-1", ok ? "text-online" : "text-bad")} title={ok ? undefined : v}>
      {ok ? <CircleCheck className="size-3.5" /> : <CircleAlert className="size-3.5" />}{label}
    </span>
  );
}

function Runs({ runs }: { runs: BackupRun[] }) {
  const { t, lang } = useI18n();
  return (
    <Panel title={t("bkRuns")}>
      <ul className="flex flex-col divide-y divide-hair">
        {runs.slice(0, 12).map((r, i) => (
          <li key={i} className="py-2.5 text-[12.5px]">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="text-ink2 w-28 shrink-0">{ago(r.at, lang)}</span>
              <Badge className="h-5 text-[10.5px] px-2" color={r.reason === "schedule" ? "var(--primary2)" : r.reason === "update" ? "var(--color-violet)" : "var(--color-ink2)"}>{t(reasonKey[r.reason] ?? "bkReasonManual")}</Badge>
              {r.error ? <span className="text-bad">{t("bkFailed")}</span> : <>
                <span className="inline-flex items-center gap-1 text-ink2"><HardDriveDownload className="size-3.5" />{t("bkHere")}</span>
                <Dest label="Telegram" v={r.telegram} />
                <Dest label={t("bkBucketShort")} v={r.s3} />
                {r.encrypted && <Lock className="size-3.5 text-ink3" />}
              </>}
              {r.size ? <span className="ms-auto num text-ink3">{bytes(r.size, lang)}</span> : null}
            </div>
            {(r.error || (r.telegram && r.telegram !== "ok") || (r.s3 && r.s3 !== "ok")) && (
              <p dir="ltr" className="mt-1 text-[11.5px] text-bad/90 [overflow-wrap:anywhere]">{r.error || [r.telegram !== "ok" && r.telegram, r.s3 !== "ok" && r.s3].filter(Boolean).join(" · ")}</p>
            )}
          </li>
        ))}
      </ul>
    </Panel>
  );
}
