import { useCallback, useEffect, useState } from "react";
import { Activity, Ban, Cloud, Globe, KeyRound, Trash2, TriangleAlert, Waypoints } from "lucide-react";
import { api } from "../lib/api";
import { date } from "../lib/format";
import { useI18n, type Key } from "../lib/i18n";
import { Badge, Button, ConfirmButton, Dialog, ErrorState, Input, KV, Label, Panel, Segmented, Skeleton, Textarea, Toggle, useToast } from "../components/ui";
import { PageHead } from "../App";

interface RoutingSettings {
  blockAds: boolean;
  blockAdult: boolean;
  iran: "direct" | "block" | "warp";
  clientIran: boolean;
  warp: "off" | "sites" | "all";
  warpGroups: string[] | null;
  warpDomains: string[] | null;
  blockDomains: string[] | null;
  directDomains: string[] | null;
}
interface WarpInfo { transport: "masque" | "wireguard"; type: string; addressV4: string; addressV6: string; endpoint: string; created: number; license: boolean }
interface RoutingState { settings: RoutingSettings; blockTorrent: boolean; warp: WarpInfo | null; groups: string[]; geo: boolean; nodes: number }
interface Trace { ip: string; loc: string; colo: string; warp: string }

const lines = (v: string[] | null) => (v ?? []).join("\n");
const list = (v: string) => v.split(/[\n,،]+/).map((x) => x.trim()).filter(Boolean);

/** The server's routing: blocking, Iranian sites and Cloudflare WARP (owner only). */
export function RoutingPage() {
  const { t, lang } = useI18n();
  const toast = useToast();
  const [st, setSt] = useState<RoutingState | null>(null);
  const [err, setErr] = useState("");
  const [s, setS] = useState<RoutingSettings | null>(null);
  const [torrent, setTorrent] = useState(true);
  const [text, setText] = useState({ warp: "", block: "", direct: "" });
  const [busy, setBusy] = useState("");
  const [trace, setTrace] = useState<{ warp: Trace; direct: Trace | null } | null>(null);
  const [license, setLicense] = useState<string | null>(null);

  const take = useCallback((x: RoutingState) => {
    setSt(x);
    setS(x.settings);
    setTorrent(x.blockTorrent);
    setText({ warp: lines(x.settings.warpDomains), block: lines(x.settings.blockDomains), direct: lines(x.settings.directDomains) });
  }, []);
  const load = useCallback(() => { api<RoutingState>("routing").then(take).catch((e) => setErr(e.message)); }, [take]);
  useEffect(load, [load]);
  if (!st || !s) return err ? <ErrorState message={err} onRetry={load} /> : <Skeleton className="h-96" />;

  const set = <K extends keyof RoutingSettings>(k: K, v: RoutingSettings[K]) => setS({ ...s, [k]: v });
  const run = (what: string, p: Promise<RoutingState>, done?: string) => {
    setBusy(what);
    return p.then((x) => { take(x); if (done) toast(done); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const save = () =>
    run("save", api<RoutingState>("routing", { method: "PUT", body: {
      blockTorrent: torrent,
      settings: { ...s, warpDomains: list(text.warp), blockDomains: list(text.block), directDomains: list(text.direct) },
    } }), t("saved"));
  const check = () => {
    setBusy("check");
    setTrace(null);
    api<{ warp: Trace; direct: Trace | null }>("routing/warp/check", { method: "POST" })
      .then(setTrace).catch((e) => toast(e.message, "error")).finally(() => setBusy(""));
  };
  const w = st.warp;
  const groups = s.warpGroups ?? [];

  return (
    <>
      <PageHead title={t("routing")} sub={t("routingSub")} />
      {!st.geo && (
        <div className="flex gap-3 items-start rounded-2xl border border-warn/40 bg-warn/[.08] px-4 py-3 text-[13px] mb-[var(--gap)]">
          <TriangleAlert className="size-5 text-warn shrink-0" /><span>{t("routingNoGeo")}</span>
        </div>
      )}
      <div className="grid gap-[var(--gap)] xl:grid-cols-2 items-start">
        <Panel icon={<Cloud className="size-4" />} title={t("warpTitle")}
          action={w && <Badge color="var(--ok)">{w.type === "free" ? t("warpFree") : t("warpPlus")}</Badge>}>
          {!w ? (
            <div className="flex flex-col gap-4">
              <p className="text-[13.5px] text-ink2 leading-7">{t("warpOffHint")}</p>
              <p className="text-[12.5px] text-ink3">{t("warpOnHint")}</p>
              <div><Button variant="primary" icon={<Cloud className="size-4" />} loading={busy === "warp"}
                onClick={() => run("warp", api<RoutingState>("routing/warp", { method: "POST" }), t("warpReady"))}>{t("warpTurnOn")}</Button></div>
            </div>
          ) : (
            <div className="flex flex-col gap-5">
              <div className="grid grid-cols-2 gap-x-5 gap-y-3">
                <KV label={t("warpAddress")}><bdi dir="ltr" className="mono text-[12.5px]">{w.addressV4}</bdi></KV>
                <KV label={t("warpTransport")}>{w.transport === "masque" ? t("warpMasque") : "WireGuard"}</KV>
                <KV label={t("created")}>{date(w.created, lang)}</KV>
                <KV label={t("warpLicense")}>{w.license ? "✓" : "—"}</KV>
              </div>
              {w.transport !== "masque" && (
                <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-warn/40 bg-warn/[.08] px-4 py-3 text-[13px]">
                  <TriangleAlert className="size-5 text-warn shrink-0" /><span className="flex-1 min-w-0">{t("warpWireGuardHint")}</span>
                  <Button size="sm" variant="primary" loading={busy === "warp"}
                    onClick={() => run("warp", api<RoutingState>("routing/warp", { method: "POST" }), t("warpReady"))}>{t("warpMoveMasque")}</Button>
                </div>
              )}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="soft" icon={<Activity className="size-4" />} loading={busy === "check"} onClick={check}>{t("warpCheck")}</Button>
                <Button size="sm" icon={<KeyRound className="size-4" />} onClick={() => setLicense("")}>{t("warpLicense")}</Button>
                <span className="ms-auto" />
                <ConfirmButton icon={<Trash2 className="size-4" />} title={t("warpRemove")} body={t("warpRemoveHint")}
                  onConfirm={() => run("remove", api<RoutingState>("routing/warp", { method: "DELETE" }))}>{t("warpRemove")}</ConfirmButton>
              </div>
              {trace && (
                <div className="rounded-2xl border border-hair bg-fg/[.025] p-4">
                  <div className="text-[12px] text-ink3 mb-3">{t("warpChecked")}</div>
                  <div className="grid grid-cols-2 gap-x-5 gap-y-3">
                    <KV label={t("warpSeenAs")}><bdi dir="ltr" className="mono text-[12.5px]">{trace.warp.ip}</bdi> <Badge color="var(--ok)">warp={trace.warp.warp}</Badge></KV>
                    <KV label={t("directSeenAs")}><bdi dir="ltr" className="mono text-[12.5px]">{trace.direct?.ip ?? "—"}</bdi></KV>
                    <KV label="Cloudflare"><bdi dir="ltr" className="mono text-[12.5px]">{trace.warp.colo} · {trace.warp.loc}</bdi></KV>
                  </div>
                </div>
              )}
              <div>
                <Label>{t("warpSends")}</Label>
                <Segmented value={s.warp} onChange={(v) => set("warp", v)}
                  options={[{ value: "off", label: t("warpSendOff") }, { value: "sites", label: t("warpSendSites") }, { value: "all", label: t("warpSendAll") }]} />
              </div>
              {s.warp === "sites" && (
                <>
                  <div className="grid sm:grid-cols-2 gap-1 rounded-2xl border border-hair p-2">
                    {st.groups.map((g) => (
                      <label key={g} className="flex items-center gap-3 min-h-10 px-2.5 py-1.5 rounded-xl hover:bg-fg/[.04] cursor-pointer">
                        <input type="checkbox" className="accent-[var(--primary)] size-4 shrink-0" checked={groups.includes(g)}
                          onChange={(e) => set("warpGroups", e.target.checked ? [...groups, g] : groups.filter((x) => x !== g))} />
                        <span className="text-[13.5px]">{t(("group_" + g) as Key)}</span>
                      </label>
                    ))}
                  </div>
                  <label><Label>{t("warpDomainsField")}</Label>
                    <Textarea dir="ltr" rows={3} className="mono text-[12.5px]" placeholder={"grok.com\napi.example.com"} value={text.warp} onChange={(e) => setText({ ...text, warp: e.target.value })} /></label>
                </>
              )}
              {s.warp === "all" && (
                <>
                  <p className="text-[12.5px] text-ink3">{t("warpAllHint")}</p>
                  <label><Label>{t("directDomainsField")}</Label>
                    <Textarea dir="ltr" rows={3} className="mono text-[12.5px]" placeholder="mybank.com" value={text.direct} onChange={(e) => setText({ ...text, direct: e.target.value })} /></label>
                </>
              )}
              {st.nodes > 0 && <p className="text-[12px] text-ink3">{t("routingNodes")}</p>}
            </div>
          )}
        </Panel>

        <div className="flex flex-col gap-[var(--gap)]">
          <Panel icon={<Globe className="size-4" />} title={t("iranSites")}>
            <Label>{t("iranOnServer")}</Label>
            <Segmented value={s.iran} onChange={(v) => (v === "warp" && !w ? toast(t("warpNeedsOn"), "error") : set("iran", v))}
              options={[{ value: "direct", label: t("iranDirect") }, { value: "block", label: t("iranBlock") }, { value: "warp", label: t("iranWarp") }]} />
            <p className="text-[12.5px] text-ink3 mt-2">{t(({ direct: "iranDirectHint", block: "iranBlockHint", warp: "iranWarpHint" } as const)[s.iran])}</p>
            <div className="mt-5"><Toggle checked={s.clientIran} onChange={(v) => set("clientIran", v)} label={t("clientIran")} hint={t("clientIranHint")} /></div>
          </Panel>
          <Panel icon={<Ban className="size-4" />} title={t("blocking")}>
            <div className="flex flex-col gap-4">
              <Toggle checked={torrent} onChange={setTorrent} label={t("blockTorrent")} />
              <Toggle checked={s.blockAds} onChange={(v) => set("blockAds", v)} label={t("blockAds")} />
              <Toggle checked={s.blockAdult} onChange={(v) => set("blockAdult", v)} label={t("blockAdult")} />
              <label><Label>{t("blockDomainsField")}</Label>
                <Textarea dir="ltr" rows={3} className="mono text-[12.5px]" placeholder="example.com" value={text.block} onChange={(e) => setText({ ...text, block: e.target.value })} /></label>
            </div>
          </Panel>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-3 mt-[var(--gap)]">
        <p className="text-[12.5px] text-ink3 flex items-center gap-2"><Waypoints className="size-4 shrink-0" />{t("routingCoverage")}</p>
        <span className="ms-auto" />
        <Button variant="primary" loading={busy === "save"} onClick={save}>{t("save")}</Button>
      </div>

      <Dialog open={license !== null} onClose={() => setLicense(null)} title={t("warpLicense")}
        footer={<><Button variant="quiet" onClick={() => setLicense(null)}>{t("cancel")}</Button>
          <Button variant="primary" loading={busy === "license"} disabled={!license?.trim()}
            onClick={() => run("license", api<RoutingState>("routing/warp/license", { method: "POST", body: { license } }), t("saved")).then(() => setLicense(null))}>{t("save")}</Button></>}>
        <p className="text-[13px] text-ink2 mb-3">{t("warpLicenseHint")}</p>
        <Input dir="ltr" className="mono" value={license ?? ""} placeholder="xxxxxxxx-xxxxxxxx-xxxxxxxx" onChange={(e) => setLicense(e.target.value)} />
      </Dialog>
    </>
  );
}
