import { useCallback, useEffect, useState } from "react";
import { Gauge, Languages, LogOut, Plus, Route, Settings as SettingsIcon, Users as UsersIcon } from "lucide-react";
import { api, setUnauthorizedHandler, VERSION } from "./lib/api";
import { cx } from "./lib/format";
import { useI18n, type Key } from "./lib/i18n";
import { Button, Spinner } from "./components/ui";
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

const nav: { page: Route["page"]; key: Key; icon: typeof Gauge }[] = [
  { page: "overview", key: "overview", icon: Gauge },
  { page: "users", key: "users", icon: UsersIcon },
  { page: "protocols", key: "protocols", icon: Route },
  { page: "settings", key: "settings", icon: SettingsIcon },
];

export default function App() {
  const { t, lang, setLang } = useI18n();
  const [auth, setAuth] = useState<"loading" | "in" | "out">("loading");
  const [admin, setAdmin] = useState("");
  const [route, setRoute] = useState<Route>(parseHash);

  const check = useCallback(() => {
    api<{ admin: { username: string } }>("me")
      .then((r) => {
        setAdmin(r.admin?.username ?? "");
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
  if (auth === "out") return <Login onDone={check} />;

  const logout = () => api("logout", { method: "POST" }).finally(() => setAuth("out"));

  return (
    <div className="min-h-dvh md:grid md:grid-cols-[232px_1fr]">
      {/* rail */}
      <aside className="hidden md:flex flex-col sticky top-0 h-dvh bg-rail border-e border-hair">
        <div className="flex items-center gap-2.5 px-5 h-16">
          <Mark />
          <span className="text-[17px] font-bold tracking-tight">{t("brand")}</span>
        </div>
        <nav className="flex flex-col gap-1 px-3 mt-2">
          {nav.map(({ page, key, icon: Icon }) => (
            <a
              key={page}
              href={"#/" + page}
              aria-current={route.page === page ? "page" : undefined}
              className={cx(
                "group relative flex items-center gap-3 h-10 px-3 rounded-xl text-[14px] transition-colors",
                route.page === page ? "bg-panel2 text-ink font-semibold" : "text-ink2 hover:text-ink hover:bg-panel",
              )}
            >
              {route.page === page && <span className="absolute [inset-inline-start:-12px] top-2 bottom-2 w-1 rounded-full bg-ink" />}
              <Icon className="size-[18px]" />
              {t(key)}
            </a>
          ))}
        </nav>
        <div className="mt-auto px-3 pb-4 flex flex-col gap-1">
          <button onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="flex items-center gap-3 h-10 px-3 rounded-xl text-ink2 hover:text-ink hover:bg-panel text-[14px]">
            <Languages className="size-[18px]" />
            {lang === "fa" ? "English" : "فارسی"}
          </button>
          <button onClick={logout} className="flex items-center gap-3 h-10 px-3 rounded-xl text-ink2 hover:text-ink hover:bg-panel text-[14px]">
            <LogOut className="size-[18px]" />
            {t("logout")}
          </button>
          <div className="px-3 pt-3 text-[11.5px] text-ink3 flex justify-between">
            <span dir="ltr">{admin}</span>
            <span dir="ltr" className="num">Dunvex {VERSION}</span>
          </div>
        </div>
      </aside>

      {/* top bar on phones */}
      <header className="md:hidden sticky top-0 z-30 flex items-center gap-2 px-4 h-14 bg-rail/95 backdrop-blur border-b border-hair">
        <Mark />
        <span className="font-bold">{t("brand")}</span>
        <button onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="ms-auto text-ink2 text-[13px] h-9 px-2">{lang === "fa" ? "EN" : "فا"}</button>
        <button onClick={logout} aria-label={t("logout")} className="text-ink2 h-9 px-2"><LogOut className="size-[18px]" /></button>
      </header>

      <main className="min-w-0 px-4 sm:px-6 lg:px-8 pt-5 pb-28 md:pb-10 max-w-[1440px] w-full mx-auto">
        {route.page === "overview" && <OverviewPage />}
        {route.page === "users" && <UsersPage arg={route.arg} />}
        {route.page === "protocols" && <ProtocolsPage arg={route.arg} />}
        {route.page === "settings" && <SettingsPage />}
      </main>

      {/* bottom tabs on phones */}
      <nav className="md:hidden fixed bottom-0 inset-x-0 z-30 grid grid-cols-4 bg-rail/95 backdrop-blur border-t border-hair pb-[env(safe-area-inset-bottom)]">
        {nav.map(({ page, key, icon: Icon }) => (
          <a key={page} href={"#/" + page} className={cx("flex flex-col items-center justify-center gap-0.5 h-16 text-[11px]", route.page === page ? "text-ink" : "text-ink3")}>
            <Icon className="size-5" />
            {t(key)}
          </a>
        ))}
      </nav>
    </div>
  );
}

export function Mark({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
      <path d="M3 12h8l5 4 5-4h8" fill="none" stroke="var(--color-reality)" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
      <path d="M3 20h8l5-4 5 4h8" fill="none" stroke="var(--color-cdn)" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" />
      <circle cx={16} cy={16} r={4.6} fill="var(--color-rail)" stroke="var(--color-ink)" strokeWidth={2.6} />
    </svg>
  );
}

export function PageHead({ title, sub, action }: { title: string; sub?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3 mb-5">
      <div className="min-w-0">
        <h1 className="text-[22px] sm:text-[26px] font-bold leading-tight">{title}</h1>
        {sub && <div className="text-ink2 text-[13.5px] mt-1">{sub}</div>}
      </div>
      {action}
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
