import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, Globe2, Plus, Radar, Sparkles, Trash2 } from "lucide-react";
import { api, type Field, type Group, type Inbound, type Preset } from "../lib/api";
import { bytes, cx, groupColor, groups } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Button, ConfirmButton, Empty, Input, Label, Select, Sheet, Spinner, Toggle, useToast } from "../components/ui";
import { PageHead, navigate } from "../App";
import { shortName } from "../components/NetworkMap";

export function ProtocolsPage({ arg }: { arg?: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [inbounds, setInbounds] = useState<Inbound[] | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const load = useCallback(() => api<Inbound[]>("inbounds").then(setInbounds).catch((e) => toast(e.message, "error")), [toast]);
  useEffect(() => {
    load();
    api<Preset[]>("presets").then(setPresets).catch(() => {});
  }, [load]);
  const byId = useMemo(() => Object.fromEntries(presets.map((p) => [p.id, p])), [presets]);

  const adding = arg?.startsWith("add");
  const addGroup = (arg?.split("-")[1] as Group | undefined) || undefined;
  const editing = arg && !adding ? inbounds?.find((i) => String(i.id) === arg) : undefined;
  const close = () => navigate("protocols");

  const toggle = (i: Inbound, enabled: boolean) =>
    api<{ warning?: string }>(`inbounds/${i.id}`, { method: "PUT", body: { enabled } })
      .then((r) => { if (r.warning) toast(r.warning, "error"); load(); })
      .catch((e) => toast(e.message, "error"));

  return (
    <div className="rise">
      <PageHead
        title={t("protocols")}
        sub={inbounds && <span className="num">{n(inbounds.filter((i) => i.enabled).length)} / {n(inbounds.length)} {t("active")}</span>}
        action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("protocols", "add")}>{t("addProtocol")}</Button>}
      />
      {!inbounds ? (
        <Spinner />
      ) : inbounds.length === 0 ? (
        <div className="rounded-2xl border border-hair bg-panel">
          <Empty icon={<Radar className="size-6" />} title={t("noLines")} action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("protocols", "add")}>{t("addProtocol")}</Button>} />
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          {groups.map(({ id, color, label }) => {
            const list = inbounds.filter((i) => byId[i.preset]?.group === id);
            if (!list.length) return null;
            return (
              <section key={id}>
                <h2 className="flex items-center gap-2.5 mb-2 text-[14px] font-semibold">
                  <i className="h-2 w-7 rounded-full" style={{ background: color }} />
                  {t(label)}
                  <span className="text-ink3 font-normal num">{n(list.length)}</span>
                </h2>
                <ul className="rounded-2xl border border-hair bg-panel overflow-hidden">
                  {list.map((i) => {
                    const p = byId[i.preset];
                    return (
                      <li key={i.id} className="border-b border-hair last:border-b-0">
                        <div className="flex items-center gap-4 px-4 py-3">
                          <span className="size-4 rounded-full border-[4px] shrink-0 bg-ground" style={{ borderColor: !i.enabled ? "var(--color-ink3)" : i.error ? "var(--color-bad)" : color }} />
                          <a href={`#/protocols/${i.id}`} className="min-w-0 flex-1">
                            <div className="font-semibold truncate" dir="ltr">{i.remark || shortName[i.preset] || i.name}</div>
                            <div className="text-[12.5px] text-ink3 truncate" dir="ltr">{p?.name} · <span className="num">:{i.port}</span> · {p?.engine === "singbox" ? "sing-box" : "Xray"}</div>
                          </a>
                          <div className="hidden sm:block text-end text-[12.5px] num">
                            <div>{bytes(i.up + i.down, lang)}</div>
                            <div className="text-ink3">{t("traffic")}</div>
                          </div>
                          <Toggle checked={i.enabled} onChange={(v) => toggle(i, v)} />
                        </div>
                        {i.error && (
                          <div className="flex gap-2 items-start px-4 pb-3 -mt-1 text-[12.5px] text-[#ffc2cc]">
                            <AlertTriangle className="size-4 shrink-0 mt-0.5 text-bad" />
                            <bdi dir="ltr">{i.error}</bdi>
                          </div>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </section>
            );
          })}
        </div>
      )}

      <AddSheet open={!!adding} group={addGroup} presets={presets} onClose={close} onDone={() => { load(); close(); }} />
      {editing && byId[editing.preset] && <EditSheet key={editing.id} inbound={editing} preset={byId[editing.preset]} onClose={close} onDone={() => { load(); }} />}
    </div>
  );
}

// ---------------------------------------------------------------- add

function AddSheet({ open, group, presets, onClose, onDone }: { open: boolean; group?: Group; presets: Preset[]; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [pick, setPick] = useState<Preset | null>(null);
  const [port, setPort] = useState("");
  const [remark, setRemark] = useState("");
  const [params, setParams] = useState<Record<string, any>>({});
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) { setPick(null); setPort(""); setRemark(""); setParams({}); }
  }, [open]);

  const submit = () => {
    if (!pick) return;
    setBusy(true);
    api<{ warning?: string }>("inbounds", { body: { preset: pick.id, port: +port || 0, remark, params } })
      .then((r) => { r.warning ? toast(r.warning, "error") : toast(t("saved")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };

  const shown = presets.filter((p) => !group || p.group === group);
  return (
    <Sheet open={open} onClose={onClose} wide title={pick ? pick.name : t("choosePreset")}
      footer={pick && (<><Button variant="quiet" onClick={() => setPick(null)}>{t("cancel")}</Button><Button variant="primary" loading={busy} onClick={submit}>{t("create")}</Button></>)}>
      {!pick ? (
        <div className="flex flex-col gap-6">
          {groups.filter((g) => !group || g.id === group).map((g) => {
            const list = shown.filter((p) => p.group === g.id);
            if (!list.length) return null;
            return (
              <div key={g.id}>
                <div className="flex items-center gap-2 mb-2 text-[13px] font-semibold" style={{ color: g.color }}>
                  <i className="h-1.5 w-5 rounded-full" style={{ background: g.color }} />{t(g.label)}
                </div>
                <div className="grid sm:grid-cols-2 gap-2">
                  {list.map((p) => (
                    <button key={p.id} onClick={() => setPick(p)}
                      className="text-start rounded-xl border border-hair2 bg-panel hover:border-ink3 hover:bg-panel2 px-3.5 py-3 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-[13.5px]" dir="ltr">{p.name}</span>
                        {p.recommended && <Sparkles className="size-3.5 text-udp" aria-label={t("recommended")} />}
                        {p.status === "beta" && <span className="text-[11px] text-ink3 border border-hair2 rounded px-1">{t("beta")}</span>}
                      </div>
                      <p className="text-[12.5px] text-ink2 mt-1 leading-relaxed">{lang === "fa" ? p.descFa : p.desc}</p>
                      {p.needsDomain && <div className="mt-1.5 inline-flex items-center gap-1 text-[11.5px] text-cdn"><Globe2 className="size-3.5" />{t("needsDomain")}</div>}
                    </button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <p className="text-ink2 text-[13.5px] -mt-1">{lang === "fa" ? pick.descFa : pick.desc}</p>
          <div className="grid grid-cols-2 gap-3">
            <label><Label hint={String(pick.defaultPort)}>{t("port")}</Label><Input type="number" value={port} placeholder={String(pick.defaultPort)} onChange={(e) => setPort(e.target.value)} className="num" /></label>
            <label><Label>{t("remark")}</Label><Input value={remark} onChange={(e) => setRemark(e.target.value)} dir="ltr" /></label>
          </div>
          <Params fields={pick.fields} values={params} onChange={setParams} group={pick.group} />
        </div>
      )}
    </Sheet>
  );
}

// ---------------------------------------------------------------- edit

function EditSheet({ inbound, preset, onClose, onDone }: { inbound: Inbound; preset: Preset; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [port, setPort] = useState(String(inbound.port));
  const [remark, setRemark] = useState(inbound.remark);
  const [params, setParams] = useState<Record<string, any>>(inbound.params);
  const [busy, setBusy] = useState(false);
  const save = () => {
    setBusy(true);
    api<{ warning?: string }>(`inbounds/${inbound.id}`, { method: "PUT", body: { port: +port, remark, params } })
      .then((r) => { r.warning ? toast(r.warning, "error") : toast(t("saved")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  return (
    <Sheet open onClose={onClose} wide title={<span dir="ltr">{preset.name}</span>}
      footer={<>
        <ConfirmButton icon={<Trash2 className="size-4" />} onConfirm={() => api(`inbounds/${inbound.id}`, { method: "DELETE" }).then(() => { onDone(); onClose(); })}>{t("delete")}</ConfirmButton>
        <span className="me-auto" />
        <Button variant="quiet" onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button>
      </>}>
      <div className="flex flex-col gap-5">
        <p className="text-ink2 text-[13.5px] -mt-1">{lang === "fa" ? preset.descFa : preset.desc}</p>
        {inbound.error && <div className="rounded-xl border border-[rgba(255,93,115,.4)] bg-[rgba(255,93,115,.07)] px-3 py-2 text-[13px]"><bdi dir="ltr">{inbound.error}</bdi></div>}
        <div className="grid grid-cols-2 gap-3">
          <label><Label>{t("port")}</Label><Input type="number" value={port} onChange={(e) => setPort(e.target.value)} className="num" /></label>
          <label><Label>{t("remark")}</Label><Input value={remark} onChange={(e) => setRemark(e.target.value)} dir="ltr" /></label>
        </div>
        <Params fields={preset.fields} values={params} onChange={setParams} group={preset.group} />
        <div className="grid grid-cols-2 gap-3 text-[12.5px] text-ink2 pt-2">
          <div>↓ <span className="num text-ink">{bytes(inbound.down, lang)}</span></div>
          <div>↑ <span className="num text-ink">{bytes(inbound.up, lang)}</span></div>
        </div>
      </div>
    </Sheet>
  );
}

// ---------------------------------------------------------------- parameter fields

function Params({ fields, values, onChange, group }: { fields: Field[]; values: Record<string, any>; onChange: (v: Record<string, any>) => void; group: Group }) {
  const { t, lang } = useI18n();
  const [adv, setAdv] = useState(false);
  const basic = fields.filter((f) => !f.advanced);
  const advanced = fields.filter((f) => f.advanced);
  const set = (k: string, v: unknown) => onChange({ ...values, [k]: v });
  const render = (f: Field) => {
    const label = lang === "fa" ? f.labelFa : f.label;
    const v = values[f.key];
    if (f.type === "bool") return <Toggle key={f.key} checked={!!v} onChange={(x) => set(f.key, x)} label={label} />;
    if (f.type === "select")
      return (
        <label key={f.key}>
          <Label>{label}</Label>
          <Select value={v ?? f.options?.[0] ?? ""} onChange={(x) => set(f.key, x)} options={(f.options ?? []).map((o) => ({ value: o, label: o }))} />
        </label>
      );
    return (
      <label key={f.key}>
        <Label>{label}</Label>
        <div className="flex gap-2">
          <Input value={v ?? ""} type={f.type === "number" ? "number" : "text"} onChange={(e) => set(f.key, f.type === "number" ? +e.target.value : e.target.value)} dir="ltr" placeholder={f.type === "secret" ? "auto" : ""} className={f.type === "secret" ? "font-mono text-[12px]" : ""} />
          {f.key === "sni" && group === "reality" && <SniTest onPick={(d) => onChange({ ...values, sni: d, target: d + ":443" })} />}
        </div>
      </label>
    );
  };
  return (
    <div className="flex flex-col gap-4">
      {basic.map(render)}
      {advanced.length > 0 && (
        <div className="rounded-xl border border-hair2">
          <button type="button" onClick={() => setAdv(!adv)} className="w-full flex items-center justify-between h-11 px-3.5 text-[13.5px] text-ink2 hover:text-ink">
            {t("advanced")}
            <ChevronDown className={cx("size-4 transition-transform", adv && "rotate-180")} />
          </button>
          {adv && <div className="flex flex-col gap-4 px-3.5 pb-4">{advanced.map(render)}</div>}
        </div>
      )}
    </div>
  );
}

function SniTest({ onPick }: { onPick: (d: string) => void }) {
  const { t, n } = useI18n();
  const [res, setRes] = useState<{ domain: string; ok: boolean; latencyMs: number; error?: string }[] | null>(null);
  const [busy, setBusy] = useState(false);
  const run = () => {
    setBusy(true);
    api<typeof res>("tools/sni", { body: {} }).then(setRes).finally(() => setBusy(false));
  };
  return (
    <div className="relative">
      <Button type="button" loading={busy} onClick={run} icon={<Radar className="size-4" />}>{t("testSni")}</Button>
      {res && (
        <div className="rise absolute z-10 mt-2 [inset-inline-end:0] w-72 max-h-72 overflow-auto rounded-xl border border-hair2 bg-panel2 p-1.5 shadow-[0_14px_40px_-12px_rgba(0,0,0,.7)]">
          {res.map((r) => (
            <button key={r.domain} type="button" disabled={!r.ok} onClick={() => { onPick(r.domain); setRes(null); }}
              className="w-full flex items-center gap-2 h-9 px-2.5 rounded-lg text-[13px] hover:bg-panel disabled:opacity-45">
              <span className={cx("size-2 rounded-full", r.ok ? "bg-online" : "bg-bad")} />
              <span dir="ltr" className="truncate">{r.domain}</span>
              <span className="ms-auto num text-ink3">{r.ok ? `${n(r.latencyMs)}ms` : "✕"}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export const _groupColor = groupColor;
