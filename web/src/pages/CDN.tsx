import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Activity, CircleCheck, CircleX, Cloud, Globe, KeyRound, Plus, Radar, Server, TriangleAlert } from "lucide-react";
import { api } from "../lib/api";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, Empty, ErrorState, Input, Label, SectionTitle, Skeleton, Toggle, useToast } from "../components/ui";
import { navigate } from "../App";

export interface Address { address: string; name: string }
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

/** The CDN assistant of one CDN name (inside its Domains sheet): the CDN
 * protocols and their ports, a check of every leg, and the Cloudflare setup.
 * The name's own settings (links through it, clean addresses) are the sheet's. */
export function CDNAssistant({ name, tokenKept, onChanged }: { name: string; tokenKept: boolean; onChanged: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [st, setSt] = useState<CDNState | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [check, setCheck] = useState<Check | null>(null);
  const [cf, setCf] = useState({ token: "", ssl: true, websockets: true });
  const [setup, setSetup] = useState<Setup | null>(null);

  const load = useCallback(() => { api<CDNState>("cdn?name=" + encodeURIComponent(name)).then(setSt).catch((e) => setErr(e.message)); }, [name]);
  useEffect(load, [load]);
  if (!st) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-40" />;

  const runCheck = () => {
    setBusy("check");
    setCheck(null);
    api<Check>("cdn/check", { method: "POST", body: { name } }).then(setCheck).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const runSetup = (replace: boolean) => {
    setBusy("cf");
    api<{ result: Setup; state: CDNState }>("cdn/cloudflare", { method: "POST", body: { ...cf, domain: name, replace } })
      .then((x) => {
        setSetup(x.result);
        setSt(x.state);
        if (x.result.done) {
          setCf((c) => ({ ...c, token: "" }));
          toast(t("cfDone"));
          onChanged();
        }
      })
      .catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const live = st.inbounds.filter((i) => i.enabled);

  return (
    <div className="flex flex-col gap-6">
      <section>
        <SectionTitle>{t("cdnProtocols")}</SectionTitle>
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
                {i.host && i.host !== name && <Note>{t("cdnHostSet")} <bdi dir="ltr" className="mono">{i.host}</bdi></Note>}
              </div>
            ))}
          </div>
        )}
      </section>

      <section>
        <SectionTitle action={<Button size="sm" variant="soft" icon={<Activity className="size-4" />} loading={busy === "check"} onClick={runCheck}>{t("cdnCheck")}</Button>}>{t("cdnCheckTitle")}</SectionTitle>
        <p className="text-[12.5px] text-ink3 mb-4">{t("cdnCheckHint")}</p>
        {check ? <CheckView check={check} inbounds={live} /> : <p className="text-[13px] text-ink2">{t("cdnCheckIdle")}</p>}
      </section>

      <section className="flex flex-col gap-4">
        <SectionTitle>{t("cfTitle")}</SectionTitle>
        <p className="text-[13px] text-ink2 leading-7 -mt-2">{tokenKept ? t("cfHintKept") : t("cfHint")}</p>
        <label>
          <Label hint={tokenKept ? t("dmCFSaved") : undefined}>{t("cfToken")}</Label>
          <Input dir="ltr" className="mono" type="password" autoComplete="off" spellCheck={false} value={cf.token} onChange={(e) => setCf({ ...cf, token: e.target.value })} />
          {!tokenKept && <Hint>{t("cfTokenHow")}</Hint>}
        </label>
        <Toggle checked={cf.ssl} onChange={(v) => setCf({ ...cf, ssl: v })} label={t("cfSSL")} hint={t("cfSSLHint")} />
        <Toggle checked={cf.websockets} onChange={(v) => setCf({ ...cf, websockets: v })} label={t("cfWS")} />
        <div><Button variant="primary" icon={<KeyRound className="size-4" />} loading={busy === "cf"} disabled={!cf.token.trim() && !tokenKept}
          onClick={() => runSetup(false)}>{t("cfRun")}</Button></div>
        {setup && <SetupResult setup={setup} domain={name} busy={busy === "cf"} onReplace={() => runSetup(true)} />}
        <p className="text-[12.5px] text-ink3">{t("cfGrpc")}</p>
      </section>
    </div>
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
