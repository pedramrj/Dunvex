import { useEffect, useState } from "react";
import { Gift, ShoppingBag, Timer, UserCheck } from "lucide-react";
import { api, type Inbound, type TrialInfo, type TrialSettings } from "../lib/api";
import { ago, cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Button, Checkbox, Input, Label, Panel, Select, Skeleton, StatusBadge, Toggle, useToast } from "./ui";
import { navigate } from "../App";
import { shortName } from "./NetworkMap";

/** Settings › Telegram: the free trial the bot gives to people new to it. */
export function TrialPanel() {
  const { t, n, pc, lang } = useI18n();
  const toast = useToast();
  const [info, setInfo] = useState<TrialInfo | null>(null);
  const [f, setF] = useState<TrialSettings | null>(null);
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [dataUnit, setDataUnit] = useState<"MB" | "GB">("GB");
  const [timeUnit, setTimeUnit] = useState<"h" | "d">("h");
  const [busy, setBusy] = useState(false);
  const load = (r: TrialInfo) => {
    setInfo(r);
    setF(r.settings);
    setDataUnit(r.settings.dataMB > 0 && r.settings.dataMB % 1024 === 0 ? "GB" : "MB");
    setTimeUnit(r.settings.hours % 24 === 0 ? "d" : "h");
  };
  useEffect(() => {
    api<TrialInfo>("trial").then(load).catch((e) => toast(e.message, "error"));
    api<Inbound[]>("inbounds").then((l) => setInbounds(l.filter((i) => i.enabled))).catch(() => {});
  }, []);
  if (!info || !f) return <Skeleton className="h-64" />;

  const dataVal = dataUnit === "GB" ? f.dataMB / 1024 : f.dataMB;
  const timeVal = timeUnit === "d" ? f.hours / 24 : f.hours;
  const setData = (v: number, unit = dataUnit) => setF({ ...f, dataMB: Math.max(0, Math.round(unit === "GB" ? v * 1024 : v)) });
  const setTime = (v: number, unit = timeUnit) => setF({ ...f, hours: Math.max(1, Math.round(unit === "d" ? v * 24 : v)) });
  const all = f.inbounds.length === 0;
  const toggleInbound = (id: number, on: boolean) => setF({ ...f, inbounds: on ? [...f.inbounds, id] : f.inbounds.filter((x) => x !== id) });
  const save = () => {
    setBusy(true);
    api<TrialInfo>("trial", { method: "PUT", body: f }).then((r) => { load(r); toast(t("saved")); }).catch((e) => toast(e.message, "error")).finally(() => setBusy(false));
  };
  const s = info.stats;
  const conv = s.total > 0 ? (s.bought / s.total) * 100 : 0;

  return (
    <Panel icon={<Gift className="size-4" />} title={t("trTitle")} sub={t("trSub")}
      action={<Badge color={info.settings.on ? "var(--ok)" : "var(--color-ink3)"}>{info.settings.on ? t("on") : t("off")}</Badge>}>
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5 mb-5">
        <Stat icon={<Gift className="size-4" />} label={t("trGiven")} value={n(s.total)} />
        <Stat icon={<Timer className="size-4" />} label={t("trDay")} value={n(s.day)} />
        <Stat icon={<UserCheck className="size-4" />} label={t("trActive")} value={n(s.active)} />
        <Stat icon={<ShoppingBag className="size-4" />} label={t("trBought")} value={n(s.bought)} sub={s.total > 0 ? pc(conv, 0) : undefined} />
      </div>

      <p className="text-[13px] text-ink2 leading-relaxed max-w-[72ch]">{t("trHelp")}</p>
      {!info.bot?.configured && <p className="mt-2 text-[12.5px] text-warn">{t("trNoBot")}</p>}
      <div className="mt-4"><Toggle checked={f.on} onChange={(v) => setF({ ...f, on: v })} label={t("trOn")} /></div>

      <div className="mt-5 grid sm:grid-cols-3 gap-4">
        <label>
          <Label hint={f.dataMB === 0 ? t("unlimited") : undefined}>{t("trData")}</Label>
          <div className="flex gap-2">
            <Input type="number" min={0} step="any" className="num" value={dataVal} onChange={(e) => setData(+e.target.value)} />
            <Select className="!w-24 shrink-0" value={dataUnit} onChange={(v) => { setDataUnit(v as "MB" | "GB"); }} options={[{ value: "MB", label: "MB" }, { value: "GB", label: "GB" }]} />
          </div>
        </label>
        <label>
          <Label>{t("trTime")}</Label>
          <div className="flex gap-2">
            <Input type="number" min={1} step="any" className="num" value={timeVal} onChange={(e) => setTime(+e.target.value)} />
            <Select className="!w-28 shrink-0" value={timeUnit} onChange={(v) => setTimeUnit(v as "h" | "d")} options={[{ value: "h", label: t("trHours") }, { value: "d", label: t("days") }]} />
          </div>
        </label>
        <label><Label hint={f.devices === 0 ? t("unlimited") : undefined}>{t("trDevices")}</Label><Input type="number" min={0} className="num" value={f.devices} onChange={(e) => setF({ ...f, devices: Math.max(0, +e.target.value) })} /></label>
        <label><Label hint={f.daily === 0 ? t("trNoCap") : undefined}>{t("trDaily")}</Label><Input type="number" min={0} className="num" value={f.daily} onChange={(e) => setF({ ...f, daily: Math.max(0, +e.target.value) })} /></label>
        <label><Label hint={f.cleanupDays === 0 ? t("trKeepAll") : undefined}>{t("trCleanup")}</Label><Input type="number" min={0} className="num" value={f.cleanupDays} onChange={(e) => setF({ ...f, cleanupDays: Math.max(0, +e.target.value) })} /></label>
        <label><Label>{t("trChannel")}</Label><Input dir="ltr" placeholder="@mychannel" value={f.channel} onChange={(e) => setF({ ...f, channel: e.target.value })} /></label>
      </div>
      {f.channel && <p className="mt-2 text-[12px] text-ink3 leading-relaxed">{t("trChannelHint")}</p>}

      <div className="mt-5">
        <Toggle checked={all} onChange={(v) => setF({ ...f, inbounds: v ? [] : inbounds.map((i) => i.id) })} label={t("trAllProtocols")} hint={t("trAllProtocolsHint")} />
        {!all && (
          <div className="mt-3 grid sm:grid-cols-2 xl:grid-cols-3 gap-2">
            {inbounds.map((i) => (
              <div key={i.id} className={cx("flex items-center gap-2.5 rounded-xl border px-3 py-2", f.inbounds.includes(i.id) ? "border-primary/40 bg-primary/8" : "border-hair")}>
                <Checkbox checked={f.inbounds.includes(i.id)} onChange={(v) => toggleInbound(i.id, v)} label={i.remark || shortName[i.preset] || i.name} />
                <button type="button" className="min-w-0 flex-1 text-start" onClick={() => toggleInbound(i.id, !f.inbounds.includes(i.id))}>
                  <bdi dir="ltr" className="block truncate text-[13px] font-medium">{i.remark || shortName[i.preset] || i.name}</bdi>
                  <span dir="ltr" className="block truncate text-[11.5px] text-ink3">{i.name}{i.port ? " · :" + i.port : ""}</span>
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-6 flex justify-end"><Button variant="primary" loading={busy} onClick={save}>{t("save")}</Button></div>

      {info.recent.length > 0 && (
        <div className="mt-6 pt-5 border-t border-hair">
          <div className="text-[13.5px] font-semibold mb-2">{t("trRecent")}</div>
          <ul className="flex flex-col divide-y divide-hair">
            {info.recent.map((r) => (
              <li key={r.chat} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-[12.5px]">
                {r.status !== "deleted" && r.userId ? (
                  <button className="mono text-[12.5px] text-primary2 hover:underline" dir="ltr" onClick={() => navigate("users", String(r.userId))}>{r.name}</button>
                ) : <span className="mono text-ink3" dir="ltr">{r.name || "—"}</span>}
                <span className="text-ink3">{ago(r.at, lang)}</span>
                <span className="ms-auto flex items-center gap-1.5">
                  {r.bought && <Badge className="h-5 text-[10.5px] px-2" color="var(--ok)" icon={<ShoppingBag className="size-3" />}>{t("trBoughtBadge")}</Badge>}
                  {r.status === "deleted" ? <Badge className="h-5 text-[10.5px] px-2" color="var(--color-ink3)">{t("trDeleted")}</Badge> : <StatusBadge status={r.status as any} />}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}

function Stat({ icon, label, value, sub }: { icon: React.ReactNode; label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-2xl border border-hair bg-fg/[.02] px-3 py-2.5">
      <div className="flex items-center gap-1.5 text-[12px] text-ink3">{icon}{label}</div>
      <div className="mt-1 num text-[20px] font-bold leading-none">{value}{sub && <span className="text-[12px] font-normal text-ink3 ms-1.5">{sub}</span>}</div>
    </div>
  );
}
