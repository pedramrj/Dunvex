import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Activity, CircleCheck, CircleX, Cloud, CloudCog, Globe, KeyRound, Plus, Radar, Server, Trash2, TriangleAlert } from "lucide-react";
import { api } from "../lib/api";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, Empty, ErrorState, IconButton, Input, Label, Panel, Skeleton, Toggle, useToast } from "../components/ui";
import { navigate, PageHead } from "../App";

interface Address { address: string; name: string }
interface Settings { enabled: boolean; domain: string; addresses: Address[]; direct: boolean }
interface CDNInbound { id: number; name: string; port: number; transport: string; tls: boolean; enabled: boolean; portOK: boolean; host?: string }
interface CDNState { settings: Settings; domain: string; panelDomain: string; serverIP: string; selfSigned: boolean; inbounds: CDNInbound[] }
interface Result { ok: boolean; code: string; status?: number; cfCode?: number; server?: string; colo?: string; ms: number; error?: string }
interface Check {
  domain: string;
  dns: { ip: string; cloudflare: boolean; server: boolean }[];
  dnsError?: string;
  inbounds: { id: number; origin: Result; cdn: Result }[];
  addresses: { address: string; name: string; inbound: number; result: Result }[];
}
interface Step { id: string; ok: boolean; changed: boolean; detail?: string; warn?: string; error?: string }
interface Record { type: string; name: string; content: string; proxied: boolean }
interface Setup { zone: string; steps: Step[]; conflicts?: Record[]; done: boolean }

const transportName: { [k: string]: string } = { ws: "WebSocket", httpupgrade: "HTTPUpgrade", grpc: "gRPC", xhttp: "XHTTP" };

/** The CDN helper: links through the CDN, clean addresses, a check and the Cloudflare setup (owner only). */
export function CDNPage() {
  const { t } = useI18n();
  const toast = useToast();
  const [st, setSt] = useState<CDNState | null>(null);
  const [err, setErr] = useState("");
  const [s, setS] = useState<Settings | null>(null);
  const [busy, setBusy] = useState("");
  const [check, setCheck] = useState<Check | null>(null);
  const [cf, setCf] = useState({ token: "", domain: "", ssl: true, websockets: true });
  const [setup, setSetup] = useState<Setup | null>(null);

  const take = useCallback((x: CDNState) => {
    setSt(x);
    setS({ ...x.settings, addresses: x.settings.addresses.length ? x.settings.addresses : [] });
    setCf((c) => ({ ...c, domain: c.domain || x.settings.domain || (x.panelDomain ? "cdn." + x.panelDomain : "") }));
  }, []);
  const load = useCallback(() => { api<CDNState>("cdn").then(take).catch((e) => setErr(e.message)); }, [take]);
  useEffect(load, [load]);
  if (!st || !s) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-96" />;

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setS({ ...s, [k]: v });
  const setAddr = (i: number, a: Partial<Address>) => set("addresses", s.addresses.map((x, j) => (j === i ? { ...x, ...a } : x)));
  const save = () => {
    setBusy("save");
    api<CDNState>("cdn", { method: "PUT", body: { ...s, addresses: s.addresses.filter((a) => a.address.trim()) } })
      .then((x) => { take(x); toast(t("saved")); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const runCheck = () => {
    setBusy("check");
    setCheck(null);
    api<Check>("cdn/check", { method: "POST" }).then(setCheck).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const runSetup = (replace: boolean) => {
    setBusy("cf");
    api<{ result: Setup; state: CDNState }>("cdn/cloudflare", { method: "POST", body: { ...cf, replace } })
      .then((x) => {
        setSetup(x.result);
        take(x.state);
        if (x.result.done) {
          setCf((c) => ({ ...c, token: "" }));
          toast(t("cfDone"));
        }
      })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const domain = s.domain || st.panelDomain;
  const live = st.inbounds.filter((i) => i.enabled);

  return (
    <>
      <PageHead title={t("cdn")} sub={t("cdnSub")} />
      <div className="grid gap-[var(--gap)] xl:grid-cols-2 items-start">
        <div className="flex flex-col gap-[var(--gap)]">
          <Panel icon={<Cloud className="size-4" />} title={t("cdnLinksTitle")}
            action={<Badge color={st.settings.enabled ? "var(--ok)" : "var(--text3)"}>{st.settings.enabled ? t("cdnOn") : t("cdnOff")}</Badge>}>
            <div className="flex flex-col gap-5">
              <Toggle checked={s.enabled} onChange={(v) => set("enabled", v)} label={t("cdnEnabled")} hint={t("cdnEnabledHint")} />
              <label>
                <Label>{t("cdnDomain")}</Label>
                <Input dir="ltr" className="mono" placeholder={st.panelDomain || "cdn.example.com"} value={s.domain} onChange={(e) => set("domain", e.target.value)} />
                <Hint>{t("cdnDomainHint")}</Hint>
              </label>
              <Toggle checked={s.direct} onChange={(v) => set("direct", v)} label={t("cdnDirect")} hint={t("cdnDirectHint")} />
              <div>
                <Label>{t("cdnAddresses")}</Label>
                <p className="text-[12.5px] text-ink3 leading-6 mb-2.5">{t("cdnAddressesHint")}</p>
                <div className="flex flex-col gap-2">
                  {s.addresses.map((a, i) => (
                    <div key={i} className="flex gap-2 items-center">
                      <Input dir="ltr" className="mono flex-[3] min-w-0" placeholder={t("cdnAddressPh")} value={a.address} onChange={(e) => setAddr(i, { address: e.target.value })} />
                      <Input className="flex-[2] min-w-0" placeholder={t("cdnAddressName")} value={a.name} onChange={(e) => setAddr(i, { name: e.target.value })} />
                      <IconButton label={t("delete")} tone="danger" onClick={() => set("addresses", s.addresses.filter((_, j) => j !== i))}><Trash2 className="size-4" /></IconButton>
                    </div>
                  ))}
                  <div><Button size="sm" variant="soft" icon={<Plus className="size-4" />} disabled={s.addresses.length >= 30}
                    onClick={() => set("addresses", [...s.addresses, { address: "", name: "" }])}>{t("cdnAddAddress")}</Button></div>
                </div>
              </div>
              <div className="flex justify-end"><Button variant="primary" loading={busy === "save"} onClick={save}>{t("save")}</Button></div>
            </div>
          </Panel>

          <Panel icon={<CloudCog className="size-4" />} title={t("cfTitle")}>
            <div className="flex flex-col gap-4">
              <p className="text-[13px] text-ink2 leading-7">{t("cfHint")}</p>
              <label>
                <Label>{t("cdnDomain")}</Label>
                <Input dir="ltr" className="mono" placeholder="cdn.example.com" value={cf.domain} onChange={(e) => setCf({ ...cf, domain: e.target.value })} />
              </label>
              <label>
                <Label>{t("cfToken")}</Label>
                <Input dir="ltr" className="mono" type="password" autoComplete="off" spellCheck={false} value={cf.token} onChange={(e) => setCf({ ...cf, token: e.target.value })} />
                <Hint>{t("cfTokenHow")}</Hint>
              </label>
              <Toggle checked={cf.ssl} onChange={(v) => setCf({ ...cf, ssl: v })} label={t("cfSSL")} hint={t("cfSSLHint")} />
              <Toggle checked={cf.websockets} onChange={(v) => setCf({ ...cf, websockets: v })} label={t("cfWS")} />
              <div><Button variant="primary" icon={<KeyRound className="size-4" />} loading={busy === "cf"} disabled={!cf.token.trim() || !cf.domain.trim()}
                onClick={() => runSetup(false)}>{t("cfRun")}</Button></div>
              {setup && <SetupResult setup={setup} domain={cf.domain} busy={busy === "cf"} onReplace={() => runSetup(true)} />}
              <p className="text-[12.5px] text-ink3">{t("cfGrpc")}</p>
            </div>
          </Panel>
        </div>

        <div className="flex flex-col gap-[var(--gap)]">
          <Panel icon={<Radar className="size-4" />} title={t("cdnProtocols")}>
            {st.inbounds.length === 0 ? (
              <Empty icon={<Radar className="size-7" />} title={t("cdnNoProtocols")} hint={t("cdnNoProtocolsHint")}
                action={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("protocols", "add-cdn")}>{t("addProtocol")}</Button>} />
            ) : (
              <div className="flex flex-col gap-2">
                {st.inbounds.map((i) => (
                  <div key={i.id} className="rounded-2xl border border-hair px-4 py-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-[13.5px] font-medium">{i.name}</span>
                      <Badge color="var(--color-cdn)">{transportName[i.transport] ?? i.transport}{i.tls ? " · TLS" : ""}</Badge>
                      <Badge><bdi dir="ltr" className="mono">:{i.port}</bdi></Badge>
                      {!i.enabled && <Badge>{t("disabled")}</Badge>}
                    </div>
                    {!i.portOK && <Note>{t(i.tls ? "cdnPortBadTLS" : "cdnPortBadHTTP")}</Note>}
                    {i.transport === "grpc" && i.port !== 443 && <Note soft>{t("cdnGrpcPort")}</Note>}
                    {i.host && i.host !== domain && <Note>{t("cdnHostSet")} <bdi dir="ltr" className="mono">{i.host}</bdi></Note>}
                  </div>
                ))}
              </div>
            )}
          </Panel>

          <Panel icon={<Activity className="size-4" />} title={t("cdnCheckTitle")}
            action={<Button size="sm" variant="soft" icon={<Activity className="size-4" />} loading={busy === "check"} disabled={!domain} onClick={runCheck}>{t("cdnCheck")}</Button>}>
            <p className="text-[12.5px] text-ink3 mb-4">{t("cdnCheckHint")}</p>
            {check ? <CheckView check={check} inbounds={live} /> : <p className="text-[13px] text-ink2">{domain ? t("cdnCheckIdle") : t("cdnNeedDomain")}</p>}
          </Panel>
        </div>
      </div>
    </>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <span className="block text-[12.5px] text-ink3 leading-6 mt-1.5">{children}</span>;
}

function Note({ children, soft }: { children: ReactNode; soft?: boolean }) {
  return (
    <p className={"flex gap-2 items-start text-[12.5px] mt-2 " + (soft ? "text-ink3" : "text-warn")}>
      <TriangleAlert className="size-4 shrink-0 mt-0.5" /><span>{children}</span>
    </p>
  );
}

/** explain says in words what a probe found. */
function useExplain() {
  const { t, n } = useI18n();
  return (r: Result) => {
    if (r.ok) return t("cdnWorks");
    if (r.code === "status" && r.status) {
      const k = ("cdnStatus_" + r.status) as Key;
      const s = t(k) !== k ? t(k) : t("cdnCode_status") + " " + n(r.status);
      return r.cfCode && r.cfCode !== r.status ? s + " · Cloudflare " + n(r.cfCode) : s;
    }
    const k = ("cdnCode_" + r.code) as Key;
    return t(k) !== k ? t(k) : r.error || r.code;
  };
}

function ResultRow({ label, r, icon, address }: { label: ReactNode; r: Result; icon: ReactNode; address?: boolean }) {
  const { t, n } = useI18n();
  const explain = useExplain();
  return (
    <div className="flex gap-3 items-start py-2">
      <span className="text-ink3 mt-0.5">{icon}</span>
      <div className="flex-1 min-w-0">
        <div className="text-[12px] text-ink3">{label}</div>
        <div className={"text-[13px] leading-6 " + (r.ok ? "text-ink" : "text-ink2")}>{address && (r.code === "tls" || r.code === "cert") ? t("cdnAddrNoName") : explain(r)}</div>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        {r.colo && <Badge><bdi dir="ltr" className="mono">{r.colo}</bdi></Badge>}
        {r.ok && <span className="text-[12px] text-ink3 mono">{n(r.ms)} ms</span>}
        {r.ok ? <CircleCheck className="size-5 text-online" /> : <CircleX className="size-5 text-bad" />}
      </div>
    </div>
  );
}

function CheckView({ check, inbounds }: { check: Check; inbounds: CDNInbound[] }) {
  const { t } = useI18n();
  const name = (id: number) => inbounds.find((i) => i.id === id)?.name ?? "#" + id;
  const proxied = check.dns.some((d) => d.cloudflare);
  const direct = check.dns.some((d) => d.server);
  return (
    <div className="flex flex-col gap-5">
      <div>
        <div className="text-[12px] text-ink3 mb-2 flex items-center gap-2"><Globe className="size-4" /><span>{t("cdnDNS")}</span> <bdi dir="ltr" className="mono">{check.domain}</bdi></div>
        {check.dnsError ? <Note>{t("cdnDnsNone")}</Note> : (
          <div className="flex flex-wrap gap-2">
            {check.dns.map((d) => (
              <Badge key={d.ip} color={d.cloudflare ? "var(--ok)" : d.server ? "var(--warn)" : "var(--text2)"}>
                <bdi dir="ltr" className="mono">{d.ip}</bdi> · {d.cloudflare ? "Cloudflare" : d.server ? t("cdnDnsServer") : t("cdnDnsOther")}
              </Badge>
            ))}
          </div>
        )}
        {!check.dnsError && !proxied && <Note>{direct ? t("cdnDnsNotProxied") : t("cdnDnsNotCloudflare")}</Note>}
      </div>
      {check.inbounds.map((c) => (
        <div key={c.id} className="rounded-2xl border border-hair px-4 py-1">
          <div className="text-[13.5px] font-medium pt-2.5">{name(c.id)}</div>
          <ResultRow label={t("cdnViaCDN")} r={c.cdn} icon={<Cloud className="size-4" />} />
          <ResultRow label={t("cdnViaOrigin")} r={c.origin} icon={<Server className="size-4" />} />
        </div>
      ))}
      {check.addresses.length > 0 && (
        <div className="rounded-2xl border border-hair px-4 py-1">
          <div className="text-[13.5px] font-medium pt-2.5">{t("cdnAddresses")} <span className="text-ink3 font-normal text-[12px]">· {name(check.addresses[0].inbound)}</span></div>
          {check.addresses.map((a) => (
            <ResultRow key={a.address} label={<><bdi dir="ltr" className="mono">{a.address}</bdi>{a.name && " · " + a.name}</>} r={a.result} icon={<Radar className="size-4" />} address />
          ))}
          <p className="text-[12px] text-ink3 pb-2.5">{t("cdnAddressesFrom")}</p>
        </div>
      )}
    </div>
  );
}

function SetupResult({ setup, domain, busy, onReplace }: { setup: Setup; domain: string; busy: boolean; onReplace: () => void }) {
  const { t } = useI18n();
  if (setup.conflicts?.length) {
    return (
      <div className="rounded-2xl border border-warn/40 bg-warn/[.08] p-4 flex flex-col gap-3">
        <p className="text-[13px] flex gap-2"><TriangleAlert className="size-5 text-warn shrink-0" /><span>{t("cfConflict")} <bdi dir="ltr" className="mono">{domain}</bdi></span></p>
        <div className="flex flex-col gap-1">
          {setup.conflicts.map((r, i) => (
            <div key={i} className="text-[12.5px] mono" dir="ltr">{r.type} {r.name} → {r.content}{r.proxied ? " (proxied)" : ""}</div>
          ))}
        </div>
        <div><Button size="sm" variant="primary" loading={busy} onClick={onReplace}>{t("cfReplace")}</Button></div>
      </div>
    );
  }
  return (
    <div className="rounded-2xl border border-hair bg-fg/[.025] px-4 py-2">
      {setup.steps.map((s) => (
        <div key={s.id} className="flex gap-3 items-start py-2">
          {s.ok ? <CircleCheck className="size-5 text-online shrink-0" /> : <CircleX className="size-5 text-bad shrink-0" />}
          <div className="flex-1 min-w-0">
            <div className="text-[13px]">{t(("cfStep_" + s.id) as Key)} {s.detail && <bdi dir="ltr" className="mono text-[12px] text-ink3">{s.detail}</bdi>}</div>
            {s.error && <div className="text-[12.5px] text-bad">{s.error}</div>}
            {s.warn && <div className="text-[12.5px] text-warn">{t(("cfWarn_" + s.warn) as Key)}</div>}
          </div>
          {s.ok && <span className="text-[12px] text-ink3 shrink-0">{s.changed ? t("cfChanged") : t("cfUnchanged")}</span>}
        </div>
      ))}
    </div>
  );
}
