import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Activity, ChartLine, KeyRound, Pencil, Plus, Send, Trash2, Webhook } from "lucide-react";
import { api, BASE } from "../lib/api";
import { ago, cx } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, ConfirmButton, CopyField, Dialog, Dot, Empty, IconButton, Input, Label, Panel, Segmented, Sheet, Skeleton, Toggle, useToast } from "../components/ui";
import { navigate } from "../App";
import { ErrText } from "../components/ErrText";

interface Delivery { id: string; event: string; message: string; at: number; attempt: number; ok: boolean; status?: number; error?: string; ms: number; retry?: number }
interface Hook { id: string; name: string; url: string; format: "json" | "slack" | "discord"; kinds: string[]; secret: string; enabled: boolean; last?: Delivery }
interface HookList { hooks: Hook[]; kinds: string[] }
type Form = { id?: string; name: string; url: string; format: Hook["format"]; kinds: string[]; enabled: boolean };

const kindKey: { [k: string]: Key } = {
  user: "catUser", limit: "catLimit", auth: "catAuth", billing: "catBilling", node: "catNode", core: "catCore",
  tls: "catTls", inbound: "catInbound", health: "catHealth", backup: "catBackup", settings: "catSettings", update: "catUpdate", support: "catSupport",
};
const formatName = { json: "JSON", slack: "Slack", discord: "Discord" };

/** Webhooks and Prometheus metrics (owner only). */
export function IntegrationsTab() {
  return (
    <>
      <WebhooksPanel />
      <MetricsPanel />
    </>
  );
}

function WebhooksPanel() {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [d, setD] = useState<HookList | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [created, setCreated] = useState<Hook | null>(null);
  const [log, setLog] = useState<Hook | null>(null);
  const [busy, setBusy] = useState("");
  const load = useCallback(() => api<HookList>("webhooks").then(setD).catch((e) => toast(e.message, "error")), [toast]);
  useEffect(() => { load(); }, [load]);
  if (!d) return <Skeleton className="h-48" />;

  const save = () => {
    if (!form) return;
    setBusy("save");
    const body = { name: form.name, url: form.url, format: form.format, kinds: form.kinds, enabled: form.enabled };
    const p = form.id ? api<{ hook: Hook; hooks: Hook[] }>(`webhooks/${form.id}`, { method: "PUT", body }) : api<{ hook: Hook; hooks: Hook[] }>("webhooks", { body });
    p.then((r) => {
      setD({ ...d, hooks: r.hooks });
      if (!form.id && r.hook.format === "json") setCreated(r.hook);
      setForm(null);
      toast(t("saved"));
    }).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const test = (h: Hook) => {
    setBusy("test" + h.id);
    api<Delivery>(`webhooks/${h.id}/test`, { method: "POST" })
      .then((r) => { r.ok ? toast(t("hookTestOk")) : toast(`${t("hookTestFail")} ${r.error ?? ""}`, "error"); load(); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const toggle = (h: Hook, on: boolean) =>
    api<{ hooks: Hook[] }>(`webhooks/${h.id}`, { method: "PUT", body: { enabled: on } }).then((r) => setD({ ...d, hooks: r.hooks })).catch((e) => toast(e.message, "error"));
  const remove = (h: Hook) =>
    api<{ hooks: Hook[] }>(`webhooks/${h.id}`, { method: "DELETE" }).then((r) => setD({ ...d, hooks: r.hooks })).catch((e) => toast(e.message, "error"));

  return (
    <Panel icon={<Webhook className="size-4" />} title={t("webhooks")}
      action={<Button size="sm" variant="primary" icon={<Plus className="size-4" />} disabled={d.hooks.length >= 20}
        onClick={() => setForm({ name: "", url: "", format: "json", kinds: [], enabled: true })}>{t("hookAdd")}</Button>}>
      <p className="text-[13px] text-ink2 leading-7 mb-4">{t("webhooksHint")}</p>
      {d.hooks.length === 0 ? (
        <Empty icon={<Webhook className="size-7" />} title={t("hookNone")} hint={t("hookNoneHint")} />
      ) : (
        <div className="flex flex-col gap-2">
          {d.hooks.map((h) => (
            <div key={h.id} className={cx("rounded-2xl border border-hair px-4 py-3", !h.enabled && "opacity-70")}>
              <div className="flex flex-wrap items-center gap-2">
                <Toggle checked={h.enabled} onChange={(v) => toggle(h, v)} />
                <span className="text-[14px] font-medium">{h.name}</span>
                <Badge>{formatName[h.format]}</Badge>
                <span className="ms-auto flex items-center gap-1">
                  <Button size="sm" variant="soft" icon={<Send className="size-3.5" />} loading={busy === "test" + h.id} onClick={() => test(h)}>{t("hookTest")}</Button>
                  <IconButton label={t("hookLog")} onClick={() => setLog(h)}><Activity className="size-4" /></IconButton>
                  <IconButton label={t("edit")} onClick={() => setForm({ id: h.id, name: h.name, url: h.url, format: h.format, kinds: h.kinds, enabled: h.enabled })}><Pencil className="size-4" /></IconButton>
                  <ConfirmButton icon={<Trash2 className="size-3.5" />} title={t("hookRemove")} body={h.name} onConfirm={() => remove(h)}>{t("delete")}</ConfirmButton>
                </span>
              </div>
              <div className="mt-2 text-[12px] text-ink3 mono truncate" dir="ltr">{h.url}</div>
              <div className="mt-2 flex flex-wrap items-center gap-1.5">
                {h.kinds.length === 0 ? <Badge color="var(--primary2)">{t("hookAllEvents")}</Badge> : h.kinds.map((k) => <Badge key={k}>{t(kindKey[k] ?? "catCore")}</Badge>)}
                {h.last && (
                  // the last delivery: a readable sentence for a failure, the time kept apart so it never runs into the text
                  <div className={cx("flex items-start gap-2 text-[12px] text-ink3", h.last.ok ? "ms-auto" : "w-full mt-1")}>
                    <span className={cx("led mt-[8px]", h.last.ok ? "on" : "rd")} />
                    {h.last.ok ? <span className="leading-6 mono" dir="ltr">{h.last.status ?? ""}</span> : <ErrText raw={h.last.error ?? ""} className="flex-1 min-w-0 text-ink2 !text-[12.5px]" />}
                    <span className="ms-auto shrink-0 leading-6 whitespace-nowrap">{ago(h.last.at, lang)}</span>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <Dialog open={form !== null} onClose={() => setForm(null)} wide title={form?.id ? t("hookEdit") : t("hookAdd")}
        footer={<><Button variant="quiet" onClick={() => setForm(null)}>{t("cancel")}</Button>
          <Button variant="primary" loading={busy === "save"} disabled={!form?.url.trim()} onClick={save}>{t("save")}</Button></>}>
        {form && (
          <div className="flex flex-col gap-4 mt-1">
            <label><Label>{t("hookUrl")}</Label>
              <Input dir="ltr" className="mono" placeholder="https://example.com/dunvex-hook" value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} /></label>
            <label><Label>{t("name")}</Label>
              <Input value={form.name} maxLength={40} placeholder={t("hookNamePh")} onChange={(e) => setForm({ ...form, name: e.target.value })} /></label>
            <div>
              <Label>{t("hookFormat")}</Label>
              <Segmented value={form.format} onChange={(v) => setForm({ ...form, format: v })}
                options={[{ value: "json", label: "JSON" }, { value: "slack", label: "Slack" }, { value: "discord", label: "Discord" }]} />
              <p className="text-[12.5px] text-ink3 mt-2 leading-6">{t(form.format === "json" ? "hookFormatJson" : "hookFormatChat")}</p>
            </div>
            <div>
              <Label>{t("hookEvents")}</Label>
              <div className="flex flex-wrap gap-1.5">
                <Chip on={form.kinds.length === 0} onClick={() => setForm({ ...form, kinds: [] })}>{t("hookAllEvents")}</Chip>
                {d.kinds.map((k) => (
                  <Chip key={k} on={form.kinds.includes(k)}
                    onClick={() => setForm({ ...form, kinds: form.kinds.includes(k) ? form.kinds.filter((x) => x !== k) : [...form.kinds, k] })}>{t(kindKey[k] ?? "catCore")}</Chip>
                ))}
              </div>
            </div>
            <Toggle checked={form.enabled} onChange={(v) => setForm({ ...form, enabled: v })} label={t("hookEnabled")} />
            {form.id && form.format === "json" && <SecretField hook={d.hooks.find((h) => h.id === form.id)!} onRotated={(hs) => setD({ ...d, hooks: hs })} />}
          </div>
        )}
      </Dialog>

      <Dialog open={created !== null} onClose={() => setCreated(null)} title={t("hookSecretTitle")}
        footer={<Button variant="primary" onClick={() => setCreated(null)}>{t("done")}</Button>}>
        {created && (
          <div className="flex flex-col gap-3">
            <p className="text-[13px] text-ink2 leading-7">{t("hookSecretHint")}</p>
            <CopyField value={created.secret} />
            <SignatureHelp />
          </div>
        )}
      </Dialog>

      <Sheet open={log !== null} onClose={() => setLog(null)} title={log?.name ?? ""} sub={t("hookLog")}>
        {log && <DeliveryLog hook={log} />}
      </Sheet>
    </Panel>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-pressed={on}
      className={cx("h-9 px-3.5 rounded-full border text-[13px] transition-[background-color,border-color,color]",
        on ? "bg-primary/15 border-primary/50 text-ink" : "border-hair text-ink2 hover:text-ink hover:bg-fg/[.04]")}>
      {children}
    </button>
  );
}

function SignatureHelp() {
  const { t } = useI18n();
  return (
    <div className="rounded-xl border border-hair bg-fg/[.025] px-3.5 py-3 text-[12.5px] text-ink3 leading-6">
      {t("hookVerify")}
      <pre dir="ltr" className="mono text-[11.5px] text-ink2 mt-2 whitespace-pre-wrap break-all">{"X-Dunvex-Signature: t=<unix>,v1=<hex>\nv1 = HMAC-SHA256(secret, t + \".\" + body)"}</pre>
    </div>
  );
}

function SecretField({ hook, onRotated }: { hook: Hook; onRotated: (h: Hook[]) => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const rotate = () => {
    setBusy(true);
    api<{ hooks: Hook[] }>(`webhooks/${hook.id}`, { method: "PUT", body: { rotateSecret: true } })
      .then((r) => { onRotated(r.hooks); toast(t("hookSecretNew")); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  return (
    <div className="flex flex-col gap-2">
      <Label>{t("hookSecret")}</Label>
      <CopyField value={hook.secret} />
      <div className="flex items-center gap-2">
        <Button size="sm" variant="soft" icon={<KeyRound className="size-3.5" />} loading={busy} onClick={rotate}>{t("hookRotate")}</Button>
      </div>
      <SignatureHelp />
    </div>
  );
}

function DeliveryLog({ hook }: { hook: Hook }) {
  const { t, lang, n } = useI18n();
  const [list, setList] = useState<Delivery[] | null>(null);
  useEffect(() => {
    const load = () => api<Delivery[]>(`webhooks/${hook.id}/deliveries`).then(setList).catch(() => setList([]));
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [hook.id]);
  if (!list) return <Skeleton className="h-40" />;
  if (list.length === 0) return <p className="text-[13px] text-ink3 py-8 text-center">{t("hookLogEmpty")}</p>;
  return (
    <div className="flex flex-col divide-y divide-hair">
      {list.map((x, i) => (
        <div key={x.id + i} className="py-3 flex gap-3 items-start">
          <Dot color={x.ok ? "var(--ok)" : "var(--bad)"} size={8} />
          <div className="flex-1 min-w-0">
            <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
              <Badge>{x.event === "test" ? t("hookTest") : t(kindKey[x.event] ?? "catCore")}</Badge>
              <span className="text-ink3">{ago(x.at, lang)}</span>
              {x.attempt > 1 && <span className="text-ink3">· {t("hookAttempt")} {n(x.attempt)}</span>}
              <span className="ms-auto text-ink3 mono" dir="ltr">{x.ok ? `${x.status} · ${x.ms} ms` : x.error}</span>
            </div>
            <div className="text-[13px] text-ink2 mt-1 break-words">{x.message}</div>
            {x.retry ? <div className="text-[12px] text-warn mt-1">{t("hookRetrying")}</div> : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function MetricsPanel() {
  const { t, n } = useI18n();
  const toast = useToast();
  const [preview, setPreview] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const url = new URL(BASE + "metrics", window.location.href);
  const scrape = `scrape_configs:
  - job_name: dunvex
    scheme: ${url.protocol.replace(":", "")}
    metrics_path: ${url.pathname}
    authorization:
      credentials: dvx_…  # a read-only API key
    static_configs:
      - targets: ["${url.host}"]`;
  const show = () => {
    setBusy(true);
    fetch(BASE + "metrics", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then(setPreview).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  const series = preview ? preview.split("\n").filter((l) => l && !l.startsWith("#")).length : 0;
  return (
    <Panel icon={<ChartLine className="size-4" />} title={t("metrics")}>
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-ink2 leading-7">{t("metricsHint")}</p>
        <div><Label>{t("metricsUrl")}</Label><CopyField value={url.toString()} sensitive={false} /></div>
        <ol className="flex flex-col gap-2 text-[13px] text-ink2 leading-6 list-decimal ps-5">
          <li>{t("metricsStep1")} <button className="text-primary2 hover:underline" onClick={() => navigate("security")}>{t("security")}</button></li>
          <li>{t("metricsStep2")}</li>
        </ol>
        <pre dir="ltr" className="mono text-[11.5px] text-ink2 rounded-xl border border-hair bg-rail p-3.5 overflow-x-auto">{scrape}</pre>
        <p className="text-[12.5px] text-ink3">{t("metricsUsers")}</p>
        <div className="flex flex-wrap items-center gap-3">
          <Button size="sm" variant="soft" icon={<Activity className="size-4" />} loading={busy} onClick={show}>{t("metricsPreview")}</Button>
          {preview && <span className="text-[12.5px] text-ink3">{t("metricsSeries")}: <span className="num">{n(series)}</span></span>}
        </div>
        {preview && <pre dir="ltr" className="mono text-[11px] text-ink2 rounded-xl border border-hair bg-rail p-3.5 max-h-80 overflow-auto">{preview}</pre>}
      </div>
    </Panel>
  );
}
