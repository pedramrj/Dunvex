import { useEffect, useState } from "react";
import { KeyRound, Lock, LogIn, ShieldAlert, ShieldCheck, ShieldX, UserCog, Users as UsersIcon } from "lucide-react";
import { api, type Admin, type Event } from "../lib/api";
import { ago, cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Avatar, Badge, Button, EventMsg, Panel, StatCard, Num } from "../components/ui";
import { PageHead, navigate, roleColor, roleKey, useSession } from "../App";
import { ApiKeysPanel, SessionsPanel } from "./Access";

/** Security centre: sign-ins, failures, two-step login and roles, from real events. */
export function SecurityPage() {
  const { t, lang, n, sep } = useI18n();
  const { me } = useSession();
  const [events, setEvents] = useState<Event[]>([]);
  const [admins, setAdmins] = useState<Admin[] | null>(null);
  const owner = me.admin.role === "owner";
  useEffect(() => {
    api<Event[]>("events?limit=500").then((l) => setEvents(l.filter((e) => e.kind === "auth"))).catch(() => {});
    if (owner) api<Admin[]>("admins").then(setAdmins).catch(() => {});
  }, [owner]);
  const day = Date.now() / 1000 - 86400;
  const failed = events.filter((e) => /fail|wrong|denied/i.test(e.message));
  const failed24 = failed.filter((e) => e.at > day).length;
  const signins = events.filter((e) => /signed in/i.test(e.message));
  const without2fa = admins?.filter((a) => a.enabled && !a.totp).length ?? null;

  return (
    <div>
      <PageHead title={t("security")} sub={t("securitySub")} />
      <div className="grid gap-[var(--gap)] grid-cols-2 lg:grid-cols-4 mb-[var(--gap)]">
        <StatCard dense icon={me.admin.totp ? <ShieldCheck className="size-[18px]" /> : <ShieldX className="size-[18px]" />} accent={me.admin.totp ? "var(--ok)" : "var(--warn)"}
          label={t("twoFactor")} value={<span className="text-[20px]">{me.admin.totp ? t("on") : t("off")}</span>} onClick={() => navigate("settings", "security")} sub={t("yourAccount")} />
        <StatCard dense icon={<LogIn className="size-[18px]" />} label={t("signIns")} value={<Num value={signins.length} />} sub={signins[0] ? ago(signins[0].at, lang) : "—"} />
        <StatCard dense icon={<ShieldAlert className="size-[18px]" />} accent={failed24 ? "var(--bad)" : "var(--text2)"} label={t("failedLogins24")} value={<Num value={failed24} />} sub={t("throttleNote")} />
        <StatCard dense icon={<UsersIcon className="size-[18px]" />} accent={without2fa ? "var(--warn)" : "var(--text2)"} label={t("adminsWithout2fa")} value={without2fa === null ? "—" : <Num value={without2fa} />} />
      </div>
      <div className="grid gap-[var(--gap)] xl:grid-cols-12">
        <Panel className="xl:col-span-7" icon={<KeyRound className="size-4" />} title={t("loginHistory")}>
          <ul className="flex flex-col">
            {events.slice(0, 40).map((e) => {
              const bad = /fail|wrong|denied/i.test(e.message);
              return (
                <li key={e.id} className="flex items-start gap-3 py-2.5 border-b border-hair last:border-0">
                  <span className={cx("grid place-items-center size-7 rounded-lg shrink-0", bad ? "bg-bad/12 text-bad" : "bg-online/12 text-online")}>{bad ? <ShieldX className="size-3.5" /> : <LogIn className="size-3.5" />}</span>
                  <EventMsg message={e.message} className="flex-1 min-w-0 text-[13px]" />
                  <span className="text-[11.5px] text-ink3 whitespace-nowrap">{ago(e.at, lang)}</span>
                </li>
              );
            })}
            {events.length === 0 && <li className="text-ink3 text-sm py-8 text-center">{t("noEvents")}</li>}
          </ul>
        </Panel>
        <div className="xl:col-span-5 flex flex-col gap-[var(--gap)]">
          <Panel icon={<Lock className="size-4" />} title={t("protections")}>
            <ul className="flex flex-col gap-3 text-[13px]">
              {[t("protSession"), t("protThrottle"), t("protCsrf"), t("protDecoy"), t("protRoles")].map((x) => (
                <li key={x} className="flex items-start gap-2.5"><ShieldCheck className="size-4 text-primary2 shrink-0 mt-0.5" /><span className="text-ink2">{x}</span></li>
              ))}
            </ul>
          </Panel>
          {owner && (
            <Panel icon={<UserCog className="size-4" />} title={t("admins")} action={<Button size="sm" variant="soft" onClick={() => navigate("settings", "admins")}>{t("manage")}</Button>}>
              <ul className="flex flex-col gap-2">
                {(admins ?? []).map((a) => (
                  <li key={a.id} className="flex items-center gap-3">
                    <Avatar name={a.username} size={30} />
                    <span className="flex-1 truncate" dir="ltr">{a.username}</span>
                    {a.totp ? <ShieldCheck className="size-4 text-online" aria-label={t("twoFactor")} /> : <ShieldX className="size-4 text-warn" aria-label={t("off")} />}
                    <Badge color={roleColor[a.role]}>{t(roleKey[a.role])}</Badge>
                  </li>
                ))}
              </ul>
              <p className="text-[12px] text-ink3 mt-3">{n(admins?.length ?? 0)}{sep}{t("sessionNote")}</p>
            </Panel>
          )}
        </div>
        <SessionsPanel className="xl:col-span-7" me={me.admin.username} />
        <ApiKeysPanel className="xl:col-span-5" me={me.admin.username} />
      </div>
    </div>
  );
}
