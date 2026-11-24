import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Bell, BellOff, ChartArea, ChevronDown, Cloud, ChevronsLeft, ChevronsRight, Command, Cpu, Eye, EyeOff, Focus, Globe2, KeyRound, Languages,
  LayoutDashboard, LogOut, Maximize2, Monitor, Moon, Network, Plus, Route as RouteIcon, ScrollText, Search, Server, Settings as SettingsIcon, ShieldCheck,
  ShoppingBag, Star, Sun, UserPlus, Users as UsersIcon, Wallet, Waypoints, X, Zap,
  HeartPulse,
  Megaphone,
  HardDriveDownload,
  LifeBuoy,
} from "lucide-react";
import { api, setUnauthorizedHandler, VERSION_LABEL, type Event, type Me, type Role, type StatusInfo, type User } from "./lib/api";
import { ago, cx } from "./lib/format";
import { useI18n, type Key } from "./lib/i18n";
import { usePrefs, useHotkey, type Theme } from "./lib/prefs";
import { Avatar, Button, ConfirmProvider, Dot, EventMsg, Kbd, Menu, RingDefs, Spinner, useEscape, useToast } from "./components/ui";
import { eventText } from "./lib/events";
import { Logo } from "./components/Logo";
import { Intro, introDue } from "./components/Intro";
import { Login } from "./pages/Login";
import { OverviewPage } from "./pages/Overview";
import { UsersPage } from "./pages/Users";
import { ProtocolsPage } from "./pages/Protocols";
import { SettingsPage, settingsTabKey } from "./pages/Settings";
import { NetworkPage } from "./pages/Network";
import { AnalyticsPage } from "./pages/Analytics";
import { LogsPage } from "./pages/Logs";
import { AlertsPage } from "./pages/Alerts";
import { BroadcastPage } from "./pages/Broadcast";
import { SupportPage, SUPPORT_EVENT } from "./pages/Support";
import { SecurityPage } from "./pages/Security";
import { NodesPage } from "./pages/Nodes";
import { MissionControl } from "./pages/Mission";
import { BillingPage } from "./pages/Billing";
import { WalletPage } from "./pages/Wallet";
import { RoutingPage } from "./pages/Routing";
import { CDNPage } from "./pages/CDN";

// ================================================================= routes

export type Page = "overview" | "users" | "broadcast" | "support" | "protocols" | "routing" | "cdn" | "sales" | "wallet" | "network" | "nodes" | "analytics" | "logs" | "alerts" | "security" | "settings" | "mission";
export type Route = { page: Page; arg?: string };
const pages: Page[] = ["overview", "users", "broadcast", "support", "protocols", "routing", "cdn", "sales", "wallet", "network", "nodes", "analytics", "logs", "alerts", "security", "settings", "mission"];

function parseHash(): Route {
  const [page, arg] = window.location.hash.replace(/^#\/?/, "").split("/");
  if (pages.includes(page as Page)) return { page: page as Page, arg };
  return { page: "overview" };
}

export function navigate(page: Page, arg?: string) {
  window.location.hash = "/" + page + (arg ? "/" + arg : "");
}

interface NavItem { page: Page; key: Key; icon: typeof Monitor; need: Role; group: "main" | "monitor" | "admin" }
export const navItems: NavItem[] = [
  { page: "overview", key: "overview", icon: LayoutDashboard, need: "reseller", group: "main" },
  { page: "users", key: "users", icon: UsersIcon, need: "reseller", group: "main" },
  { page: "broadcast", key: "broadcast", icon: Megaphone, need: "reseller", group: "main" },
  { page: "support", key: "support", icon: LifeBuoy, need: "reseller", group: "main" },
  { page: "protocols", key: "protocols", icon: RouteIcon, need: "admin", group: "main" },
  { page: "routing", key: "routing", icon: Waypoints, need: "owner", group: "main" },
  { page: "cdn", key: "cdn", icon: Cloud, need: "owner", group: "main" },
  { page: "sales", key: "sales", icon: ShoppingBag, need: "admin", group: "main" },
  { page: "wallet", key: "wallet", icon: Wallet, need: "reseller", group: "main" },
  { page: "network", key: "networkCenter", icon: Network, need: "admin", group: "monitor" },
  { page: "nodes", key: "nodes", icon: Server, need: "admin", group: "monitor" },
  { page: "analytics", key: "analytics", icon: ChartArea, need: "admin", group: "monitor" },
  { page: "logs", key: "logs", icon: ScrollText, need: "admin", group: "monitor" },
  { page: "alerts", key: "alerts", icon: Bell, need: "admin", group: "admin" },
  { page: "security", key: "security", icon: ShieldCheck, need: "admin", group: "admin" },
  { page: "settings", key: "settings", icon: SettingsIcon, need: "reseller", group: "admin" },
];

// ================================================================= session

const SessionCtx = createContext<{ me: Me; reload: () => void; status: StatusInfo | null; events: Event[]; unread: number; markRead: () => void }>(null as any);
export const useSession = () => useContext(SessionCtx);
export const can = (role: Role | undefined, need: Role) => {
  const rank: Record<Role, number> = { reseller: 1, admin: 2, owner: 3 };
  return rank[role ?? "reseller"] >= rank[need];
};
export const roleKey: Record<Role, Key> = { owner: "owner", admin: "adminRole", reseller: "reseller" };
export const roleColor: Record<Role, string> = { owner: "var(--warn)", admin: "var(--primary2)", reseller: "var(--color-violet)" };

export default function App() {
  const { prefs } = usePrefs();
  const [auth, setAuth] = useState<"loading" | "in" | "out">("loading");
  const [me, setMe] = useState<Me | null>(null);
  const [intro, setIntro] = useState(() => introDue(prefs.intro));

  const check = useCallback(() => {
    api<Me>("me").then((r) => { setMe(r); setAuth("in"); }).catch(() => setAuth("out"));
  }, []);
  useEffect(() => {
    setUnauthorizedHandler(() => setAuth("out"));
    check();
  }, [check]);

  // one listener feeds the cursor light of every .spot card
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const el = (e.target as HTMLElement)?.closest?.(".spot") as HTMLElement | null;
      if (!el) return;
      const r = el.getBoundingClientRect();
      el.style.setProperty("--mx", `${e.clientX - r.left}px`);
      el.style.setProperty("--my", `${e.clientY - r.top}px`);
    };
    window.addEventListener("pointermove", move, { passive: true });
    return () => window.removeEventListener("pointermove", move);
  }, []);

  return (
    <ConfirmProvider>
      <RingDefs />
      <div className="grain" aria-hidden />
      {intro && <Intro onDone={() => setIntro(false)} />}
      {auth === "loading" ? <div className="aurora" /> : auth === "out" || !me ? <Login onDone={check} /> : <Shell me={me} reload={check} onOut={() => setAuth("out")} />}
    </ConfirmProvider>
  );
}

// ================================================================= shell

function Shell({ me, reload, onOut }: { me: Me; reload: () => void; onOut: () => void }) {
  const { t, lang } = useI18n();
  const { prefs, set } = usePrefs();
  const toast = useToast();
  const [route, setRoute] = useState<Route>(parseHash);
  const [palette, setPalette] = useState(false);
  const [status, setStatus] = useState<StatusInfo | null>(null);
  const [events, setEvents] = useState<Event[]>([]);
  const [mobileNav, setMobileNav] = useState(false);
  const role = me.admin.role;
  const isAdmin = can(role, "admin");
  const seen = useRef(0);

  useEffect(() => {
    const on = () => { setRoute(parseHash()); setMobileNav(false); };
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);

  // live status for the shell (a cheap endpoint) and the alert feed
  useEffect(() => {
    let alive = true;
    const load = () => api<StatusInfo>("status").then((s) => alive && setStatus(s)).catch(() => {});
    load();
    const id = setInterval(load, 5000);
    return () => { alive = false; clearInterval(id); };
  }, []);
  useEffect(() => {
    if (!isAdmin) return;
    let alive = true;
    const load = () =>
      api<Event[]>("events?limit=60").then((list) => {
        if (!alive) return;
        const top = list[0]?.id ?? 0;
        if (seen.current && top > seen.current && !prefs.quiet) {
          list.filter((e) => e.id > seen.current && ["limit", "core"].includes(e.kind)).slice(0, 2).forEach((e) => toast(eventText(e.message, lang).text, e.kind === "core" ? "error" : "info"));
        }
        seen.current = Math.max(seen.current, top);
        setEvents(list);
      }).catch(() => {});
    load();
    const id = setInterval(load, 12000);
    return () => { alive = false; clearInterval(id); };
  }, [isAdmin, prefs.quiet, toast, lang]);

  // conversations with messages nobody opened, for the Support badge
  const [supportUnread, setSupportUnread] = useState(0);
  useEffect(() => {
    let alive = true;
    const load = () => api<{ unread: number }>("support/unread").then((c) => alive && setSupportUnread(c.unread)).catch(() => {});
    load();
    const id = setInterval(load, 15000);
    window.addEventListener(SUPPORT_EVENT, load);
    return () => { alive = false; clearInterval(id); window.removeEventListener(SUPPORT_EVENT, load); };
  }, []);

  const unread = events.filter((e) => e.id > prefs.lastRead).length;
  const markRead = useCallback(() => set("lastRead", events[0]?.id ?? prefs.lastRead), [events, prefs.lastRead, set]);

  useHotkey("k", useCallback(() => setPalette((v) => !v), []));
  useHotkey("b", useCallback(() => set("collapsed", !prefs.collapsed), [prefs.collapsed, set]));
  useHotkey("j", useCallback(() => set("focus", !prefs.focus), [prefs.focus, set]));

  // the wallet is the resellers' own; owners and admins see every wallet under Sales
  const items = navItems.filter((x) => can(role, x.need) && (x.page !== "wallet" || role === "reseller"));
  const page: Page = route.page === "mission" && isAdmin ? "mission" : items.some((x) => x.page === route.page) ? route.page : "overview";
  const logout = () => api("logout", { method: "POST" }).finally(onOut);
  const ctx = { me, reload, status, events, unread, markRead };

  if (page === "mission") {
    return (
      <SessionCtx.Provider value={ctx}>
        <MissionControl />
      </SessionCtx.Provider>
    );
  }

  const collapsed = prefs.collapsed;
  const focus = prefs.focus;

  return (
    <SessionCtx.Provider value={ctx}>
      <a href="#main" className="skip-link" onClick={(e) => { e.preventDefault(); document.getElementById("main")?.focus(); }}>{t("skipToContent")}</a>
      <div className="aurora" aria-hidden />
      <div className={cx("relative z-[1] min-h-dvh md:grid transition-[grid-template-columns] duration-300 ease-out", focus ? "md:grid-cols-[0_1fr]" : collapsed ? "md:grid-cols-[84px_1fr]" : "md:grid-cols-[264px_1fr]")}>
        {!focus ? <Sidebar items={items} page={page} me={me} status={status} unread={unread} supportUnread={supportUnread} onLogout={logout} /> : <span />}

        <div className="min-w-0 flex flex-col">
          {!focus && <Topbar page={page} arg={route.arg} me={me} status={status} events={events} unread={unread} markRead={markRead} onPalette={() => setPalette(true)} onLogout={logout} onMenu={() => setMobileNav(true)} />}
          <main id="main" tabIndex={-1} className="outline-none min-w-0 flex-1 px-4 sm:px-6 lg:px-8 pt-5 md:pt-6 pb-28 md:pb-12 max-w-[1560px] w-full mx-auto">
            <div key={page} className="rise">
              {page === "overview" && <OverviewPage />}
              {page === "users" && <UsersPage arg={route.arg} />}
              {page === "broadcast" && <BroadcastPage arg={route.arg} />}
              {page === "support" && <SupportPage arg={route.arg} />}
              {page === "protocols" && <ProtocolsPage arg={route.arg} />}
              {page === "network" && <NetworkPage />}
              {page === "nodes" && <NodesPage />}
              {page === "analytics" && <AnalyticsPage />}
              {page === "logs" && <LogsPage />}
              {page === "alerts" && <AlertsPage />}
              {page === "security" && <SecurityPage />}
              {page === "settings" && <SettingsPage arg={route.arg} />}
              {page === "routing" && <RoutingPage />}
              {page === "cdn" && <CDNPage />}
              {page === "sales" && <BillingPage />}
              {page === "wallet" && <WalletPage />}
            </div>
            <footer className="mt-12 text-center text-[11.5px] text-ink3">Dunvex · Powered by Pedram · <span className="mono" dir="ltr">{VERSION_LABEL}</span></footer>
          </main>
        </div>

        {/* phones: bottom tabs */}
        {!focus && (
          <nav className="md:hidden grid fixed bottom-0 inset-x-0 z-30 glass rounded-none border-x-0 border-b-0 pb-[env(safe-area-inset-bottom)] grid-cols-5">
            {items.slice(0, 4).map(({ page: p, key, icon: Icon }) => (
              <a key={p} href={"#/" + p} aria-current={page === p ? "page" : undefined} className={cx("relative flex flex-col items-center justify-center gap-1 h-16 text-[11px] transition-colors", page === p ? "text-ink" : "text-ink3")}>
                {page === p && <span className="absolute top-0 h-[3px] w-10 rounded-b-full brand-gradient" />}
                {p === "support" && supportUnread > 0 && <span className="absolute top-2.5 [inset-inline-start:calc(50%+6px)] size-2 rounded-full bg-primary2 shadow-[0_0_8px_var(--primary)]" aria-hidden />}
                <Icon className={cx("size-5", page === p && "text-primary2")} />
                {t(key)}
              </a>
            ))}
            <button onClick={() => setMobileNav(true)} className="flex flex-col items-center justify-center gap-1 h-16 text-[11px] text-ink3">
              <Command className="size-5" />{t("more")}
            </button>
          </nav>
        )}
        {mobileNav && <MobileNav items={items} page={page} supportUnread={supportUnread} onClose={() => setMobileNav(false)} onPalette={() => { setMobileNav(false); setPalette(true); }} />}
      </div>

      {focus && (
        <button onClick={() => set("focus", false)} className="fixed top-4 [inset-inline-end:16px] z-40 glass-strong rounded-xl h-10 px-3.5 inline-flex items-center gap-2 text-[13px]">
          <X className="size-4" />{t("exitFocus")}
        </button>
      )}
      {palette && <CommandPalette items={items} onClose={() => setPalette(false)} onLogout={logout} />}
    </SessionCtx.Provider>
  );
}

// ================================================================= sidebar

function Sidebar({ items, page, me, status, unread, supportUnread, onLogout }: { items: NavItem[]; page: Page; me: Me; status: StatusInfo | null; unread: number; supportUnread: number; onLogout: () => void }) {
  const { t, n } = useI18n();
  const { prefs, set, toggleFavorite } = usePrefs();
  const [q, setQ] = useState("");
  const collapsed = prefs.collapsed;
  const role = me.admin.role;
  const shown = items.filter((x) => !q || t(x.key).toLowerCase().includes(q.toLowerCase()));
  const favs = items.filter((x) => prefs.favorites.includes(x.page));
  const groups: { id: NavItem["group"]; key: Key }[] = [{ id: "main", key: "navMain" }, { id: "monitor", key: "navMonitor" }, { id: "admin", key: "navAdmin" }];
  const healthy = status ? status.cores.every((c) => c.running || !c.installed) && status.problems === 0 : null;
  const counter = (p: Page) => (p === "alerts" ? unread : p === "users" ? status?.online ?? 0 : p === "support" ? supportUnread : 0);

  const link = (x: NavItem, fav = false) => {
    const on = page === x.page;
    const c = counter(x.page);
    return (
      <a key={(fav ? "f-" : "") + x.page} href={"#/" + x.page} aria-current={on ? "page" : undefined} data-tip={collapsed ? t(x.key) : undefined}
        className={cx("group tip tip-side relative flex items-center gap-3 h-10 rounded-xl text-[13.5px] transition-[background-color,color] duration-150",
          collapsed ? "justify-center px-0" : "px-3",
          on ? "bg-primary/14 text-ink font-semibold shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--primary)_28%,transparent)]" : "text-ink2 hover:text-ink hover:bg-fg/[.045]")}>
        {on && <span className="absolute [inset-inline-start:0] top-2 bottom-2 w-[3px] rounded-full brand-gradient shadow-[0_0_12px_var(--primary)]" />}
        <x.icon className={cx("size-[18px] shrink-0 transition-colors", on ? "text-primary2" : "text-ink3 group-hover:text-ink2")} />
        {!collapsed && <span className="flex-1 truncate">{t(x.key)}</span>}
        {c > 0 && (collapsed
          ? <span className="absolute top-1.5 [inset-inline-end:14px] size-2 rounded-full bg-primary2" />
          : <span className={cx("num text-[11px] rounded-full px-1.5 min-w-5 text-center", x.page === "alerts" ? "bg-bad/20 text-bad" : x.page === "support" ? "bg-primary/20 text-primary2" : "bg-online/15 text-online")}>{n(c)}</span>)}
        {!collapsed && !fav && (
          <button onClick={(e) => { e.preventDefault(); toggleFavorite(x.page); }} aria-label={t("pin")}
            className={cx("opacity-0 group-hover:opacity-100 focus:opacity-100 transition-opacity", prefs.favorites.includes(x.page) && "opacity-100")}>
            <Star className={cx("size-3.5", prefs.favorites.includes(x.page) ? "fill-warn text-warn" : "text-ink3")} />
          </button>
        )}
      </a>
    );
  };

  return (
    <aside className="hidden md:block sticky top-0 h-dvh p-3 overflow-hidden">
      <div className="glass rounded-[22px] flex flex-col h-full overflow-hidden">
        <div className={cx("flex items-center gap-3 h-[76px] shrink-0", collapsed ? "justify-center px-2" : "px-5")}>
          <Logo part="emblem" height={collapsed ? 30 : 36} className="drop-shadow-[0_6px_18px_color-mix(in_oklab,var(--primary)_45%,transparent)]" />
          {!collapsed && (
            <div className="min-w-0">
              <Logo part="wordmark" width={112} />
              <div className="text-[10.5px] text-ink3 mt-1 mono" dir="ltr">{VERSION_LABEL}</div>
            </div>
          )}
        </div>

        {!collapsed && (
          <div className="px-3 pb-2">
            <div className="relative">
              <Search className="size-3.5 text-ink3 absolute top-[11px] [inset-inline-start:11px] pointer-events-none" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("searchNav")} aria-label={t("searchNav")}
                className="w-full h-9 rounded-xl bg-fg/[.035] border border-hair ps-8 pe-2 text-[12.5px] outline-none focus:border-primary2/60" />
            </div>
          </div>
        )}

        <nav className="flex-1 overflow-y-auto px-3 pb-2">
          {favs.length > 0 && !q && (
            <div className="mb-2">
              {!collapsed && <div className="px-3 pt-2 pb-1 text-[12px] text-ink3 font-medium flex items-center gap-1.5"><Star className="size-3" />{t("favorites")}</div>}
              <div className="flex flex-col gap-0.5">{favs.map((x) => link(x, true))}</div>
            </div>
          )}
          {groups.map((g) => {
            const list = shown.filter((x) => x.group === g.id);
            if (!list.length) return null;
            return (
              <div key={g.id} className="mb-2">
                {!collapsed ? <div className="px-3 pt-2 pb-1 text-[12px] text-ink3 font-medium">{t(g.key)}</div> : <div className="mx-3 my-2 h-px bg-hair" />}
                <div className="flex flex-col gap-0.5">{list.map((x) => link(x))}</div>
              </div>
            );
          })}
        </nav>

        <div className="p-3 border-t border-hair shrink-0 flex flex-col gap-2">
          <a href="#/nodes" data-tip={collapsed ? t("health") : undefined}
            className={cx("tip tip-side flex items-center gap-2.5 rounded-xl px-3 h-10 text-[12.5px] border", healthy === false ? "border-bad/30 bg-bad/[.06]" : "border-hair bg-fg/[.025]", collapsed && "justify-center px-0")}>
            <Dot color={healthy === null ? "var(--text3)" : healthy ? "var(--ok)" : "var(--bad)"} pulse={!!healthy} />
            {!collapsed && <span className="flex-1 truncate">{healthy === null ? "…" : healthy ? t("allGood") : t("issues")}</span>}
            {!collapsed && status && <span className="num text-ink3">{n(status.online)} {t("online")}</span>}
          </a>
          <div className={cx("flex items-center gap-2.5 rounded-2xl bg-fg/[.025] border border-hair", collapsed ? "flex-col p-2" : "p-2.5")}>
            <Avatar name={me.admin.username} size={34} />
            {!collapsed && (
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-semibold truncate" dir="ltr">{me.admin.username}</div>
                <div className="flex items-center gap-1 text-[11.5px]" style={{ color: roleColor[role] }}>
                  {me.admin.totp && <ShieldCheck className="size-3.5 text-online" aria-label={t("twoFactor")} />}
                  {t(roleKey[role])}
                </div>
              </div>
            )}
            <button onClick={onLogout} aria-label={t("logout")} data-tip={t("logout")} className="tip grid place-items-center size-8 rounded-lg text-ink3 hover:text-bad hover:bg-bad/10 transition-colors">
              <LogOut className="size-4" />
            </button>
          </div>
          <button onClick={() => set("collapsed", !collapsed)} className="flex items-center justify-center gap-2 h-8 rounded-lg text-ink3 hover:text-ink hover:bg-fg/[.04] text-[12px]" aria-label={t("collapse")}>
            {collapsed ? <ChevronsRight className="size-4 rtl:rotate-180" /> : <><ChevronsLeft className="size-4 rtl:rotate-180" />{t("collapse")} <Kbd>Ctrl B</Kbd></>}
          </button>
        </div>
      </div>
    </aside>
  );
}

function MobileNav({ items, page, supportUnread, onClose, onPalette }: { items: NavItem[]; page: Page; supportUnread: number; onClose: () => void; onPalette: () => void }) {
  const { t, n } = useI18n();
  useEscape(true, onClose);
  return (
    <div className="md:hidden fixed inset-0 z-50">
      <div className="fade absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="sheet absolute bottom-0 inset-x-0 glass-strong rounded-t-[26px] p-4 pb-[calc(16px+env(safe-area-inset-bottom))]" style={{ ["--sheet-from" as any]: "0px" }}>
        <div className="mx-auto mb-3 h-1.5 w-12 rounded-full bg-fg/15" />
        <button onClick={onPalette} className="w-full flex items-center gap-3 h-11 px-3 rounded-xl bg-fg/[.04] border border-hair text-ink3 text-[13px] mb-3"><Search className="size-4" />{t("searchAnything")}</button>
        <div className="grid grid-cols-3 gap-2">
          {items.map(({ page: p, key, icon: Icon }) => (
            <a key={p} href={"#/" + p} className={cx("relative flex flex-col items-center gap-1.5 py-3 rounded-2xl text-[12px] border", page === p ? "border-primary/40 bg-primary/12 text-ink" : "border-hair text-ink2")}>
              {p === "support" && supportUnread > 0 && <span className="absolute top-1.5 [inset-inline-end:8px] num text-[10.5px] rounded-full px-1.5 min-w-5 text-center bg-primary/20 text-primary2">{n(supportUnread)}</span>}
              <Icon className="size-5" />{t(key)}
            </a>
          ))}
        </div>
      </div>
    </div>
  );
}

// ================================================================= top bar

function Topbar({ page, arg, me, status, events, unread, markRead, onPalette, onLogout, onMenu }: {
  page: Page; arg?: string; me: Me; status: StatusInfo | null; events: Event[]; unread: number; markRead: () => void; onPalette: () => void; onLogout: () => void; onMenu: () => void;
}) {
  const { t, lang, setLang, n } = useI18n();
  const { prefs, set, resolvedTheme } = usePrefs();
  const isAdmin = can(me.admin.role, "admin");
  const item = navItems.find((x) => x.page === page);
  const [bell, setBell] = useState(false);
  const ThemeIcon = resolvedTheme === "light" ? Sun : resolvedTheme === "oled" ? Moon : Monitor;
  const themes: { id: Theme; key: Key }[] = [{ id: "navy", key: "themeNavy" }, { id: "oled", key: "themeOled" }, { id: "light", key: "themeLight" }, { id: "system", key: "themeSystem" }];

  const crumb: Key | undefined = arg === "new" ? "newUser" : page === "settings" ? settingsTabKey(arg) : undefined;
  return (
    <header className="sticky top-0 z-30 px-4 sm:px-6 lg:px-8 pt-3">
      <div className="glass-strong rounded-2xl h-14 flex items-center gap-2 px-2.5 sm:px-3">
        <span className="md:hidden flex items-center gap-2 ps-1"><Logo part="emblem" height={26} /></span>
        <nav aria-label="breadcrumb" className="hidden sm:flex items-center gap-2 min-w-0 text-[13px] ps-1.5">
          <a href="#/overview" className="text-ink3 hover:text-ink">Dunvex</a>
          <span className="text-ink3">/</span>
          <a href={"#/" + page} className={cx(arg ? "text-ink3 hover:text-ink" : "text-ink font-semibold")}>{item ? t(item.key) : ""}</a>
          {arg && page !== "support" && <><span className="text-ink3">/</span><span className="text-ink font-semibold truncate max-w-40" dir={crumb ? undefined : "ltr"}>{crumb ? t(crumb) : arg}</span></>}
        </nav>

        <button onClick={onPalette} className="ms-auto sm:ms-4 flex items-center gap-2 h-9 px-3 rounded-xl bg-fg/[.035] border border-hair text-ink3 text-[12.5px] hover:border-hair2 hover:text-ink2 transition-colors sm:w-64">
          <Search className="size-4" />
          <span className="hidden sm:inline flex-1 min-w-0 truncate text-start">{t("searchAnything")}</span>
          <span className="hidden sm:inline"><Kbd>Ctrl K</Kbd></span>
        </button>

        <div className="ms-auto flex items-center gap-1">
          {status && (
            <span className="hidden lg:inline-flex items-center gap-2 h-8 px-3 rounded-full text-[12px] bg-online/10 text-online border border-online/25 me-1">
              <Dot color="var(--ok)" pulse size={6} /><span className="num">{n(status.online)}</span> {t("online")}
            </span>
          )}
          {isAdmin && (
            <Menu align="end" trigger={(open) => (
              <button onClick={open} data-tip={t("quickActions")} aria-label={t("quickActions")} className="tip grid place-items-center size-9 rounded-xl text-ink2 hover:text-ink hover:bg-fg/[.06]"><Plus className="size-[18px]" /></button>
            )} items={[
              { label: t("newUser"), icon: <UserPlus />, onClick: () => navigate("users", "new") },
              { label: t("addProtocol"), icon: <RouteIcon />, onClick: () => navigate("protocols", "add") },
              { divider: true, label: "" },
              { label: t("missionControl"), icon: <Maximize2 />, onClick: () => navigate("mission") },
            ]} />
          )}
          {isAdmin && (
            <div className="relative">
              <button onClick={() => { setBell(!bell); if (!bell) markRead(); }} aria-label={t("alerts")} data-tip={t("alerts")} className="tip relative grid place-items-center size-9 rounded-xl text-ink2 hover:text-ink hover:bg-fg/[.06]">
                <Bell className="size-[18px]" />
                {unread > 0 && <span className="absolute top-1 [inset-inline-end:4px] min-w-4 h-4 px-1 rounded-full bg-[#c9394a] text-[10px] font-bold text-white grid place-items-center num"><bdi dir="ltr">{unread > 9 ? n(9) + "+" : n(unread)}</bdi></span>}
              </button>
              {bell && <BellPanel events={events} onClose={() => setBell(false)} />}
            </div>
          )}
          <Menu align="end" trigger={(open) => (
            <button onClick={open} data-tip={t("theme")} aria-label={t("theme")} className="tip grid place-items-center size-9 rounded-xl text-ink2 hover:text-ink hover:bg-fg/[.06]"><ThemeIcon className="size-[18px]" /></button>
          )} items={themes.map((x) => ({ label: t(x.key) + (prefs.theme === x.id ? "  ✓" : ""), icon: x.id === "light" ? <Sun /> : x.id === "oled" ? <Moon /> : x.id === "system" ? <Monitor /> : <Zap />, onClick: () => set("theme", x.id) }))} />
          <button onClick={() => setLang(lang === "fa" ? "en" : "fa")} data-tip={t("language")} aria-label={t("language")} className="tip hidden sm:grid place-items-center h-9 px-2.5 rounded-xl text-ink2 hover:text-ink hover:bg-fg/[.06] text-[12.5px] font-semibold">
            {lang === "fa" ? "EN" : "فا"}
          </button>
          <Menu align="end" trigger={(open) => (
            <button onClick={open} aria-label={t("account")} className="flex items-center gap-2 h-9 ps-1 pe-2 rounded-xl hover:bg-fg/[.06]">
              <Avatar name={me.admin.username} size={28} />
              <ChevronDown className="size-3.5 text-ink3 hidden sm:block" />
            </button>
          )} items={[
            { label: me.admin.username, icon: <KeyRound />, onClick: () => navigate("settings", "security") },
            { label: prefs.present ? t("presentOff") : t("presentOn"), icon: prefs.present ? <EyeOff /> : <Eye />, onClick: () => set("present", !prefs.present) },
            { label: t("focusMode"), icon: <Focus />, hint: "Ctrl J", onClick: () => set("focus", true) },
            { label: prefs.quiet ? t("quietOff") : t("quietOn"), icon: prefs.quiet ? <Bell /> : <BellOff />, onClick: () => set("quiet", !prefs.quiet) },
            { label: lang === "fa" ? "English" : "فارسی", icon: <Languages />, onClick: () => setLang(lang === "fa" ? "en" : "fa") },
            { divider: true, label: "" },
            { label: t("logout"), icon: <LogOut />, danger: true, onClick: onLogout },
          ]} />
          <button onClick={onMenu} className="md:hidden grid place-items-center size-9 rounded-xl text-ink2" aria-label={t("more")}><Command className="size-[18px]" /></button>
        </div>
      </div>
    </header>
  );
}

const kindMeta: Record<string, { icon: typeof Bell; color: string; key: Key }> = {
  user: { icon: UsersIcon, color: "var(--primary2)", key: "catUser" },
  limit: { icon: Activity, color: "var(--color-violet)", key: "catLimit" },
  core: { icon: Cpu, color: "var(--warn)", key: "catCore" },
  auth: { icon: KeyRound, color: "var(--ok)", key: "catAuth" },
  settings: { icon: SettingsIcon, color: "var(--text2)", key: "catSettings" },
  node: { icon: Server, color: "var(--cyan)", key: "nodes" },
  health: { icon: HeartPulse, color: "var(--color-classic)", key: "catHealth" },
  backup: { icon: HardDriveDownload, color: "var(--warn)", key: "catBackup" },
  support: { icon: LifeBuoy, color: "var(--cyan)", key: "catSupport" },
};
export const eventMeta = (k: string) => kindMeta[k] ?? { icon: Bell, color: "var(--text2)", key: "events" as Key };

function BellPanel({ events, onClose }: { events: Event[]; onClose: () => void }) {
  const { t, lang } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const c = (e: MouseEvent) => !ref.current?.parentElement?.contains(e.target as Node) && onClose();
    setTimeout(() => window.addEventListener("mousedown", c), 0);
    return () => window.removeEventListener("mousedown", c);
  }, [onClose]);
  return (
    <div ref={ref} className="pop absolute top-11 [inset-inline-end:0] w-[min(380px,90vw)] glass-strong rounded-2xl p-2 z-40">
      <div className="flex items-center justify-between px-2 py-1.5">
        <span className="text-[13.5px] font-semibold">{t("alerts")}</span>
        <a href="#/alerts" onClick={onClose} className="text-[12px] text-primary2 hover:underline">{t("viewAll")}</a>
      </div>
      <ul className="max-h-96 overflow-y-auto">
        {events.slice(0, 12).map((e) => {
          const m = eventMeta(e.kind);
          return (
            <li key={e.id} className="flex items-start gap-2.5 px-2 py-2 rounded-xl hover:bg-fg/[.04]">
              <span className="grid place-items-center size-7 rounded-lg shrink-0" style={{ color: m.color, background: `color-mix(in oklab, ${m.color} 14%, transparent)` }}><m.icon className="size-3.5" /></span>
              <EventMsg message={e.message} className="flex-1 min-w-0 text-[12.5px]" />
              <span className="text-[11px] text-ink3 whitespace-nowrap">{ago(e.at, lang)}</span>
            </li>
          );
        })}
        {events.length === 0 && <li className="text-center text-ink3 text-[13px] py-8">{t("noEvents")}</li>}
      </ul>
    </div>
  );
}

// ================================================================= command palette

function CommandPalette({ items, onClose, onLogout }: { items: NavItem[]; onClose: () => void; onLogout: () => void }) {
  const { t, lang, setLang } = useI18n();
  const { prefs, set } = usePrefs();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const [users, setUsers] = useState<User[]>([]);
  const input = useRef<HTMLInputElement>(null);
  useEscape(true, onClose);
  useEffect(() => {
    input.current?.focus();
    api<User[]>("users").then(setUsers).catch(() => {});
  }, []);

  type Cmd = { id: string; label: string; icon: React.ReactNode; hint?: string; group: string; run: () => void };
  const cmds = useMemo<Cmd[]>(() => {
    const out: Cmd[] = items.map((x) => ({ id: "p-" + x.page, label: t(x.key), icon: <x.icon className="size-4" />, group: t("pagesGroup"), run: () => navigate(x.page) }));
    const admin = items.some((x) => x.page === "protocols");
    out.push({ id: "a-new", label: t("newUser"), icon: <UserPlus className="size-4" />, group: t("actionsGroup"), run: () => navigate("users", "new") });
    if (admin) {
      out.push(
        { id: "a-proto", label: t("addProtocol"), icon: <Plus className="size-4" />, group: t("actionsGroup"), run: () => navigate("protocols", "add") },
        { id: "a-mission", label: t("missionControl"), icon: <Maximize2 className="size-4" />, group: t("actionsGroup"), run: () => navigate("mission") },
      );
    }
    out.push(
      { id: "a-theme", label: t("theme") + ": " + (prefs.theme === "light" ? t("themeNavy") : t("themeLight")), icon: <Sun className="size-4" />, group: t("actionsGroup"), run: () => set("theme", prefs.theme === "light" ? "navy" : "light") },
      { id: "a-oled", label: t("themeOled"), icon: <Moon className="size-4" />, group: t("actionsGroup"), run: () => set("theme", "oled") },
      { id: "a-lang", label: lang === "fa" ? "English" : "فارسی", icon: <Globe2 className="size-4" />, group: t("actionsGroup"), run: () => setLang(lang === "fa" ? "en" : "fa") },
      { id: "a-focus", label: t("focusMode"), icon: <Focus className="size-4" />, hint: "Ctrl J", group: t("actionsGroup"), run: () => set("focus", !prefs.focus) },
      { id: "a-present", label: prefs.present ? t("presentOff") : t("presentOn"), icon: <Eye className="size-4" />, group: t("actionsGroup"), run: () => set("present", !prefs.present) },
      { id: "a-side", label: t("collapse"), icon: <ChevronsLeft className="size-4" />, hint: "Ctrl B", group: t("actionsGroup"), run: () => set("collapsed", !prefs.collapsed) },
      { id: "a-out", label: t("logout"), icon: <LogOut className="size-4" />, group: t("actionsGroup"), run: onLogout },
    );
    for (const u of users) out.push({ id: "u-" + u.id, label: u.name + (u.note ? " · " + u.note : ""), icon: <Avatar name={u.name} size={20} />, group: t("users"), run: () => navigate("users", String(u.id)) });
    return out;
  }, [items, users, t, lang, setLang, prefs.theme, prefs.focus, prefs.present, prefs.collapsed, set, onLogout]);

  const list = useMemo(() => {
    const s = q.trim().toLowerCase();
    const f = s ? cmds.filter((c) => c.label.toLowerCase().includes(s)) : cmds.filter((c) => !c.id.startsWith("u-"));
    return f.slice(0, 40);
  }, [cmds, q]);
  useEffect(() => setSel(0), [q]);
  const run = (c?: Cmd) => { if (!c) return; onClose(); c.run(); };

  let lastGroup = "";
  return (
    <div className="fixed inset-0 z-[66] flex items-start justify-center pt-[12vh] px-4">
      <div className="fade absolute inset-0 bg-[rgba(3,6,12,.55)] backdrop-blur-[3px]" onClick={onClose} />
      <div className="pop relative w-full max-w-xl glass-strong rounded-[22px] overflow-hidden" role="dialog" aria-label={t("commandPalette")}>
        <div className="flex items-center gap-3 px-4 h-14 border-b border-hair">
          <Search className="size-5 text-ink3" />
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("searchAnything")} className="flex-1 bg-transparent outline-none text-[15px]" aria-label={t("searchAnything")}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(list.length - 1, s + 1)); }
              if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(0, s - 1)); }
              if (e.key === "Enter") run(list[sel]);
            }} />
          <Kbd>Esc</Kbd>
        </div>
        <ul className="max-h-[52vh] overflow-y-auto p-2">
          {list.map((c, i) => {
            const head = c.group !== lastGroup ? ((lastGroup = c.group), true) : false;
            return (
              <li key={c.id}>
                {head && <div className="px-2.5 pt-2 pb-1 text-[12px] text-ink3 font-medium">{c.group}</div>}
                <button onMouseEnter={() => setSel(i)} onClick={() => run(c)}
                  className={cx("w-full flex items-center gap-3 h-10 px-2.5 rounded-xl text-[13.5px] text-start transition-colors duration-100", i === sel ? "bg-primary/14 text-ink" : "text-ink2")}>
                  <span className="text-ink3">{c.icon}</span>
                  <span className="flex-1 truncate">{c.label}</span>
                  {c.hint && <Kbd>{c.hint}</Kbd>}
                </button>
              </li>
            );
          })}
          {list.length === 0 && <li className="text-center text-ink3 text-[13px] py-10">{t("noMatch")}</li>}
        </ul>
      </div>
    </div>
  );
}

// ================================================================= page heading

export function PageHead({ title, eyebrow, sub, action }: { title: string; eyebrow?: React.ReactNode; sub?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4 mb-6">
      <div className="min-w-0">
        {eyebrow && <div className="text-[12.5px] text-ink3 mb-1">{eyebrow}</div>}
        <h1 className="text-[26px] sm:text-[30px] font-bold leading-tight tracking-tight chrome">{title}</h1>
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

export function Loading() {
  return <Spinner />;
}
