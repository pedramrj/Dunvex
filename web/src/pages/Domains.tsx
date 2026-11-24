import { useCallback, useEffect, useRef, useState, type DragEvent, type ReactNode } from "react";
import {
  Activity, ArrowDown, ArrowUp, Cloud, CloudCog, Crown, Globe2, MoreHorizontal, Pencil, Plus, RefreshCw, Search, Server, ShieldCheck, Trash2, Wand2,
} from "lucide-react";
import { api } from "../lib/api";
import { ago, cx, date } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import {
  Badge, Button, Chip, ConfirmButton, CopyField, Dialog, Empty, ErrorState, IconButton, Input, Label, Menu, Panel, Segmented, Select, Sheet, Skeleton, Toggle,
  useConfirm, useToast, type MenuItem,
} from "../components/ui";
import {
  CertWord, CoverLine, DomainName, DomainRow, Fact, Issue, MainBadge, RecordLine, RoleBadge, SheetSection, StateWord, toneColor, type Role, type Tone,
} from "../components/domains";
import { CDNAssistant, type Address } from "./CDN";
import { PageHead, navigate } from "../App";

// ---------------------------------------------------------------- types

interface Addr { ip: string; here: boolean; cloudflare: boolean }
interface DnsCheck {
  name: string; a: Addr[]; aaaa: Addr[]; cname?: string; ns?: string[]; zone?: string; onCloudflare: boolean;
  points: "here" | "cloudflare" | "elsewhere" | "mixed" | "none"; status: "ok" | "warn" | "bad"; problems: string[]; error?: string; at: number;
}
interface Use { kind: string; count?: number; names?: string[] }
interface CertState { wanted: boolean; covered: boolean; code?: string; detail?: string; failedAt?: number; retryAt?: number }
interface Domain { name: string; role: Role; label?: string; configs: boolean; all?: boolean; cert: boolean; dns01?: boolean; addresses?: Address[]; note?: string; added?: number }
interface Row extends Domain { main: boolean; check?: DnsCheck; certState: CertState; uses: Use[]; configCount: number; registered?: string }
interface CertInfo { names: string[]; ips: string[]; issuer: string; notBefore: string; notAfter: string; selfSigned: boolean; key: string }
interface CertRun { at: number; ok: boolean; names?: string[]; failed?: Record<string, { code: string; detail: string }>; error?: string; rateLimited?: boolean; notAfter?: number; secs: number }
interface CertView {
  mode: "acme" | "custom" | "self"; ca: string; email: string; file: string; keyFile: string; names: string[]; ip?: string; have?: CertInfo; due: boolean;
  missing: string[]; renewAt?: number; running: boolean; started?: number; working?: string[]; last?: CertRun; retryAt?: number; port80?: string; selfSigned: boolean;
}
interface NodeDomain { id: number; name: string; domain: string; address: string; realCert: boolean; certExpiry?: number; online: boolean; check?: DnsCheck }
interface RDAP { expires?: number; registrar?: string; at: number; error?: string }
interface State {
  domains: Row[]; cdnDirect: boolean; ipCert: boolean; problems: number;
  server: { ipv4: string[]; ipv6: string[]; publicIp: string; host: string; linkHost: string; panelPort: number; panelTls: boolean; subBase: string };
  cert: CertView; cloudflare: { saved: boolean; hint?: string }; nodes: NodeDomain[]; rdap: Record<string, RDAP>;
}
interface PointResult { result: { done: boolean; conflicts?: { type: string; name: string; content: string; proxied: boolean }[] }; state: State }

const roles: Role[] = ["direct", "cdn", "sub"];
const fixable = ["notFound", "notHere", "proxied", "notProxied", "notCDN", "someElsewhere", "v6Elsewhere"];

/** The fields of a domain as saved (a row carries more). */
const bare = (d: Domain): Domain => ({ name: d.name, role: d.role, label: d.label, configs: d.configs, all: d.all, cert: d.cert, dns01: d.dns01, addresses: d.addresses, note: d.note, added: d.added });
const sec = (iso?: string) => (iso ? Math.floor(Date.parse(iso) / 1000) : 0);
const now = () => Math.floor(Date.now() / 1000);

function useFmt() {
  const { t, n, lang } = useI18n();
  const tx = (k: string, v: Record<string, string | number> = {}) => {
    let s = t(k as Key);
    for (const [a, b] of Object.entries(v)) s = s.replace("{" + a + "}", typeof b === "number" ? n(b) : b);
    return s;
  };
  const left = (unix: number) => {
    const s = unix - now();
    if (s <= 0) return t("dmExpired");
    if (s < 2 * 86400) return tx("dmHoursLeft", { n: Math.floor(s / 3600) });
    return tx("dmDaysLeft", { n: Math.floor(s / 86400) });
  };
  const leftTone = (unix: number, life: number): Tone => {
    const s = unix - now();
    if (s <= 0) return "bad";
    const short = life < 10 * 86400;
    if (s < (short ? 86400 : 3 * 86400)) return "bad";
    if (s < (short ? 2 * 86400 : 14 * 86400)) return "warn";
    return "ok";
  };
  return { t, n, lang, tx, left, leftTone };
}

// ---------------------------------------------------------------- the page

/** Domains: every name of the panel, its DNS, its certificate and what uses it (owner only). */
export function DomainsPage({ arg }: { arg?: string }) {
  const { t } = useFmt();
  const toast = useToast();
  const confirm = useConfirm();
  const [st, setSt] = useState<State | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [drag, setDrag] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const autoChecked = useRef(false);

  const load = useCallback(() => api<State>("domains").then((x) => { setSt(x); setErr(""); return x; }).catch((e) => { setErr(e.message); return null; }), []);

  const check = useCallback((names?: string[]) => {
    setBusy(names?.length === 1 ? "check:" + names[0] : "check");
    return api<{ state: State }>("domains/check", { method: "POST", body: { names: names ?? [] } })
      .then((x) => setSt(x.state)).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  }, [toast]);

  useEffect(() => {
    load().then((x) => {
      // names never checked, or checked long ago: look them up now
      if (!x || autoChecked.current) return;
      autoChecked.current = true;
      if (x.domains.some((d) => !d.check || now() - d.check.at > 600) || x.nodes.some((d) => !d.check)) check();
      if (arg === "cdn") {
        const c = x.domains.find((d) => d.role === "cdn");
        if (c) setOpen(c.name);
      }
    });
  }, [load, check, arg]);

  // follow a running issuance
  useEffect(() => {
    if (!st?.cert.running) return;
    const id = setInterval(() => {
      api<CertView>("domains/cert").then((c) => {
        if (!c.running) load();
        else setSt((s) => (s ? { ...s, cert: c } : s));
      }).catch(() => {});
    }, 2000);
    return () => clearInterval(id);
  }, [st?.cert.running, load]);

  const save = useCallback((list: Domain[], extra: Record<string, unknown> = {}, quiet = false) => {
    if (!st) return Promise.resolve(false);
    setBusy("save");
    return api<State>("domains", { method: "PUT", body: { domains: list.map(bare), cdnDirect: st.cdnDirect, ipCert: st.ipCert, ...extra } })
      .then((x) => {
        setSt(x);
        if (!quiet) toast(t("dmSaved"));
        // a new name in the certificate: the issuance starts by itself in a moment
        setTimeout(() => api<CertView>("domains/cert").then((c) => setSt((s) => (s ? { ...s, cert: c } : s))).catch(() => {}), 4500);
        return true;
      })
      .catch((e) => { toast(e.message, "error"); return false; })
      .finally(() => setBusy(""));
  }, [st, t, toast]);

  const point = useCallback(async (name: string, replace = false): Promise<void> => {
    setBusy("point:" + name);
    try {
      const x = await api<PointResult>("domains/point", { method: "POST", body: { name, replace } });
      setSt(x.state);
      if (x.result.conflicts?.length && !replace) {
        const list = x.result.conflicts.map((r) => `${r.type} ${r.name} → ${r.content}${r.proxied ? " (proxied)" : ""}`).join("\n");
        if (await confirm({ title: t("dmFixConflict"), body: list, danger: true })) return point(name, true);
        return;
      }
      if (x.result.done) {
        toast(t("dmFixDone"));
        setTimeout(() => check([name]), 6000);
      }
    } catch (e: any) {
      toast(e.message, "error");
    } finally {
      setBusy("");
    }
  }, [check, confirm, t, toast]);

  if (!st) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-96" />;

  const list = st.domains;
  const move = (from: number, to: number) => {
    if (to < 0 || to >= list.length || from === to) return;
    const next = [...list];
    const [d] = next.splice(from, 1);
    next.splice(to, 0, d);
    setSt({ ...st, domains: next });
    save(next, {}, true);
  };
  const remove = (name: string) => save(list.filter((d) => d.name !== name));
  const row = open ? list.find((d) => d.name === open) ?? null : null;

  const dragProps = (i: number) => ({
    onDragStart: (e: DragEvent) => { setDrag(i); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", list[i].name); },
    onDragOver: (e: DragEvent) => { if (drag === null) return; e.preventDefault(); setOver(i); },
    onDrop: (e: DragEvent) => { e.preventDefault(); if (drag !== null) move(drag, i); setDrag(null); setOver(null); },
    onDragEnd: () => { setDrag(null); setOver(null); },
  });

  return (
    <>
      <PageHead title={t("domains")} sub={t("dmSub")} action={<>
        <Button icon={<RefreshCw className="size-4" />} loading={busy === "check"} disabled={!list.length && !st.nodes.length} onClick={() => check()}>{t("dmCheckAll")}</Button>
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>{t("dmAdd")}</Button>
      </>} />

      <div className="grid gap-[var(--gap)] xl:grid-cols-[minmax(0,1fr)_380px] items-start">
        <div className="flex flex-col gap-[var(--gap)] min-w-0">
          <StatusPanel st={st} busy={busy} onOpen={setOpen} onPoint={point} onRenew={() => renew(st, setSt, toast)} />

          <Panel icon={<Globe2 className="size-4" />} title={t("dmNames")} sub={list.length > 1 ? t("dmNamesHint") : undefined}>
            {list.length === 0 ? (
              <Empty icon={<Globe2 className="size-7" />} title={t("dmEmpty")} hint={t("dmEmptyHint")}
                action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>{t("dmAdd")}</Button>} />
            ) : (
              <ol className="flex flex-col gap-2">
                {list.map((d, i) => (
                  <NameRow key={d.name} d={d} cert={st.cert} cfSaved={st.cloudflare.saved} busy={busy} first={i === 0} last={i === list.length - 1}
                    dragging={drag === i} over={over === i && drag !== i} drag={dragProps(i)}
                    onOpen={() => setOpen(d.name)} onMove={(dir) => move(i, i + dir)} onMain={() => move(i, 0)}
                    onCheck={() => check([d.name])} onPoint={() => point(d.name)} onRemove={() => remove(d.name)} />
                ))}
              </ol>
            )}
          </Panel>

          {st.nodes.length > 0 && <NodesPanel nodes={st.nodes} />}
        </div>

        <div className="flex flex-col gap-[var(--gap)] min-w-0">
          <CertPanel st={st} setSt={setSt} save={save} />
          <CloudflarePanel st={st} setSt={setSt} />
          <LinksPanel st={st} save={save} />
        </div>
      </div>

      {row && <DomainSheet key={row.name} row={row} st={st} busy={busy} onClose={() => setOpen(null)} reload={load}
        onSave={(d) => save(list.map((x) => (x.name === row.name ? d : x)), d.cert && st.cert.mode === "self" && st.cert.names.length === 0 ? { certMode: "acme" } : {}).then((ok) => { if (ok) setOpen(d.name); })}
        onRemove={() => { remove(row.name); setOpen(null); }} onCheck={() => check([row.name])} onPoint={() => point(row.name)} />}
      <AddDialog open={adding} st={st} onClose={() => setAdding(false)}
        onAdd={(d) => {
          // the first name in the certificate turns Let's Encrypt on: the main domain comes along
          const auto = d.cert && st.cert.mode === "self";
          const next = auto ? list.map((x) => (x.main && x.role === "direct" ? { ...x, cert: true } : x)) : list;
          save([...next, d], auto ? { certMode: "acme" } : {}).then((ok) => { if (ok) { setAdding(false); check([d.name]); } });
        }} />
    </>
  );

}

function renew(st: State, setSt: (s: State) => void, toast: (m: string, k?: "ok" | "error" | "info") => void) {
  api<CertView>("domains/cert", { method: "POST" }).then((c) => setSt({ ...st, cert: c })).catch((e) => toast(e.message, "error"));
}

// ---------------------------------------------------------------- status

/** What the names add up to, and what needs the owner (with the fix). */
function StatusPanel({ st, busy, onOpen, onPoint, onRenew }: { st: State; busy: string; onOpen: (n: string) => void; onPoint: (n: string) => void; onRenew: () => void }) {
  const { t, tx, left, leftTone, lang } = useFmt();
  const c = st.cert;
  const main = st.domains.find((d) => d.main);
  const have = c.have && !c.selfSigned && c.mode !== "self" ? c.have : undefined;
  const end = have ? sec(have.notAfter) : 0;
  const life = have ? end - sec(have.notBefore) : 0;
  const issues: ReactNode[] = [];
  for (const d of st.domains) {
    const ch = d.check;
    if (ch?.status === "bad") {
      const p = ch.problems[0] ?? "lookup";
      const can = st.cloudflare.saved && fixable.includes(p);
      issues.push(<Issue key={"dns" + d.name} tone={p === "lookup" ? "warn" : "bad"} title={<><DomainName name={d.name} className="font-medium" /> · {t(("dmProb_" + p) as Key)}</>}
        action={can ? <Button size="sm" variant="soft" icon={<Wand2 className="size-3.5" />} loading={busy === "point:" + d.name} onClick={() => onPoint(d.name)}>{t("dmFix")}</Button>
          : <Button size="sm" onClick={() => onOpen(d.name)}>{t("edit")}</Button>} />);
    }
    if (d.certState.wanted && !d.certState.covered && d.certState.code) {
      issues.push(<Issue key={"cert" + d.name} tone="warn" title={<><DomainName name={d.name} className="font-medium" /> · {t("dmCertFailed")}</>}
        detail={t(("dmCode_" + d.certState.code) as Key)} action={<Button size="sm" onClick={() => onOpen(d.name)}>{t("edit")}</Button>} />);
    }
  }
  if (have && leftTone(end, life) !== "ok") {
    issues.push(<Issue key="end" tone={leftTone(end, life)} title={<>{t("dmCertTitle")} · {left(end)}</>} detail={c.last && !c.last.ok ? c.last.error : undefined}
      action={c.mode === "acme" ? <Button size="sm" variant="soft" loading={c.running} onClick={onRenew}>{t("dmRenewNow")}</Button> : undefined} />);
  }
  for (const [reg, r] of Object.entries(st.rdap ?? {})) {
    if (r.expires && r.expires - now() < 30 * 86400) {
      issues.push(<Issue key={"reg" + reg} tone={r.expires - now() < 7 * 86400 ? "bad" : "warn"} title={<><DomainName name={reg} className="font-medium" /> · {tx("dmRegExpires", { d: date(r.expires, lang) })}</>}
        detail={r.registrar} />);
    }
  }
  const bad = st.domains.filter((d) => d.check?.status === "bad").length;
  const unchecked = st.domains.filter((d) => !d.check).length;
  return (
    <Panel pad>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-x-6 gap-y-3">
        <Fact label={t("dmMain")} tone={main ? "ok" : "idle"}>{main ? <DomainName name={main.name} truncate /> : t("dmNoMain")}</Fact>
        <Fact label={t("dmCertTitle")} tone={have ? leftTone(end, life) : c.running ? "busy" : "idle"}>
          {c.running ? t("dmRenewing") : have ? <>{have.issuer || "—"} · {left(end)}</> : t(("dmCertMode_" + c.mode) as Key)}
        </Fact>
        <Fact label={t("dmDnsTitle")} tone={busy === "check" ? "busy" : bad ? "bad" : unchecked ? "idle" : "ok"}>
          {busy === "check" ? t("dmChecking") : bad ? tx("dmNeeds", { n: bad }) : unchecked ? t("dmDnsNot") : t("dmAllGood")}
        </Fact>
        <Fact label={t("dmRenewsOn")}>{c.renewAt ? date(c.renewAt, lang) : "—"}</Fact>
      </div>
      {issues.length > 0 && (
        <div className="mt-4 pt-4 border-t border-hair">
          <h3 className="text-[13px] font-semibold text-ink mb-3">{t("dmNeedsTitle")}</h3>
          <ul>{issues}</ul>
        </div>
      )}
    </Panel>
  );
}

// ---------------------------------------------------------------- one row

function dnsTone(c?: DnsCheck): Tone {
  return !c ? "idle" : c.status === "ok" ? "ok" : c.status === "warn" ? "warn" : c.problems[0] === "lookup" ? "warn" : "bad";
}

/** Where a name points, in one word ("no answer" when the lookup itself failed). */
const pointsKey = (c: DnsCheck): Key => (c.problems[0] === "lookup" ? "dmPoints_noanswer" : (("dmPoints_" + c.points) as Key));

function certOf(d: Row, cert: CertView): { tone: Tone; key: Key } {
  if (cert.running && cert.working?.includes(d.name)) return { tone: "busy", key: "dmRenewingNames" };
  if (!d.cert) return d.role === "cdn" ? { tone: "idle", key: "dmCertCdn" } : { tone: "idle", key: "dmCertNo" };
  if (cert.mode !== "acme") return { tone: "idle", key: ("dmCertMode_" + cert.mode) as Key };
  if (d.certState.covered) return { tone: "ok", key: "dmCertIn" };
  if (d.certState.code) return { tone: "bad", key: "dmCertFailed" };
  return { tone: "warn", key: "dmCertWaiting" };
}

function NameRow({ d, cert, cfSaved, busy, first, last, dragging, over, drag, onOpen, onMove, onMain, onCheck, onPoint, onRemove }: {
  d: Row; cert: CertView; cfSaved: boolean; busy: string; first: boolean; last: boolean; dragging: boolean; over: boolean;
  drag: Parameters<typeof DomainRow>[0]["drag"]; onOpen: () => void; onMove: (d: -1 | 1) => void; onMain: () => void; onCheck: () => void; onPoint: () => void; onRemove: () => void;
}) {
  const { t, tx } = useFmt();
  const confirm = useConfirm();
  const c = certOf(d, cert);
  const items: MenuItem[] = [
    { label: t("edit"), icon: <Pencil />, onClick: onOpen },
    { label: t("dmCheck"), icon: <Search />, onClick: onCheck },
    ...(cfSaved && d.role !== "sub" ? [{ label: t("dmFixCF"), icon: <Wand2 />, onClick: onPoint }] : []),
    { divider: true, label: "" },
    ...(!first ? [{ label: t("dmMoveUp"), icon: <ArrowUp />, onClick: () => onMove(-1), hint: "Alt ↑" }] : []),
    ...(!last ? [{ label: t("dmMoveDown"), icon: <ArrowDown />, onClick: () => onMove(1), hint: "Alt ↓" }] : []),
    ...(d.role === "direct" && !d.main ? [{ label: t("dmMakeMain"), icon: <Crown />, onClick: onMain }] : []),
    { divider: true, label: "" },
    { label: t("delete"), icon: <Trash2 />, danger: true, onClick: async () => { if (await confirm({ title: t("delete") + " " + d.name, body: d.main ? t("dmDeleteMain") : t("dmDeleteBody"), danger: true })) onRemove(); } },
  ];
  const usesWords = d.uses.filter((u) => u.kind !== "protocol").slice(0, 2).map((u) => t(("dmUse_" + u.kind) as Key));
  return (
    <DomainRow name={d.name} label={d.label} dragging={dragging} over={over} drag={drag} onOpen={onOpen} onMove={onMove}
      badges={<><RoleBadge role={d.role} />{d.main && <MainBadge />}</>}
      uses={(d.configCount > 0 || usesWords.length > 0) && <>
        {d.label && <span aria-hidden>·</span>}
        {d.configCount > 0 && <span>{tx("dmConfigCount", { n: d.configCount })}</span>}
        {usesWords.map((w) => <Chip key={w} size="sm">{w}</Chip>)}
      </>}
      dns={<StateWord tone={busy === "check:" + d.name || busy === "check" ? "busy" : dnsTone(d.check)}>
        {d.check ? t(pointsKey(d.check)) : t("dmDnsNot")}
      </StateWord>}
      cert={<CertWord tone={c.tone}>{c.key === "dmRenewingNames" ? t("dmRenewing") : t(c.key)}</CertWord>}
      menu={<Menu items={items} trigger={(o) => <IconButton label={t("edit")} tip={false} onClick={o}><MoreHorizontal className="size-[18px]" /></IconButton>} />} />
  );
}

// ---------------------------------------------------------------- the sheet

function DomainSheet({ row, st, busy, onClose, onSave, onRemove, onCheck, onPoint, reload }: {
  row: Row; st: State; busy: string; onClose: () => void; onSave: (d: Domain) => void; onRemove: () => void; onCheck: () => void; onPoint: () => void; reload: () => void;
}) {
  const { t, tx, lang } = useFmt();
  const [d, setD] = useState<Domain>(() => ({ ...bare(row), addresses: row.addresses ?? [] }));
  const set = <K extends keyof Domain>(k: K, v: Domain[K]) => setD((x) => ({ ...x, [k]: v }));
  const ch = row.check;
  const ip = st.server.ipv4[0] || st.server.publicIp || "—";
  const reg = row.registered ? st.rdap[row.registered] : undefined;
  const c = certOf(row, st.cert);
  const addrs = d.addresses ?? [];
  const setAddr = (i: number, a: Partial<Address>) => set("addresses", addrs.map((x, j) => (j === i ? { ...x, ...a } : x)));
  const mark = (a: Addr): [string, Tone] => (a.here ? [t("dmAddrHere"), "ok"] : a.cloudflare ? [t("dmAddrCF"), d.role === "direct" ? "bad" : "ok"] : [t("dmAddrOther"), "bad"]);
  const changed = JSON.stringify(bare(d)) !== JSON.stringify({ ...bare(row), addresses: row.addresses ?? [] });
  const p = ch?.problems[0];

  return (
    <Sheet open onClose={onClose} wide title={<DomainName name={row.name} />} sub={<span className="inline-flex gap-2 items-center"><RoleBadge role={row.role} />{row.main && <MainBadge />}</span>}
      footer={<>
        <div className="me-auto"><ConfirmButton icon={<Trash2 className="size-4" />} title={t("delete") + " " + row.name} body={row.main ? t("dmDeleteMain") : t("dmDeleteBody")} onConfirm={onRemove}>{t("delete")}</ConfirmButton></div>
        <Button onClick={onClose}>{t("cancel")}</Button>
        <Button variant="primary" disabled={!changed} loading={busy === "save"} onClick={() => onSave({ ...d, addresses: addrs.filter((a) => a.address.trim()) })}>{t("save")}</Button>
      </>}>
      <SheetSection title={t("dmThisName")}>
        <div className="flex flex-col gap-5">
          <div>
            <Segmented value={d.role} onChange={(v) => set("role", v)} options={roles.map((r) => ({ value: r, label: t(("dmRole_" + r) as Key) }))} />
            <p className="text-[12.5px] text-ink3 leading-6 mt-2">{t(("dmRoleHint_" + d.role) as Key)}</p>
          </div>
          <label className="max-w-sm"><Label hint={t("dmLabelHint")}>{t("dmLabel")}</Label><Input value={d.label ?? ""} maxLength={32} placeholder={row.name} onChange={(e) => set("label", e.target.value)} /></label>
          {d.role !== "sub" && (row.main && d.role === row.role
            ? <p className="text-[12.5px] text-ink3">{t("dmConfigsMain")}</p>
            : <Toggle checked={d.configs} onChange={(v) => set("configs", v)} label={t("dmConfigs")} hint={t(("dmConfigsHint_" + d.role) as Key)} />)}
          {d.role === "direct" && (d.configs || row.main) && !row.main && <Toggle checked={!!d.all} onChange={(v) => set("all", v)} label={t("dmAll")} hint={t("dmAllHint")} />}
          {d.role === "cdn" && (
            <div>
              <Label>{t("cdnAddresses")}</Label>
              <p className="text-[12.5px] text-ink3 leading-6 mb-2.5">{t("cdnAddressesHint")}</p>
              <div className="flex flex-col gap-2">
                {addrs.map((a, i) => (
                  <div key={i} className="flex gap-2 items-center">
                    <Input dir="ltr" className="mono flex-[3] min-w-0" placeholder={t("cdnAddressPh")} value={a.address} onChange={(e) => setAddr(i, { address: e.target.value })} />
                    <Input className="flex-[2] min-w-0" placeholder={t("cdnAddressName")} value={a.name} onChange={(e) => setAddr(i, { name: e.target.value })} />
                    <IconButton label={t("delete")} tone="danger" onClick={() => set("addresses", addrs.filter((_, j) => j !== i))}><Trash2 className="size-4" /></IconButton>
                  </div>
                ))}
                <div><Button size="sm" variant="soft" icon={<Plus className="size-4" />} disabled={addrs.length >= 30} onClick={() => set("addresses", [...addrs, { address: "", name: "" }])}>{t("cdnAddAddress")}</Button></div>
              </div>
            </div>
          )}
          <Toggle checked={d.cert} onChange={(v) => set("cert", v)} label={t("dmCert")} hint={d.role === "cdn" ? t("dmCertCdnHint") : t("dmCertHint")} />
          {d.cert && <Toggle checked={!!d.dns01} disabled={!st.cloudflare.saved} onChange={(v) => set("dns01", v)} label={t("dmDns01")} hint={st.cloudflare.saved ? t("dmDns01Hint") : t("dmDns01Need")} />}
          <label><Label>{t("dmNote")}</Label><Input value={d.note ?? ""} maxLength={200} onChange={(e) => set("note", e.target.value)} /></label>
        </div>
      </SheetSection>

      <SheetSection title={t("dmDnsTitle")} action={<Button size="sm" icon={<Search className="size-3.5" />} loading={busy === "check:" + row.name} onClick={onCheck}>{t("dmCheck")}</Button>}>
        {!ch ? <p className="text-[13px] text-ink3">{t("dmDnsNot")}</p> : (
          <div className="flex flex-col gap-3">
            <StateWord tone={dnsTone(ch)}>{t(pointsKey(ch))} · <span className="text-ink3">{t("dmCheckedAgo")} {ago(ch.at, lang)}</span></StateWord>
            <div className="rounded-xl border border-hair px-3.5 py-1.5">
              {ch.a.map((a) => { const [m, tn] = mark(a); return <RecordLine key={a.ip} type="A" value={a.ip} mark={m} tone={tn} />; })}
              {ch.aaaa.map((a) => { const [m, tn] = mark(a); return <RecordLine key={a.ip} type="AAAA" value={a.ip} mark={m} tone={tn} />; })}
              {ch.cname && <RecordLine type="CNAME" value={ch.cname} mark={t("dmAlias")} />}
              {ch.ns?.length ? <RecordLine type="NS" value={ch.ns.join(", ")} mark={ch.onCloudflare ? t("dmZoneCF") : ch.zone} tone={ch.onCloudflare ? "ok" : "idle"} /> : null}
              {!ch.a.length && !ch.aaaa.length && <RecordLine type="A" value="—" mark={t("dmPoints_none")} tone="bad" />}
            </div>
            {ch.problems.map((x) => <p key={x} className="text-[13px] text-ink2 leading-6">{t(("dmProb_" + x) as Key)}</p>)}
            {p && fixable.includes(p) && row.role !== "sub" && (
              <div className="flex flex-col gap-2">
                {st.cloudflare.saved && <div><Button size="sm" variant="soft" icon={<Wand2 className="size-3.5" />} loading={busy === "point:" + row.name} onClick={onPoint}>{t("dmFixCF")}</Button></div>}
                <p className="text-[12.5px] text-ink3 leading-6">{tx("dmFixManual", { ip, proxy: row.role === "cdn" ? t("dmFixManualProxy") : t("dmFixManualDirect") })}</p>
              </div>
            )}
          </div>
        )}
        {reg && (
          <p className="text-[12.5px] text-ink3 mt-3">{t("dmRegistrar")} · <DomainName name={row.registered!} /> · {reg.expires ? tx("dmRegExpires", { d: date(reg.expires, lang) }) : t("dmRegUnknown")}{reg.registrar ? " · " + reg.registrar : ""}</p>
        )}
      </SheetSection>

      <SheetSection title={t("dmCertTitle")}>
        <CertWord tone={c.tone}>{c.key === "dmRenewingNames" ? t("dmRenewing") : t(c.key)}</CertWord>
        {row.certState.code && !row.certState.covered && (
          <div className="mt-3 flex flex-col gap-1.5">
            <p className="text-[13px] text-ink2 leading-6">{t(("dmCode_" + row.certState.code) as Key)}</p>
            {row.certState.detail && <p className="text-[12px] text-ink3 mono break-words" dir="ltr">{row.certState.detail}</p>}
            {row.certState.retryAt ? <p className="text-[12px] text-ink3">{t("dmCertRetry")} · {date(row.certState.retryAt, lang)}</p> : null}
          </div>
        )}
      </SheetSection>

      <SheetSection title={t("dmUses")}>
        {row.uses.length === 0 ? <p className="text-[13px] text-ink3">{t("dmUsesNone")}</p> : (
          <ul className="flex flex-col gap-3">
            {row.uses.map((u, i) => (
              <li key={i} className="min-w-0">
                <div className="text-[13px] text-ink">{t(("dmUse_" + u.kind) as Key)}{u.count ? <span className="text-ink3"> · {tx("dmConfigCount", { n: u.count })}</span> : null}</div>
                {u.names?.length ? <div className="flex flex-wrap gap-1.5 mt-1.5">{u.names.map((x) => <Chip key={x} size="sm">{x}</Chip>)}</div> : null}
              </li>
            ))}
          </ul>
        )}
      </SheetSection>

      {row.role === "cdn" && (
        <SheetSection title={<span className="inline-flex items-center gap-2"><CloudCog className="size-4 text-ink3" />{t("dmCdnTitle")}</span>}>
          <CDNAssistant name={row.name} tokenKept={st.cloudflare.saved} onChanged={reload} />
        </SheetSection>
      )}
    </Sheet>
  );
}

// ---------------------------------------------------------------- adding

function AddDialog({ open, st, onClose, onAdd }: { open: boolean; st: State; onClose: () => void; onAdd: (d: Domain) => void }) {
  const { t, tx } = useFmt();
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("direct");
  const [cert, setCert] = useState(true);
  const [seen, setSeen] = useState<DnsCheck | null>(null);
  const [looking, setLooking] = useState(false);
  useEffect(() => { if (open) { setName(""); setRole("direct"); setCert(true); setSeen(null); } }, [open]);
  const clean = name.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/[/:].*$/, "").replace(/\.$/, "");
  const ip = st.server.ipv4[0] || st.server.publicIp || "—";
  const look = () => {
    if (!clean) return;
    setLooking(true);
    api<{ checks: Record<string, DnsCheck> }>("domains/check", { method: "POST", body: { names: [clean] } })
      .then((x) => {
        const c = x.checks[clean] ?? null;
        setSeen(c);
        if (c?.points === "cloudflare") { setRole("cdn"); setCert(false); }
        else if (c?.points === "here") { setRole("direct"); setCert(true); }
      })
      .catch(() => setSeen(null)).finally(() => setLooking(false));
  };
  const dup = st.domains.some((d) => d.name === clean);
  return (
    <Dialog open={open} onClose={onClose} title={t("dmAdd")} footer={<>
      <Button onClick={onClose}>{t("cancel")}</Button>
      <Button variant="primary" icon={<Plus className="size-4" />} disabled={!clean || dup}
        onClick={() => onAdd({ name: clean, role, cert, configs: role !== "sub", added: 0 })}>{t("dmAdd")}</Button>
    </>}>
      <div className="flex flex-col gap-4 mt-2">
        <div>
          <Label>{t("dmName")}</Label>
          <div className="flex gap-2">
            <Input dir="ltr" className="mono flex-1 min-w-0" autoFocus placeholder={t("dmNamePh")} value={name}
              onChange={(e) => { setName(e.target.value); setSeen(null); }} onBlur={look} onKeyDown={(e) => e.key === "Enter" && look()} />
            <Button icon={<Search className="size-4" />} loading={looking} disabled={!clean} onClick={look}>{t("dmLook")}</Button>
          </div>
          {seen && <p className="text-[12.5px] text-ink2 leading-6 mt-2">{tx("dmSeen_" + seen.points, { ip })}</p>}
        </div>
        <div>
          <Segmented value={role} onChange={(v) => { setRole(v); setCert(v !== "cdn"); }} options={roles.map((r) => ({ value: r, label: t(("dmRole_" + r) as Key) }))} />
          <p className="text-[12.5px] text-ink3 leading-6 mt-2">{t(("dmRoleHint_" + role) as Key)}</p>
        </div>
        <Toggle checked={cert} onChange={setCert} label={t("dmCert")} hint={cert && role !== "cdn" ? t("dmAddFirstCert") : role === "cdn" ? t("dmCertCdnHint") : undefined} />
      </div>
    </Dialog>
  );
}

// ---------------------------------------------------------------- certificate

function CertPanel({ st, setSt, save }: { st: State; setSt: (s: State) => void; save: (l: Domain[], extra?: Record<string, unknown>, quiet?: boolean) => Promise<boolean> }) {
  const { t, tx, n, left, leftTone, lang } = useFmt();
  const toast = useToast();
  const c = st.cert;
  const [more, setMore] = useState(false);
  const [mode, setMode] = useState(c.mode);
  const [ca, setCa] = useState(["letsencrypt", "staging"].includes(c.ca) ? c.ca : "custom");
  const [caUrl, setCaUrl] = useState(["letsencrypt", "staging"].includes(c.ca) ? "" : c.ca);
  const [email, setEmail] = useState(c.email);
  const [files, setFiles] = useState({ cert: c.mode === "custom" ? c.file : "", key: c.mode === "custom" ? c.keyFile : "" });
  useEffect(() => setMode(c.mode), [c.mode]);
  const have = c.have && !c.have.selfSigned ? c.have : undefined;
  const end = have ? sec(have.notAfter) : 0;
  const life = have ? end - sec(have.notBefore) : 0;
  const ipNoName = c.names.length === 0;
  const failed = (n: string) => c.last?.failed?.[n] ?? (st.domains.find((d) => d.name === n)?.certState.code ? { code: st.domains.find((d) => d.name === n)!.certState.code!, detail: "" } : undefined);
  const httpNames = st.domains.filter((d) => d.cert && !d.dns01 && !d.certState.covered);
  const renewNow = () => api<CertView>("domains/cert", { method: "POST" }).then((x) => setSt({ ...st, cert: x })).catch((e) => toast(e.message, "error"));
  const saveMore = () => save(st.domains, { certMode: mode, ca: ca === "custom" ? caUrl.trim() : ca, email, ...(mode === "custom" ? { certFile: files.cert, keyFile: files.key } : {}) });

  return (
    <Panel icon={<ShieldCheck className="size-4" />} title={t("dmCertTitle")} sub={t(("dmCertMode_" + c.mode) as Key)}
      action={have ? <Badge color={toneColor(leftTone(end, life))}>{left(end)}</Badge> : undefined}>
      <div className="flex flex-col gap-4">
        {have && (
          <div className="grid grid-cols-2 gap-x-4 gap-y-2">
            <Fact label={t("dmIssuer")}>{have.issuer || "—"}</Fact>
            <Fact label={t("dmExpires")}>{date(end, lang)}</Fact>
            {c.renewAt ? <Fact label={t("dmRenewsOn")}>{date(c.renewAt, lang)}</Fact> : null}
            <Fact label={t("dmCovers")}>{n(have.names.length + have.ips.length)}</Fact>
          </div>
        )}
        {c.selfSigned && c.mode === "acme" && c.names.length > 0 && <p className="text-[12.5px] text-ink3 leading-6">{t("dmSelfSignedNote")}</p>}

        {(c.mode === "acme" || ipNoName) && (
          <div>
            <div className="text-[11.5px] text-ink3 mb-1.5">{t("dmCovers")}</div>
            {c.names.length === 0 && !c.ip && <p className="text-[12.5px] text-ink3">{t("dmNoCertNames")}</p>}
            {c.names.map((n) => {
              const f = failed(n);
              const inCert = !!have && have.names.includes(n);
              return <CoverLine key={n} name={n} state={inCert ? "in" : f ? "failed" : "missing"} note={!inCert && f ? t(("dmCode_" + f.code) as Key) : !inCert ? t("dmMissing") : undefined} />;
            })}
            {c.ip && <CoverLine name={c.ip} state={have?.ips.includes(c.ip) ? "in" : failed(c.ip) ? "failed" : "missing"} />}
          </div>
        )}

        {c.running && <StateWord tone="busy">{t("dmRenewing")} {c.working?.length ? <bdi dir="ltr" className="mono text-[12px] text-ink3">{c.working.join(", ")}</bdi> : null}</StateWord>}
        {!c.running && c.last && !c.last.ok && c.last.error && !Object.keys(c.last.failed ?? {}).length && <p className="text-[12.5px] text-ink3 leading-6">{t("dmLastFail")} · {ago(c.last.at, lang)} · <span dir="ltr" className="mono text-[12px]">{c.last.error}</span></p>}
        {c.port80 && httpNames.length > 0 && <p className="text-[12.5px] text-warn leading-6">{tx("dmPort80Busy", { who: c.port80 === "?" ? t("dmPort80Other") : c.port80 })}</p>}

        {ipNoName && (
          <Toggle checked={st.ipCert} disabled={!st.server.publicIp && !c.ip} onChange={(v) => save(st.domains, { ipCert: v })}
            label={t("dmIPCert")} hint={st.server.publicIp || c.ip ? tx("dmIPCertHint", { ip: c.ip || st.server.publicIp }) : t("dmIPCertNoIP")} />
        )}

        <div className="flex flex-wrap gap-2">
          {(c.mode === "acme" || c.names.length > 0 || c.ip) && c.mode !== "custom" && (
            <Button variant="soft" icon={<RefreshCw className="size-4" />} loading={c.running} disabled={c.names.length === 0 && !c.ip} onClick={renewNow}>{t("dmRenewNow")}</Button>
          )}
          <Button variant="quiet" onClick={() => setMore(!more)} aria-expanded={more}>{t("advanced")}</Button>
        </div>

        {more && (
          <div className="flex flex-col gap-4 pt-4 border-t border-hair">
            <div>
              <Label>{t("dmCertSource")}</Label>
              <Select value={mode} onChange={(v) => setMode(v as CertView["mode"])} options={(["acme", "custom", "self"] as const).map((m) => ({ value: m, label: t(("dmCertMode_" + m) as Key) }))} />
            </div>
            {mode === "custom" && <>
              <label><Label>{t("dmCertFile")}</Label><Input dir="ltr" className="mono" value={files.cert} placeholder="/root/cert/fullchain.pem" onChange={(e) => setFiles({ ...files, cert: e.target.value })} /></label>
              <label><Label>{t("dmKeyFile")}</Label><Input dir="ltr" className="mono" value={files.key} placeholder="/root/cert/privkey.pem" onChange={(e) => setFiles({ ...files, key: e.target.value })} /></label>
            </>}
            {mode === "acme" && <>
              <div>
                <Label>{t("dmCA")}</Label>
                <Select value={ca} onChange={setCa} options={["letsencrypt", "staging", "custom"].map((x) => ({ value: x, label: t(("dmCA_" + x) as Key) }))} />
              </div>
              {ca === "custom" && <label><Label>{t("dmCAUrl")}</Label><Input dir="ltr" className="mono" value={caUrl} placeholder="https://…/directory" onChange={(e) => setCaUrl(e.target.value)} /></label>}
              <label><Label>{t("dmEmail")}</Label><Input dir="ltr" type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></label>
            </>}
            <div className="flex justify-end"><Button variant="primary" onClick={saveMore}>{t("save")}</Button></div>
          </div>
        )}
      </div>
    </Panel>
  );
}

// ---------------------------------------------------------------- Cloudflare

function CloudflarePanel({ st, setSt }: { st: State; setSt: (s: State) => void }) {
  const { t } = useFmt();
  const toast = useToast();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [zones, setZones] = useState<string[] | null>(null);
  const keep = () => {
    setBusy(true);
    api<{ zones: string[]; state: State }>("domains/cloudflare", { method: "PUT", body: { token } })
      .then((x) => { setSt(x.state); setZones(x.zones); setToken(""); toast(t("dmCFKept")); })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  const forget = () => api<State>("domains/cloudflare", { method: "PUT", body: { token: "" } }).then((x) => { setSt(x); setZones(null); toast(t("dmCFForgot")); }).catch((e) => toast(e.message, "error"));
  return (
    <Panel icon={<Cloud className="size-4" />} title={t("dmCF")}
      action={<Badge color={st.cloudflare.saved ? "var(--ok)" : "var(--text3)"}>{st.cloudflare.saved ? t("dmCFSaved") : t("dmCFNone")}</Badge>}>
      <div className="flex flex-col gap-3">
        <p className="text-[12.5px] text-ink2 leading-6">{t("dmCFHint")}</p>
        {st.cloudflare.saved && st.cloudflare.hint && <p className="mono text-[12px] text-ink3" dir="ltr">{st.cloudflare.hint}</p>}
        <label>
          <Label>{t("cfToken")}</Label>
          <Input dir="ltr" className="mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          <span className="block text-[12px] text-ink3 leading-5 mt-1.5">{t("dmCFPerms")}</span>
        </label>
        <div className="flex flex-wrap gap-2">
          <Button variant="soft" loading={busy} disabled={!token.trim()} onClick={keep}>{t("dmCFKeep")}</Button>
          {st.cloudflare.saved && <ConfirmButton title={t("dmCFForget")} body={t("dmCFHint")} onConfirm={forget}>{t("dmCFForget")}</ConfirmButton>}
        </div>
        {zones && (
          <div>
            <div className="text-[11.5px] text-ink3 mb-1.5">{t("dmCFZones")}</div>
            <div className="flex flex-wrap gap-1.5">{zones.map((z) => <Chip key={z} size="sm"><bdi dir="ltr" className="mono">{z}</bdi></Chip>)}</div>
          </div>
        )}
      </div>
    </Panel>
  );
}

// ---------------------------------------------------------------- links

function LinksPanel({ st, save }: { st: State; save: (l: Domain[], extra?: Record<string, unknown>, quiet?: boolean) => Promise<boolean> }) {
  const { t } = useFmt();
  const [host, setHost] = useState(st.server.host);
  useEffect(() => setHost(st.server.host), [st.server.host]);
  const hasCDN = st.domains.some((d) => d.role === "cdn");
  return (
    <Panel icon={<Activity className="size-4" />} title={t("dmLinks")}>
      <div className="flex flex-col gap-4">
        <div>
          <div className="text-[11.5px] text-ink3 mb-1">{t("dmServerIPs")}</div>
          <div className="flex flex-wrap gap-1.5">
            {[...st.server.ipv4, ...st.server.ipv6].map((ip) => <Chip key={ip} size="sm"><bdi dir="ltr" className="mono">{ip}</bdi></Chip>)}
            {st.server.ipv4.length + st.server.ipv6.length === 0 && <span className="text-[12.5px] text-ink3">—</span>}
          </div>
        </div>
        <div>
          <Label hint={t("dmLinkHostHint")}>{t("dmLinkHost")}</Label>
          <div className="flex gap-2">
            <Input dir="ltr" className="mono flex-1 min-w-0" value={host} placeholder={st.server.linkHost} onChange={(e) => setHost(e.target.value)} />
            <Button disabled={host === st.server.host} onClick={() => save(st.domains, { host })}>{t("save")}</Button>
          </div>
        </div>
        {hasCDN && <Toggle checked={st.cdnDirect} onChange={(v) => save(st.domains, { cdnDirect: v })} label={t("cdnDirect")} hint={t("cdnDirectHint")} />}
        <div>
          <div className="text-[11.5px] text-ink3 mb-1">{t("dmSubBase")}</div>
          <CopyField value={st.server.subBase} sensitive={false} />
        </div>
      </div>
    </Panel>
  );
}

// ---------------------------------------------------------------- other servers

function NodesPanel({ nodes }: { nodes: NodeDomain[] }) {
  const { t, left, leftTone } = useFmt();
  return (
    <Panel icon={<Server className="size-4" />} title={t("dmOtherServers")} sub={t("dmOtherServersHint")}
      action={<Button size="sm" variant="quiet" onClick={() => navigate("nodes")}>{t("dmOpenServers")}</Button>}>
      <ul className="flex flex-col">
        {nodes.map((n) => (
          <li key={n.id} className={cx("grid gap-x-5 gap-y-1 sm:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)] items-center py-2.5 border-b border-hair last:border-b-0")}>
            <span className="min-w-0 flex flex-col">
              <DomainName name={n.domain} className="text-[13.5px] text-ink" />
              <span className="text-[12px] text-ink3 truncate">{n.name}</span>
            </span>
            <StateWord tone={dnsTone(n.check)}>{n.check ? t(pointsKey(n.check)) : t("dmDnsNot")}</StateWord>
            <CertWord tone={n.realCert ? (n.certExpiry ? leftTone(n.certExpiry, 90 * 86400) : "ok") : "idle"}>
              {n.realCert ? (n.certExpiry ? left(n.certExpiry) : t("dmNodeReal")) : t("dmNodeSelf")}
            </CertWord>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

