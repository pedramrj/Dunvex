import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, CircleCheck, CircleSlash, CircleX, Globe2, Plus, Radar, RefreshCw, Server, Sparkles, Trash2 } from "lucide-react";
import { api, type Field, type Group, type HealthResult, type Inbound, type NodeView, type Preset } from "../lib/api";
import { ago, bytes, cx, groupColor, groups } from "../lib/format";
import { msColor, useHealth, worstByInbound } from "../lib/health";
import { useI18n } from "../lib/i18n";
import { Badge, Button, ConfirmButton, Empty, Input, Label, SectionTitle, Select, Sheet, Skeleton, Toggle, useToast } from "../components/ui";
import { PageHead, navigate } from "../App";
import { shortName } from "../components/NetworkMap";
import { ErrText } from "../components/ErrText";

/** the program that serves each engine, as shown on the cards */
const engineName: Record<string, string> = { singbox: "sing-box", mtg: "mtg", awg: "AmneziaWG", mita: "mita", trusttunnel: "TrustTunnel", dnstt: "dnstt", slipstream: "Slipstream", ssh: "SSH (built in)", xray: "Xray" };

export function ProtocolsPage({ arg }: { arg?: string }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [inbounds, setInbounds] = useState<Inbound[] | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [nodes, setNodes] = useState<NodeView[]>([]);
  const load = useCallback(() => api<Inbound[]>("inbounds").then(setInbounds).catch((e) => toast(e.message, "error")), [toast]);
  useEffect(() => {
    load();
    api<Preset[]>("presets").then(setPresets).catch(() => {});
    api<NodeView[]>("nodes").then((l) => setNodes(l.filter((x) => x.role !== "relay"))).catch(() => {}); // relays run no protocols
  }, [load]);
  const nodeName = (id: number) => nodes.find((x) => x.id === id)?.name ?? "?";
  const health = useHealth();
  const worst = useMemo(() => worstByInbound(health.report?.results ?? []), [health.report]);
  const [asked, setAsked] = useState(false);
  const checking = !!health.report?.running || asked;
  const checkNow = () => { setAsked(true); health.check().catch((e) => toast(e.message, "error")).finally(() => setAsked(false)); };
  const byId = useMemo(() => Object.fromEntries(presets.map((p) => [p.id, p])), [presets]);

  const adding = arg?.startsWith("add");
  const addGroup = (arg?.split("-")[1] as Group | undefined) || undefined;
  const editing = arg && !adding ? inbounds?.find((i) => String(i.id) === arg) : undefined;
  const close = () => navigate("protocols");

  const toggle = (i: Inbound, enabled: boolean) =>
    api<{ warning?: string }>(`inbounds/${i.id}`, { method: "PUT", body: { enabled } })
      .then((r) => { if (r.warning) toast(r.warning, "error"); load(); })
      .catch((e) => toast(e.message, "error"));
  const errors = inbounds?.filter((i) => i.error).length ?? 0;

  return (
    <div>
      <PageHead
        title={t("protocols")}
        sub={inbounds && (
          <span className="inline-flex flex-wrap gap-x-4 gap-y-1">
            <span className="num">{n(inbounds.filter((i) => i.enabled).length)} / {n(inbounds.length)} {t("active")}</span>
            <span className="num text-ink3">{t("traffic")}: {bytes(inbounds.reduce((s, i) => s + i.up + i.down, 0), lang)}</span>
            {errors > 0 && <span className="text-bad num">{n(errors)} {t("error")}</span>}
          </span>
        )}
        action={<>
          {inbounds && inbounds.length > 0 && (
            <Button icon={<RefreshCw className={cx("size-4", checking && "animate-spin")} />} disabled={checking} onClick={checkNow}
              title={health.report?.at ? t("healthChecked").replace("{ago}", ago(health.report.at, lang)) : undefined}>
              {checking ? t("checking") : t("checkConfigs")}
            </Button>
          )}
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("protocols", "add")}>{t("addProtocol")}</Button>
        </>}
      />
      {!inbounds ? (
        <div className="grid sm:grid-cols-2 xl:grid-cols-3 gap-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-40" />)}</div>
      ) : inbounds.length === 0 ? (
        <div className="glass rounded-[20px]">
          <Empty icon={<Radar className="size-7" />} title={t("noLines")} action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("protocols", "add")}>{t("addProtocol")}</Button>} />
        </div>
      ) : (
        <div className="flex flex-col gap-7">
          {groups.map(({ id, color, label }) => {
            const list = inbounds.filter((i) => byId[i.preset]?.group === id);
            if (!list.length) return null;
            return (
              <section key={id}>
                <SectionTitle color={color} action={<button onClick={() => navigate("protocols", "add-" + id)} className="text-[12.5px] text-ink3 hover:text-ink inline-flex items-center gap-1"><Plus className="size-3.5" />{t("addProtocol")}</button>}>
                  {t(label)} <span className="text-ink3 font-normal num">{n(list.length)}</span>
                </SectionTitle>
                <div className="stagger grid sm:grid-cols-2 xl:grid-cols-3 gap-4">
                  {list.map((i) => {
                    const p = byId[i.preset];
                    const state = !i.enabled ? "var(--color-ink3)" : i.error ? "var(--color-bad)" : color;
                    return (
                      <article key={i.id} className={cx("rack-mod border border-hair2 rounded-[14px] overflow-hidden flex flex-col", !i.enabled && "opacity-70")}>
                        {/* a protocol is a module in the rack: its line's colour on top, its jack, its LED */}
                        <div className="h-[3px]" style={{ background: i.enabled ? color : "var(--color-hair2)" }} />
                        <div className="p-4 flex flex-col gap-3 flex-1">
                          <div className="flex items-start gap-3">
                            <span className="grid place-items-center size-11 rounded-2xl shrink-0" style={{ background: `color-mix(in oklab, ${color} 14%, transparent)` }}>
                              <span className="size-4 rounded-full border-[4px] bg-ground" style={{ borderColor: state }} />
                            </span>
                            <a href={`#/protocols/${i.id}`} className="min-w-0 flex-1 group">
                              <div className="flex items-center gap-2 min-w-0"><span className={cx("led", !i.enabled ? "" : i.error ? "rd" : "on")} /><span className="font-semibold truncate group-hover:text-primary2" dir="ltr">{i.remark || shortName[i.preset] || i.name}</span></div>
                              <div className="text-[12px] text-ink3 truncate" dir="ltr">{p?.name}</div>
                            </a>
                            <Toggle checked={i.enabled} onChange={(v) => toggle(i, v)} />
                          </div>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <Badge color={color}><span className="mono" dir="ltr">:{i.port}</span></Badge>
                            <Badge color="var(--color-ink2)">{engineName[p?.engine ?? ""] ?? "Xray"}</Badge>
                            {p?.network === "udp" && <Badge color="var(--color-udp)">UDP</Badge>}
                            {p?.status === "beta" && <Badge color="var(--color-violet)">{t("beta")}</Badge>}
                            {nodes.length > 0 && (
                              <Badge color={i.nodeId ? "var(--color-cyan)" : "var(--color-ink3)"} icon={<Server className="size-3" />}>
                                <bdi dir="ltr">{i.nodeId ? nodeName(i.nodeId) : t("thisServer")}</bdi>
                              </Badge>
                            )}
                            {i.enabled && worst[i.id] && <HealthBadge r={worst[i.id]} />}
                          </div>
                          {i.enabled && !i.error && worst[i.id]?.status === "fail" && worst[i.id].error && (
                            <ErrText raw={worst[i.id].error!} compact className="-mt-1 text-[12px] text-bad/90" />
                          )}
                          {i.error ? (
                            <div className="flex gap-2 items-start rounded-xl bg-bad/[.08] border border-bad/30 px-3 py-2 text-[12px] text-bad">
                              <AlertTriangle className="size-4 shrink-0 mt-0.5 text-bad" />
                              <ErrText raw={i.error} compact className="min-w-0 text-[12.5px]" />
                            </div>
                          ) : (
                            <div className="mt-auto grid grid-cols-2 gap-3 pt-3 border-t border-hair text-[12px]">
                              <div><div className="text-ink3">{t("download")}</div><div className="num font-medium text-[13px]">{bytes(i.down, lang)}</div></div>
                              <div><div className="text-ink3">{t("upload")}</div><div className="num font-medium text-[13px]">{bytes(i.up, lang)}</div></div>
                            </div>
                          )}
                        </div>
                      </article>
                    );
                  })}
                </div>
              </section>
            );
          })}
        </div>
      )}

      <AddSheet open={!!adding} group={addGroup} presets={presets} nodes={nodes} onClose={close} onDone={() => { load(); close(); }} />
      {editing && byId[editing.preset] && <EditSheet key={editing.id} inbound={editing} preset={byId[editing.preset]} nodes={nodes} onClose={close} onDone={() => { load(); }} />}
    </div>
  );
}

/** What the last config check found for an inbound (its worst copy). */
function HealthBadge({ r }: { r: HealthResult & { copies: number; failing: number } }) {
  const { t, n, sep } = useI18n();
  if (r.status === "ok")
    return <Badge color={msColor(r.ms)} icon={<CircleCheck className="size-3" />}><span className="num">{n(r.ms ?? 0)}</span> ms</Badge>;
  if (r.status === "fail")
    return <Badge color="var(--color-bad)" icon={<CircleX className="size-3" />}>{t("healthFail")}{r.copies > 1 && <span className="num">{sep}{n(r.failing)}/{n(r.copies)}</span>}</Badge>;
  return <span title={r.error}><Badge color="var(--color-ink3)" icon={<CircleSlash className="size-3" />}>{t("healthSkip")}</Badge></span>;
}

// ---------------------------------------------------------------- add

/** Setup steps some protocols need before they work. */
function PresetHelp({ id }: { id: string }) {
  const { t } = useI18n();
  const key = id === "vless-xdns" ? "xdnsHelp" : id === "vless-xicmp" ? "xicmpHelp" : id === "dnstt" || id === "slipstream" ? "dnsTunnelHelp" : id === "mtproto" ? "mtprotoHelp" : id === "amneziawg" ? "amneziawgHelp" : id === "mieru" || id === "mieru-udp" ? "mieruHelp" : id === "trusttunnel" ? "trusttunnelHelp" : id === "ssh" ? "sshHelp" : null;
  if (!key) return null;
  return <p className="rounded-xl border border-hair bg-fg/[.03] px-3.5 py-3 text-[12.5px] text-ink2 leading-relaxed">{t(key)}</p>;
}

/** The server a protocol runs on, when there is more than one. */
function ServerPick({ nodes, value, onChange }: { nodes: NodeView[]; value: string; onChange: (v: string) => void }) {
  const { t } = useI18n();
  if (nodes.length === 0) return null;
  return (
    <label>
      <Label>{t("server")}</Label>
      <Select value={value} onChange={onChange} options={[{ value: "0", label: t("thisServer") }, ...nodes.map((x) => ({ value: String(x.id), label: x.name }))]} />
    </label>
  );
}

function AddSheet({ open, group, presets, nodes, onClose, onDone }: { open: boolean; group?: Group; presets: Preset[]; nodes: NodeView[]; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [pick, setPick] = useState<Preset | null>(null);
  const [port, setPort] = useState("");
  const [remark, setRemark] = useState("");
  const [params, setParams] = useState<Record<string, any>>({});
  const [server, setServer] = useState("0");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) { setPick(null); setPort(""); setRemark(""); setParams({}); setServer("0"); }
  }, [open]);

  const submit = () => {
    if (!pick) return;
    setBusy(true);
    api<{ warning?: string }>("inbounds", { body: { preset: pick.id, port: +port || 0, remark, params, nodeId: +server } })
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
                      style={{ borderInlineStartColor: g.color }}
                      className="text-start rounded-2xl border border-hair border-s-[3px] bg-fg/[.03] hover:bg-fg/[.06] hover:border-hair2 px-4 py-3.5 transition-[background-color,border-color] duration-200 hover:-translate-y-0.5">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-[13.5px]" dir="ltr">{p.name}</span>
                        {p.recommended && <Badge color="var(--color-udp)" icon={<Sparkles className="size-3" />} className="h-5 text-[10.5px] px-2">{t("recommended")}</Badge>}
                        {p.status === "beta" && <Badge color="var(--color-violet)" className="h-5 text-[10.5px] px-2">{t("beta")}</Badge>}
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
          <PresetHelp id={pick.id} />
          {nodes.length > 0 && (
            <div className="grid sm:grid-cols-2 gap-3 items-end">
              <ServerPick nodes={nodes} value={server} onChange={setServer} />
              {server !== "0" && <p className="text-[12px] text-ink3 pb-1">{t("nodeDomainNote")}</p>}
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <label><Label hint={pick.defaultPort ? String(pick.defaultPort) : t("randomPort")}>{t("port")}</Label><Input type="number" value={port} placeholder={pick.defaultPort ? String(pick.defaultPort) : t("randomPort")} onChange={(e) => setPort(e.target.value)} className="num" /></label>
            <label><Label>{t("remark")}</Label><Input value={remark} onChange={(e) => setRemark(e.target.value)} dir="ltr" /></label>
          </div>
          <Params fields={pick.fields} values={params} onChange={setParams} group={pick.group} />
        </div>
      )}
    </Sheet>
  );
}

// ---------------------------------------------------------------- edit

function EditSheet({ inbound, preset, nodes, onClose, onDone }: { inbound: Inbound; preset: Preset; nodes: NodeView[]; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [server, setServer] = useState(String(inbound.nodeId ?? 0));
  const [port, setPort] = useState(String(inbound.port));
  const [remark, setRemark] = useState(inbound.remark);
  const [params, setParams] = useState<Record<string, any>>(inbound.params);
  const [busy, setBusy] = useState(false);
  const save = () => {
    setBusy(true);
    api<{ warning?: string }>(`inbounds/${inbound.id}`, { method: "PUT", body: { port: +port, remark, params, nodeId: +server } })
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
        <PresetHelp id={preset.id} />
        {inbound.error && <div className="rounded-xl border border-bad/40 bg-bad/[.07] px-3 py-2"><ErrText raw={inbound.error} /></div>}
        {nodes.length > 0 && <div className="grid sm:grid-cols-2 gap-3"><ServerPick nodes={nodes} value={server} onChange={setServer} /></div>}
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
          <Select value={v ?? f.options?.[0] ?? ""} onChange={(x) => set(f.key, x)} options={(f.options ?? []).map((o, i) => ({ value: o, label: (lang === "fa" ? f.optionLabelsFa : f.optionLabels)?.[i] ?? o }))} />
        </label>
      );
    return (
      <label key={f.key}>
        <Label>{label}</Label>
        <div className="flex gap-2">
          <Input value={v ?? ""} type={f.type === "number" ? "number" : "text"} onChange={(e) => set(f.key, f.type === "number" ? +e.target.value : e.target.value)} dir="ltr" placeholder={f.type === "secret" || f.key === "publicKey" ? "auto" : ""} className={f.type === "secret" ? "mono text-[12px]" : ""} />
          {f.key === "sni" && group === "reality" && <SniTest onPick={(d) => onChange({ ...values, sni: d, target: d + ":443" })} />}
        </div>
      </label>
    );
  };
  return (
    <div className="flex flex-col gap-4">
      {basic.map(render)}
      {advanced.length > 0 && (
        <div className="rounded-2xl border border-hair bg-fg/[.02]">
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
        <div className="rise absolute z-10 mt-2 [inset-inline-end:0] w-72 max-h-72 overflow-auto rounded-2xl glass-strong p-1.5 shadow-[0_18px_44px_-12px_rgba(0,0,0,.8)]">
          {res.map((r) => (
            <button key={r.domain} type="button" disabled={!r.ok} onClick={() => { onPick(r.domain); setRes(null); }}
              className="w-full flex items-center gap-2 h-10 px-2.5 rounded-xl text-[13px] hover:bg-fg/[.06] disabled:opacity-45">
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
