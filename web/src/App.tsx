import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { Gauge, Languages, LogOut, Plus, Route as RouteIcon, Settings as SettingsIcon, ShieldCheck, Users as UsersIcon } from "lucide-react";
import { api, setUnauthorizedHandler, VERSION, type Me, type Role } from "./lib/api";
import { cx } from "./lib/format";
import { useI18n, type Key } from "./lib/i18n";
import { Avatar, Badge, Button, Spinner } from "./components/ui";
import { Logo } from "./components/Logo";
import { Login } from "./pages/Login";
import { OverviewPage } from "./pages/Overview";
import { UsersPage } from "./pages/Users";
import { ProtocolsPage } from "./pages/Protocols";
import { SettingsPage } from "./pages/Settings";

export type Route = { page: "overview" | "users" | "protocols" | "settings"; arg?: string };

function parseHash(): Route {
  const [page, arg] = window.location.hash.replace(/^#\/?/, "").split("/");
  if (page === "users" || page === "protocols" || page === "settings") return { page, arg };
  return { page: "overview" };
}

export function navigate(page: Route["page"], arg?: string) {
  window.location.hash = "/" + page + (arg ? "/" + arg : "");
}

// ---------------------------------------------------------------- session

const SessionCtx = createContext<{ me: Me; reload: () => void }>(null as any);
export const useSession = () => useContext(SessionCtx);
export const can = (role: Role | undefined, need: Role) => {
  const rank: Record<Role, number> = { reseller: 1, admin: 2, owner: 3 };
  return rank[role ?? "reseller"] >= rank[need];
};

const nav: { page: Route["page"]; key: Key; icon: typeof Gauge; need: Role }[] = [
  { page: "overview", key: "overview", icon: Gauge, need: "reseller" },
  { page: "users", key: "users", icon: UsersIcon, need: "reseller" },
  { page: "protocols", key: "protocols", icon: RouteIcon, need: "admin" },
  { page: "settings", key: "settings", icon: SettingsIcon, need: "reseller" },
];

export const roleKey: Record<Role, Key> = { owner: "owner", admin: "adminRole", reseller: "reseller" };
export const roleColor: Record<Role, string> = { owner: "var(--color-udp)", admin: "var(--color-cdn)", reseller: "var(--color-violet)" };

export default function App() {
  const { t, lang, setLang } = useI18n();
  const [auth, setAuth] = useState<"loading" | "in" | "out">("loading");
  const [me, setMe] = useState<Me | null>(null);
  const [route, setRoute] = useState<Route>(parseHash);

  const check = useCallback(() => {
    api<Me>("me")
      .then((r) => {
        setMe(r);
        setAuth("in");
      })
      .catch(() => setAuth("out"));
  }, []);
  useEffect(() => {
    setUnauthorizedHandler(() => setAuth("out"));
    check();
    const onHash = () => setRoute(parseHash());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [check]);

  if (auth === "loading") return <Spinner />;
  if (auth === "out" || !me) return <Login onDone={check} />;

  const role = me.admin.role;
  const items = nav.filter((x) => can(role, x.need));
  const page = items.some((x) => x.page === route.page) ? route.page : "overview";
  const logout = () => api("logout", { method: "POST" }).finally(() => setAuth("out"));

  return (
    <SessionCtx.Provider value={{ me, reload: check }}>
      <div className="aurora" aria-hidden />
      <div className="relative z-[1] min-h-dvh md:grid md:grid-cols-[256px_1fr]">
        {/* side rail */}
        <aside className="hidden md:flex flex-col sticky top-0 h-dvh p-3">
          <div className="glass rounded-[22px] flex flex-col h-full">
            <div className="flex flex-col items-center gap-2 px-5 pt-6 pb-4">
              <Logo part="emblem" height={56} className="drop-shadow-[0_6px_22px_rgba(47,140,255,.35)]" />
              <Logo part="wordmark" width={150} />
              <div className="flex items-center gap-2 text-[10.5px] text-ink3">
                <span className="mono" dir="ltr">v{VERSION}</span>
              </div>
            </div>
            <div className="mx-4 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
            <nav className="flex flex-col gap-1 px-3 mt-4">
              {items.map(({ page: p, key, icon: Icon }) => {
                const on = page === p;
                return (
                  <a
                    key={p}
                    href={"#/" + p}
                    aria-current={on ? "page" : undefined}
                    className={cx(
                      "group relative flex items-center gap-3 h-11 px-3.5 rounded-xl text-[14px] transition-all duration-200",
                      on ? "bg-white/[.08] text-white font-semibold shadow-[inset_0_1px_0_rgba(255,255,255,.06)]" : "text-ink2 hover:text-ink hover:bg-white/[.04]",
                    )}
                  >
                    {on && <span className="absolute [inset-inline-start:0] top-2.5 bottom-2.5 w-[3px] rounded-full brand-gradient shadow-[0_0_12px_rgba(47,140,255,.9)]" />}
                    <Icon className={cx("size-[18px] transition-colors", on ? "text-[#8db6ff]" : "text-ink3 group-hover:text-ink2")} />
                    {t(key)}
                  </a>
                );
              })}
            </nav>

            <div className="mt-auto p-3 flex flex-col gap-1">
              <button onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="flex items-center gap-3 h-10 px-3.5 rounded-xl text-ink2 hover:text-ink hover:bg-white/[.04] text-[13.5px]">
                <Languages className="size-[18px] text-ink3" />
                {lang === "fa" ? "English" : "فارسی"}
              </button>
              <div className="mt-2 flex items-center gap-3 rounded-2xl bg-white/[.03] border border-white/[.07] p-2.5">
                <Avatar name={me.admin.username} size={36} />
                <div className="min-w-0 flex-1">
                  <div className="text-[13.5px] font-semibold truncate" dir="ltr">{me.admin.username}</div>
                  <div className="flex items-center gap-1 text-[11.5px]" style={{ color: roleColor[role] }}>
                    {me.admin.totp && <ShieldCheck className="size-3.5 text-online" aria-label={t("twoFactor")} />}
                    {t(roleKey[role])}
                  </div>
                </div>
                <button onClick={logout} aria-label={t("logout")} title={t("logout")} className="grid place-items-center size-9 rounded-xl text-ink3 hover:text-bad hover:bg-bad/10 transition-colors">
                  <LogOut className="size-[18px]" />
                </button>
              </div>
            </div>
          </div>
        </aside>

        {/* top bar on phones */}
        <header className="md:hidden sticky top-0 z-30 flex items-center gap-2.5 px-4 h-14 glass border-x-0 border-t-0 rounded-none">
          <Logo part="emblem" height={30} />
          <Logo part="wordmark" width={92} />
          <Badge color={roleColor[role]} className="ms-1 h-5 text-[11px]">{t(roleKey[role])}</Badge>
          <button onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="ms-auto text-ink2 text-[13px] h-10 px-2.5 rounded-lg">{lang === "fa" ? "EN" : "فا"}</button>
          <button onClick={logout} aria-label={t("logout")} className="text-ink2 h-10 px-2.5 rounded-lg"><LogOut className="size-[18px]" /></button>
        </header>

        <main className="min-w-0 px-4 sm:px-6 lg:px-8 pt-5 md:pt-7 pb-28 md:pb-12 max-w-[1480px] w-full mx-auto">
          {page === "overview" && <OverviewPage />}
          {page === "users" && <UsersPage arg={route.arg} />}
          {page === "protocols" && <ProtocolsPage arg={route.arg} />}
          {page === "settings" && <SettingsPage />}
          <footer className="mt-10 text-center text-[11.5px] text-ink3">Dunvex · Pedram</footer>
        </main>

        {/* bottom tabs on phones */}
        <nav className="md:hidden grid fixed bottom-0 inset-x-0 z-30 glass rounded-none border-x-0 border-b-0 pb-[env(safe-area-inset-bottom)]" style={{ gridTemplateColumns: `repeat(${items.length}, 1fr)` }}>
          {items.map(({ page: p, key, icon: Icon }) => (
            <a key={p} href={"#/" + p} aria-current={page === p ? "page" : undefined} className={cx("relative flex flex-col items-center justify-center gap-1 h-16 text-[11px] transition-colors", page === p ? "text-white" : "text-ink3")}>
              {page === p && <span className="absolute top-0 h-[3px] w-10 rounded-b-full brand-gradient" />}
              <Icon className={cx("size-5", page === p && "text-[#8db6ff]")} />
              {t(key)}
            </a>
          ))}
        </nav>
      </div>
    </SessionCtx.Provider>
  );
}

export function Mark({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden className="shrink-0 drop-shadow-[0_0_10px_rgba(110,130,255,.45)]">
      <path d="M3 12h8l5 4 5-4h8" fill="none" stroke="var(--color-reality)" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
      <path d="M3 20h8l5-4 5 4h8" fill="none" stroke="var(--color-cdn)" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
      <circle cx={16} cy={16} r={4.6} fill="#0a1022" stroke="#f1f4fa" strokeWidth={2.6} />
    </svg>
  );
}

export function PageHead({ title, eyebrow, sub, action }: { title: string; eyebrow?: React.ReactNode; sub?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4 mb-6">
      <div className="min-w-0">
        {eyebrow && <div className="text-[12.5px] text-ink3 mb-1">{eyebrow}</div>}
        <h1 className="text-[26px] sm:text-[30px] font-bold leading-tight tracking-tight">{title}</h1>
        {sub && <div className="text-ink2 text-[13.5px] mt-1.5">{sub}</div>}
      </div>
      {action && <div className="flex flex-wrap items-center gap-2">{action}</div>}
    </div>
  );
}

export function NewUserButton() {
  const { t } = useI18n();
  return (
    <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => navigate("users", "new")}>
      {t("newUser")}
    </Button>
  );
}
