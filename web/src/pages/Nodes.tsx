import { useCallback, useEffect, useState } from "react";
import { ArrowRightLeft, CircleArrowUp, Cpu, Globe, HardDrive, LayoutGrid, List, MemoryStick, Network, Pencil, Plus, Power, RefreshCw, Server, Settings2, ShieldCheck, Trash2, TriangleAlert, Users as UsersIcon } from "lucide-react";
import { api, type Geo, type NodeView, type Overview, type RelayStat } from "../lib/api";
import { ago, bytes, cx, date, duration, speed } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Button, ConfirmButton, CopyField, Dialog, Dot, ErrorState, IconButton, Input, KV, Label, Meter, Panel, Segmented, Select, Skeleton, Textarea, Toggle, useToast } from "../components/ui";
import { Spark } from "../components/charts";
import { Logo } from "../components/Logo";
import { PageHead, useSession } from "../App";
import { ErrText } from "../components/ErrText";
import { Cells } from "../components/Rack";

const INSTALL_NODE = "bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh) node";

/** This server and every node it runs protocols on, with their real state. */
export function NodesPage() {
  const { t, lang, n, pc, sep } = useI18n();
  const { me } = useSession();
  const owner = me.admin.role === "owner";
  const [d, setD] = useState<Overview | null>(null);
  const [geo, setGeo] = useState<Geo | null>(null);
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [err, setErr] = useState("");
  const [mode, setMode] = useState<"grid" | "list">("grid");
  const [adding, setAdding] = useState(false);
  const [updatingAll, setUpdatingAll] = useState(false);
  const toast = useToast();
  const loadNodes = useCallback(() => api<NodeView[]>("nodes").then(setNodes).catch(() => setNodes([])), []);
  useEffect(() => {
    let alive = true;
    const load = () => {
      api<Overview>("overview?days=1").then((r) => alive && setD(r)).catch((e) => alive && setErr(e.message));
      loadNodes();
    };
    load();
    const id = setInterval(load, 5000);
    api<Geo>("network/geo").then(setGeo).catch(() => {});
    return () => { alive = false; clearInterval(id); };
  }, [loadNodes]);
  if (!d) return err ? <ErrorState message={err} /> : <Skeleton className="h-96" />;
  const s = d.system;
  const pct = (a: number, b: number) => (b ? (a / b) * 100 : 0);
  const problems = Object.keys(d.coreErrors ?? {}).length + (d.inboundErrs ?? 0);
  const hist = d.history.slice(-90);
  const live = d.live[d.live.length - 1] ?? { up: 0, down: 0 };
  const servers = (nodes ?? []).filter((x) => x.role !== "relay"); // where a relay can point
  const behind = (nodes ?? []).filter((x) => x.online && x.update && !x.update.newer && x.update.via);
  const updateAll = () => {
    setUpdatingAll(true);
    api<{ results: { name: string; version?: string; error?: string }[] }>("nodes/update", { method: "POST" })
      .then((r) => {
        const bad = r.results.filter((x) => x.error);
        bad.forEach((x) => toast(`${x.name}: ${x.error}`, "error"));
        if (r.results.length > bad.length) toast(t("nodeUpdated"));
        loadNodes();
      })
      .catch((e) => toast(e.message, "error")).finally(() => setUpdatingAll(false));
  };

  const local = (
    <Panel className="rack-mod">
      <div className="flex flex-wrap items-center gap-4 mb-5">
        <span className="grid place-items-center size-14 rounded-2xl bg-primary/12 border border-primary/30"><Logo part="emblem" width={42} /></span>
        <div className="min-w-0">
          <div className="text-[18px] font-bold" dir="ltr">{s.hostname || "Dunvex"}</div>
          <div className="text-[12.5px] text-ink3"><span className="mono sensitive" dir="ltr">{s.publicIp}</span>{geo ? ` · ${geo.city ? geo.city + ", " : ""}${geo.country}` : ""}</div>
        </div>
        <div className="ms-auto flex flex-wrap gap-2">
          <Badge color={problems ? "var(--bad)" : "var(--ok)"}><Dot color={problems ? "var(--bad)" : "var(--ok)"} size={6} pulse={!problems} />{problems ? t("issues") : t("healthy")}</Badge>
          <Badge color="var(--primary2)">{t("thisServer")}</Badge>
        </div>
      </div>
      <Rings cpu={s.cpu} mem={pct(s.memUsed, s.memTotal)} disk={pct(s.diskUsed, s.diskTotal)} />
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-5">
        <KV label={t("uptime")}>{duration(s.uptime, lang)}</KV>
        <KV label={t("lastHeartbeat")}>{t("justNow")}</KV>
        <KV label={t("bandwidth")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(live.down, lang)}</span><span>↑ {speed(live.up, lang)}</span></span></KV>
        <KV label={t("netNow")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(s.netDown, lang)}</span><span>↑ {speed(s.netUp, lang)}</span></span></KV>
        <KV label={t("today")}>{bytes(d.today.down + d.today.up, lang)}</KV>
        <KV label={t("thisMonth")}>{bytes(d.month.down + d.month.up, lang)}</KV>
        <KV label={t("users")}><span className="inline-flex items-center gap-1"><UsersIcon className="size-3.5 text-ink3" /><span className="num">{n(d.users)}{sep}{n(d.online)} {t("online")}</span></span></KV>
        <KV label={t("protocols")}><span className="inline-flex items-center gap-1"><Network className="size-3.5 text-ink3" /><span className="num">{n(d.inboundsOn)} / {n(d.inbounds)}</span></span></KV>
      </div>
      <div className="grid sm:grid-cols-2 gap-4">
        <div><div className="text-[11.5px] text-ink3 mb-1">{t("cpu")} · {t("last6min")}</div><Spark values={hist.map((h) => h.cpu)} color="var(--warn)" height={44} /></div>
        <div><div className="text-[11.5px] text-ink3 mb-1">{t("ram")} · {t("last6min")}</div><Spark values={hist.map((h) => h.mem)} color="var(--primary2)" height={44} /></div>
      </div>
      <div className="mt-5 flex flex-wrap gap-2">
        {d.cores.map((c) => (
          <Badge key={c.name} color={c.running ? "var(--ok)" : "var(--text3)"}><Dot color={c.running ? "var(--ok)" : "var(--text3)"} size={6} /><span dir="ltr">{c.name} {c.version}</span></Badge>
        ))}
      </div>
      <div className="mt-4"><Meter value={s.diskUsed} max={s.diskTotal} /></div>
    </Panel>
  );

  const state = (x: NodeView) => !x.enabled ? { c: "var(--text3)", k: "nodeOff" as const }
    : !x.paired ? { c: "var(--warn)", k: "nodePairing" as const }
    : x.online ? { c: "var(--ok)", k: "nodeOnline" as const } : { c: "var(--bad)", k: "nodeOffline" as const };

  return (
    <div>
      <PageHead title={t("nodes")} sub={t("nodesSub")} action={
        <div className="flex items-center gap-2">
          {owner && behind.length > 1 && <Button variant="soft" icon={<CircleArrowUp className="size-4" />} loading={updatingAll} onClick={updateAll}>{t("nodeUpdateAll")}{sep}{n(behind.length)}</Button>}
          {owner && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>{t("addServer")}</Button>}
          <Segmented value={mode} onChange={setMode} options={[{ value: "grid", label: <LayoutGrid className="size-4" /> }, { value: "list", label: <List className="size-4" /> }]} />
        </div>} />
      {mode === "grid" ? (
        <div className="grid gap-[var(--gap)] xl:grid-cols-2">
          {local}
          {(nodes ?? []).map((x) => <NodeCard key={x.id} node={x} servers={servers} owner={owner} onChange={loadNodes} />)}
        </div>
      ) : (
        <div className="glass rounded-[18px] overflow-x-auto">
          <table className="w-full text-[13px]">
            <thead className="text-ink3 text-[12px]"><tr className="border-b border-hair">{[t("name"), t("status"), "IP", t("cpu"), t("ram"), t("disk"), t("uptime"), t("protocols")].map((h) => <th key={h} className="text-start font-medium px-4 h-11">{h}</th>)}</tr></thead>
            <tbody>
              <tr className="border-b border-hair">
                <td className="px-4 h-14"><span className="inline-flex items-center gap-2"><Server className="size-4 text-primary2" /><span dir="ltr">{s.hostname}</span><Badge color="var(--primary2)">{t("thisServer")}</Badge></span></td>
                <td className="px-4"><Badge color={problems ? "var(--bad)" : "var(--ok)"}>{problems ? t("issues") : t("healthy")}</Badge></td>
                <td className="px-4 mono sensitive" dir="ltr">{s.publicIp}</td>
                <td className="px-4 num">{pc(s.cpu, 0)}</td>
                <td className="px-4 num">{pc(pct(s.memUsed, s.memTotal), 0)}</td>
                <td className="px-4 num">{pc(pct(s.diskUsed, s.diskTotal), 0)}</td>
                <td className="px-4">{duration(s.uptime, lang)}</td>
                <td className="px-4 num">{n(d.inboundsOn)} / {n(d.inbounds)}</td>
              </tr>
              {(nodes ?? []).map((x) => {
                const st = state(x), ns = x.status;
                return (
                  <tr key={x.id} className="border-b border-hair last:border-0">
                    <td className="px-4 h-14"><span className="inline-flex items-center gap-2">{x.role === "relay" ? <ArrowRightLeft className="size-4 text-violet" /> : <Server className="size-4 text-cyan" />}<span dir="ltr">{x.name}</span></span></td>
                    <td className="px-4"><Badge color={st.c}>{t(st.k)}</Badge></td>
                    <td className="px-4 mono sensitive" dir="ltr">{x.address}</td>
                    <td className="px-4 num">{ns ? pc(ns.cpu, 0) : "—"}</td>
                    <td className="px-4 num">{ns ? pc(pct(ns.memUsed, ns.memTotal), 0) : "—"}</td>
                    <td className="px-4 num">{ns ? pc(pct(ns.diskUsed, ns.diskTotal), 0) : "—"}</td>
                    <td className="px-4">{ns ? duration(ns.uptime, lang) : "—"}</td>
                    <td className="px-4 num">{x.role === "relay" ? `${t("relay")}${sep}${n(ns?.forwards?.length ?? 0)}` : n(x.inbounds)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <div className="mt-4 max-w-3xl flex flex-col gap-1.5 text-[12.5px] text-ink3">
        <p>{t("multiNodeNote")}</p>
        <p className="inline-flex items-start gap-1.5"><ShieldCheck className="size-4 shrink-0 text-primary2" />{t("nodeSecure")}</p>
        <p className="inline-flex items-start gap-1.5"><ArrowRightLeft className="size-4 shrink-0 text-violet" />{t("relayNote")}</p>
      </div>
      {owner && <AddServerDialog open={adding} servers={servers} onClose={() => setAdding(false)} onDone={() => { setAdding(false); loadNodes(); }} />}
    </div>
  );
}

function Rings({ cpu, mem, disk }: { cpu: number; mem: number; disk: number }) {
  const { t, pc } = useI18n();
  // console readouts: a segment meter per resource, amber past 75 %, red past 90 %
  const tone = (v: number) => (v >= 90 ? "var(--bad)" : v >= 75 ? "var(--warn)" : "var(--ok)");
  return (
    <div className="grid grid-cols-3 gap-3 mb-5">
      {[{ k: t("cpu"), v: cpu, i: <Cpu className="size-3.5" /> }, { k: t("ram"), v: mem, i: <MemoryStick className="size-3.5" /> }, { k: t("disk"), v: disk, i: <HardDrive className="size-3.5" /> }].map((x) => (
        <div key={x.k} className="field-recess p-3">
          <div className="flex items-center gap-1.5 text-[12px] text-ink2">{x.i}{x.k}<b className="ms-auto num font-semibold" style={x.v >= 75 ? { color: tone(x.v) } : undefined}>{pc(x.v, 0)}</b></div>
          <div className="mt-2"><Cells lit={Math.round(x.v / 10)} n={10} c={tone(x.v)} /></div>
        </div>
      ))}
    </div>
  );
}

/** One node: its health, what it runs, and what the owner can do with it. */
function NodeCard({ node: x, servers, owner, onChange }: { node: NodeView; servers: NodeView[]; owner: boolean; onChange: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(x.name);
  const [relaySet, setRelaySet] = useState(false);
  const [domainSet, setDomainSet] = useState(false);
  const [domain, setDomain] = useState(x.domain);
  const isRelay = x.role === "relay";
  const up = x.update;
  const ns = x.status;
  const pct = (a: number, b: number) => (b ? (a / b) * 100 : 0);
  const st = !x.enabled ? { c: "var(--text3)", k: "nodeOff" as const }
    : !x.paired ? { c: "var(--warn)", k: "nodePairing" as const }
    : x.online ? { c: "var(--ok)", k: "nodeOnline" as const } : { c: "var(--bad)", k: "nodeOffline" as const };
  const act = (key: string, p: Promise<unknown>, ok?: string) => {
    setBusy(key);
    p.then(() => { if (ok) toast(ok); onChange(); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const coreErr = ns ? Object.entries(ns.coreErrors ?? {}) : [];

  return (
    <Panel className={cx("rack-mod", !x.enabled && "opacity-75")}>
      <div className="flex flex-wrap items-center gap-4 mb-5">
        {isRelay
          ? <span className="grid place-items-center size-14 rounded-2xl bg-violet/10 border border-violet/30 text-violet"><ArrowRightLeft className="size-6" /></span>
          : <span className="grid place-items-center size-14 rounded-2xl bg-cyan/10 border border-cyan/30 text-cyan"><Server className="size-6" /></span>}
        <div className="min-w-0">
          <div className="text-[18px] font-bold truncate" dir="ltr">{x.name}</div>
          <div className="text-[12.5px] text-ink3"><span className="mono sensitive" dir="ltr">{x.address}</span>{x.domain ? <> · <span className="mono" dir="ltr">{x.domain}</span></> : null}{ns ? <> · <span dir="ltr">Dunvex {ns.version}</span></> : null}</div>
        </div>
        <div className="ms-auto flex flex-wrap items-center gap-1.5">
          {isRelay && <Badge color="var(--color-violet)">{t("relayTo")} <bdi dir="ltr">{x.relayTarget ? x.targetName : t("thisServer")}</bdi>{x.relayMode === "reverse" ? ` · ${t("relayModeReverse")}` : ""}</Badge>}
          <Badge color={st.c}><Dot color={st.c} size={6} pulse={x.online} />{t(st.k)}</Badge>
          {owner && (
            <>
              {isRelay && <IconButton label={t("relaySettings")} onClick={() => setRelaySet(true)}><Settings2 className="size-4" /></IconButton>}
              {!isRelay && <IconButton label={t("nodeDomainTitle")} onClick={() => { setDomain(x.domain); setDomainSet(true); }}><Globe className="size-4" /></IconButton>}
              <IconButton label={t("syncNow")} onClick={() => act("sync", api(`nodes/${x.id}/sync`, { method: "POST" }))} disabled={!!busy}><RefreshCw className={cx("size-4", busy === "sync" && "animate-spin")} /></IconButton>
              <IconButton label={t("rename")} onClick={() => { setName(x.name); setRenaming(true); }}><Pencil className="size-4" /></IconButton>
              <IconButton label={x.enabled ? t("turnOff") : t("turnOn")} onClick={() => act("power", api(`nodes/${x.id}`, { method: "PUT", body: { enabled: !x.enabled } }))}><Power className="size-4" /></IconButton>
            </>
          )}
        </div>
      </div>

      {x.error && (
        <div className="mb-4 rounded-xl bg-bad/[.08] border border-bad/30 px-3 py-2"><ErrText raw={x.error} /></div>
      )}
      {up && x.online && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-primary2/30 bg-primary2/[.07] px-3 py-2 text-[12.5px]">
          <CircleArrowUp className="size-4 text-primary2 shrink-0" />
          <span className="flex-1 min-w-0">
            {up.newer ? <>{t("nodeUpdateNewer")} (<bdi dir="ltr">{up.panel}</bdi>)</>
              : up.via ? <>{t("nodeUpdateReady")} <bdi dir="ltr" className="font-medium">{up.panel}</bdi>{up.via === "github" ? <span className="text-ink3"> · {t("nodeUpdateGithub")}</span> : null}</>
              : t(up.reason === "dev" ? "nodeUpdateDev" : "nodeUpdateOld")}
          </span>
          {owner && up.via && !up.newer && (
            <Button size="sm" variant="primary" loading={busy === "update"}
              onClick={() => act("update", api(`nodes/${x.id}/update`, { method: "POST" }), t("nodeUpdated"))}>{t("nodeUpdate")}</Button>
          )}
        </div>
      )}
      {ns?.certError && x.domain && (
        <div className="mb-4 flex gap-2 rounded-xl border border-warn/40 bg-warn/[.08] px-3 py-2 text-[12.5px]">
          <TriangleAlert className="size-4 text-warn shrink-0 mt-0.5" />
          <span className="min-w-0">{t("nodeCertError")} <bdi dir="ltr" className="break-words text-ink2">{ns.certError}</bdi></span>
        </div>
      )}
      {ns ? (
        <>
          <Rings cpu={ns.cpu} mem={pct(ns.memUsed, ns.memTotal)} disk={pct(ns.diskUsed, ns.diskTotal)} />
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-5">
            <KV label={t("uptime")}>{duration(ns.uptime, lang)}</KV>
            <KV label={t("lastContact")}>{x.lastSeen ? ago(x.lastSeen, lang) : "—"}</KV>
            <KV label={t("bandwidth")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(x.rateDown, lang)}</span><span>↑ {speed(x.rateUp, lang)}</span></span></KV>
            <KV label={t("netNow")}><span className="num flex flex-wrap gap-x-2 whitespace-normal"><span>↓ {speed(ns.netDown, lang)}</span><span>↑ {speed(ns.netUp, lang)}</span></span></KV>
            {isRelay ? (
              <>
                <KV label={t("relayPorts")}><span className="num">{n(ns.forwards?.length ?? 0)}</span></KV>
                <KV label={t("relayConns")}><span className="num">{n((ns.forwards ?? []).reduce((a, f) => a + f.conns, 0))}</span></KV>
              </>
            ) : (
              <>
                <KV label={t("users")}><span className="num">{n(ns.users)}</span></KV>
                <KV label={t("protocols")}><span className="num">{n(x.inbounds)}</span></KV>
              </>
            )}
            <KV label={t("ram")}><span className="num">{bytes(ns.memUsed, lang)} / {bytes(ns.memTotal, lang)}</span></KV>
            {isRelay
              ? <KV label={t("relayShift")}><span className="num" dir="ltr">{x.relayShift > 0 ? "+" : ""}{n(x.relayShift)}</span></KV>
              : <KV label={t("nodeCert")}>{ns.realCert
                  ? <span className="text-online">Let's Encrypt{ns.certExpiry ? <span className="text-ink3"> · {t("nodeCertUntil")} {date(ns.certExpiry, lang)}</span> : null}</span>
                  : ns.certBusy ? <span className="text-ink2">{t("nodeCertIssuing")}</span>
                  : <span className="text-ink2">{t("nodeCertSelf")} <span className="mono text-[11px] text-ink3" dir="ltr">{ns.certSha256 ? ns.certSha256.slice(0, 8) + "…" : ""}</span></span>}</KV>}
          </div>
          {isRelay && x.relayMode === "reverse" && <TunnelState x={x} />}
          {isRelay ? <RelayPorts list={ns.forwards ?? []} /> : (
            <div className="flex flex-wrap gap-2">
              {ns.cores.map((c) => (
                <Badge key={c.name} color={c.running ? "var(--ok)" : "var(--text3)"}><Dot color={c.running ? "var(--ok)" : "var(--text3)"} size={6} /><span dir="ltr">{c.name} {c.version || "—"}</span></Badge>
              ))}
            </div>
          )}
          {coreErr.length > 0 && (
            <ul className="mt-3 flex flex-col gap-1 text-[12px] text-bad">
              {coreErr.map(([k, v]) => <li key={k}><ErrText raw={v.includes(k) ? v : `${k}: ${v}`} compact /></li>)}
            </ul>
          )}
        </>
      ) : (
        <p className="text-ink3 text-[13px] py-6 text-center">{t("nodeNoStatus")}</p>
      )}
      {owner && (
        <div className="mt-5 pt-4 border-t border-hair flex justify-end">
          <ConfirmButton icon={<Trash2 className="size-3.5" />} title={t("removeServer")} body={t("removeServerBody")} onConfirm={() => act("del", api(`nodes/${x.id}`, { method: "DELETE" }))}>{t("removeServer")}</ConfirmButton>
        </div>
      )}

      <Dialog open={renaming} onClose={() => setRenaming(false)} title={t("rename")}
        footer={<><Button variant="quiet" onClick={() => setRenaming(false)}>{t("cancel")}</Button>
          <Button variant="primary" disabled={!name.trim()} loading={busy === "name"} onClick={() => { act("name", api(`nodes/${x.id}`, { method: "PUT", body: { name: name.trim() } }), t("saved")); setRenaming(false); }}>{t("save")}</Button></>}>
        <label><Label>{t("serverName")}</Label><Input value={name} maxLength={32} onChange={(e) => setName(e.target.value)} dir="auto" /></label>
      </Dialog>
      {isRelay && <RelaySettings open={relaySet} node={x} servers={servers.filter((s) => s.id !== x.id)} onClose={() => setRelaySet(false)}
        onDone={() => { setRelaySet(false); onChange(); }} />}
      <Dialog open={domainSet} onClose={() => setDomainSet(false)} title={<>{t("nodeDomainTitle")} · <bdi dir="ltr">{x.name}</bdi></>}
        footer={<><Button variant="quiet" onClick={() => setDomainSet(false)}>{t("cancel")}</Button>
          <Button variant="primary" loading={busy === "domain"} onClick={() => { act("domain", api(`nodes/${x.id}`, { method: "PUT", body: { domain: domain.trim() } }), t("saved")); setDomainSet(false); }}>{t("save")}</Button></>}>
        <label>
          <Label>{t("nodeDomain")}</Label>
          <Input dir="ltr" className="mono" value={domain} placeholder="de.example.com" onChange={(e) => setDomain(e.target.value)} />
          <span className="block text-[12.5px] text-ink3 leading-6 mt-2">{t("nodeDomainHint")}</span>
        </label>
      </Dialog>
    </Panel>
  );
}

/** A reverse relay's tunnel: whether the target server keeps its links to it. */
function TunnelState({ x }: { x: NodeView }) {
  const { t, lang, n } = useI18n();
  const tun = x.status?.tunnel;
  const links = tun?.links ?? 0;
  const err = tun?.error || (links === 0 ? x.tunnelError : "");
  return (
    <div className={cx("mb-4 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-2xl border px-4 py-3 text-[13px]",
      links > 0 ? "border-online/30 bg-online/[.06]" : "border-warn/40 bg-warn/[.08]")}>
      <ArrowRightLeft className={cx("size-4 shrink-0", links > 0 ? "text-online" : "text-warn")} />
      <span className="font-semibold">{t("relayTunnel")}</span>
      <span className="text-ink3 num" dir="ltr">:{tun?.port ?? x.relayPort}</span>
      {links > 0
        ? <span className="num">{n(links)} {t("relayTunnelLinks")} <bdi dir="ltr" className="mono sensitive">{tun?.peer}</bdi>{tun?.since ? <span className="text-ink3"> · {ago(tun.since, lang)}</span> : null}</span>
        : <span className="text-warn">{t("relayTunnelDown")}</span>}
      {err && <bdi dir="ltr" className="basis-full text-[12px] text-bad break-words">{err}</bdi>}
    </div>
  );
}

/** A relay's forwarded ports: where each goes, whether it listens, what it carries. */
function RelayPorts({ list }: { list: RelayStat[] }) {
  const { t, lang, n } = useI18n();
  if (list.length === 0) return <p className="text-ink3 text-[13px] py-3">{t("relayNone")}</p>;
  return (
    <div className="rounded-2xl border border-hair overflow-x-auto">
      <table className="w-full text-[12.5px]">
        <thead className="text-ink3 text-[11.5px]">
          <tr className="border-b border-hair">
            {[t("port"), t("relayTarget"), t("status"), t("relayConns"), t("traffic"), t("relayRtt")].map((h) => <th key={h} className="text-start font-medium px-3 h-9 whitespace-nowrap">{h}</th>)}
          </tr>
        </thead>
        <tbody>
          {list.map((f) => (
            <tr key={f.listen + f.network} className="border-b border-hair last:border-0 align-top">
              <td className="px-3 py-2"><span className="num font-semibold" dir="ltr">{f.listen}</span> <span className="text-ink3 text-[11px] uppercase" dir="ltr">{f.network}</span>
                <div className="text-[11.5px] text-ink3 max-w-48 truncate" title={f.name}>{f.name}</div></td>
              <td className="px-3 py-2 mono text-[11.5px] sensitive" dir="ltr">{f.target}</td>
              <td className="px-3 py-2">{f.listening
                ? <Badge color="var(--ok)"><Dot color="var(--ok)" size={6} />{t("relayListening")}</Badge>
                : <span className="text-bad text-[11.5px]"><bdi dir="ltr" className="break-words">{f.error || "—"}</bdi></span>}</td>
              <td className="px-3 py-2 num">{n(f.conns)}</td>
              <td className="px-3 py-2 num whitespace-nowrap">↓ {bytes(f.down, lang)} · ↑ {bytes(f.up, lang)}</td>
              <td className="px-3 py-2 num whitespace-nowrap">{f.rttMs > 0 ? `${n(f.rttMs)} ms` : f.rttMs < 0 ? <span className="text-bad">{t("relayUnreachable")}</span> : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Where a relay points, its port shift, and whether users get only its configs. */
function RelaySettings({ open, node: x, servers, onClose, onDone }: { open: boolean; node: NodeView; servers: NodeView[]; onClose: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [f, setF] = useState<RelayForm>(relayForm(x));
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) setF(relayForm(x)); }, [open, x]);
  const save = () => {
    setBusy(true);
    api(`nodes/${x.id}`, { method: "PUT", body: relayBody(f) })
      .then(() => { toast(t("saved")); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  return (
    <Dialog open={open} onClose={onClose} title={<>{t("relaySettings")} · <bdi dir="ltr">{x.name}</bdi></>}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button><Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button></>}>
      <RelayFields servers={servers} f={f} set={setF} />
    </Dialog>
  );
}

/** What a relay form edits. */
interface RelayForm { target: string; shift: string; only: boolean; mode: "forward" | "reverse"; port: string }

const relayForm = (x?: NodeView): RelayForm => ({
  target: String(x?.relayTarget ?? 0), shift: String(x?.relayShift ?? 0), only: x?.relayOnly ?? false,
  mode: x?.relayMode === "reverse" ? "reverse" : "forward", port: x?.relayPort ? String(x.relayPort) : "8443",
});

const relayBody = (f: RelayForm) => ({
  relayTarget: Number(f.target), relayShift: Number(f.shift) || 0, relayOnly: f.only, relayMode: f.mode,
  ...(f.mode === "reverse" ? { relayPort: Number(f.port) || 0 } : {}),
});

function RelayFields({ servers, f, set }: { servers: NodeView[]; f: RelayForm; set: (f: RelayForm) => void }) {
  const { t } = useI18n();
  const up = <K extends keyof RelayForm>(k: K, v: RelayForm[K]) => set({ ...f, [k]: v });
  return (
    <div className="flex flex-col gap-4">
      <label><Label>{t("relayTarget")}</Label>
        <Select value={f.target} onChange={(v) => up("target", v)} options={[{ value: "0", label: t("thisServer") }, ...servers.map((s) => ({ value: String(s.id), label: s.name }))]} /></label>
      <div>
        <Label>{t("relayMode")}</Label>
        <Segmented value={f.mode} onChange={(v) => up("mode", v)} options={[{ value: "forward", label: t("relayModeForward") }, { value: "reverse", label: t("relayModeReverse") }]} />
        <p className="text-[12.5px] text-ink3 mt-2">{f.mode === "reverse" ? t("relayModeReverseHint") : t("relayModeForwardHint")}</p>
      </div>
      {f.mode === "reverse" && (
        <label><Label hint={t("relayTunnelPortHint")}>{t("relayTunnelPort")}</Label>
          <Input type="number" min={1} max={65535} dir="ltr" className="num max-w-40" value={f.port} onChange={(e) => up("port", e.target.value)} /></label>
      )}
      <label><Label hint={t("relayShiftHint")}>{t("relayShift")}</Label>
        <Input type="number" min={-60000} max={60000} dir="ltr" className="num max-w-40" value={f.shift} onChange={(e) => up("shift", e.target.value)} /></label>
      <Toggle checked={f.only} onChange={(v) => up("only", v)} label={t("relayOnlyLabel")} hint={t("relayOnlyHint")} />
    </div>
  );
}

/** Two steps: install the node with one command, paste its token. */
function AddServerDialog({ open, servers, onClose, onDone }: { open: boolean; servers: NodeView[]; onClose: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [role, setRole] = useState<"server" | "relay">("server");
  const [rf, setRf] = useState<RelayForm>(relayForm());
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) { setName(""); setToken(""); setRole("server"); setRf(relayForm()); setDomain(""); } }, [open]);
  const add = () => {
    setBusy(true);
    const relay = role === "relay" ? { role, ...relayBody(rf) } : { domain: domain.trim() };
    api<NodeView>("nodes", { body: { name: name.trim(), token: token.trim(), ...relay } })
      .then((x) => { x.paired ? toast(t("serverAdded")) : toast(x.error || t("nodePairing"), "error"); onDone(); })
      .catch((e) => toast(e.message, "error"))
      .finally(() => setBusy(false));
  };
  return (
    <Dialog open={open} onClose={onClose} wide title={t("addServer")}
      footer={<><Button variant="quiet" onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" icon={<Plus className="size-4" />} loading={busy} disabled={!name.trim() || !token.trim().startsWith("dvxnode1.")} onClick={add}>{t("addServer")}</Button></>}>
      <div className="flex flex-col gap-4 mt-1">
        <div>
          <Label>1 · {t("addServerStep1")}</Label>
          <CopyField value={INSTALL_NODE} sensitive={false} />
        </div>
        <div>
          <Label>2 · {t("addServerStep2")}</Label>
          <Textarea value={token} onChange={(e) => setToken(e.target.value)} rows={3} dir="ltr" className="mono text-[12px]" placeholder="dvxnode1.…" spellCheck={false} />
        </div>
        <label><Label>{t("serverName")}</Label><Input value={name} maxLength={32} placeholder={role === "relay" ? "Tehran" : "Frankfurt"} onChange={(e) => setName(e.target.value)} dir="auto" /></label>
        <div>
          <Segmented value={role} onChange={setRole} options={[{ value: "server", label: t("roleServer") }, { value: "relay", label: t("roleRelay") }]} />
          <p className="text-[12.5px] text-ink3 mt-2">{role === "relay" ? t("roleRelayHint") : t("roleServerHint")}</p>
        </div>
        {role === "relay" && <RelayFields servers={servers} f={rf} set={setRf} />}
        {role === "server" && (
          <label>
            <Label>{t("nodeDomain")} <span className="text-ink3 font-normal">· {t("optional")}</span></Label>
            <Input dir="ltr" className="mono" value={domain} placeholder="de.example.com" onChange={(e) => setDomain(e.target.value)} />
            <span className="block text-[12.5px] text-ink3 leading-6 mt-1.5">{t("nodeDomainHint")}</span>
          </label>
        )}
        <p className="text-[12px] text-ink3 inline-flex items-start gap-1.5"><ShieldCheck className="size-4 shrink-0 text-primary2" />{t("nodeSecure")}</p>
      </div>
    </Dialog>
  );
}
