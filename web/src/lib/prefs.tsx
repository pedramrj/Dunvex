import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

// Per-browser interface preferences. Stored in localStorage when available;
// everything still works (with defaults) when storage is blocked.

export type Theme = "navy" | "oled" | "light" | "system";
export type Density = "compact" | "comfortable" | "spacious";
export type FaFont = "estedad" | "vazirmatn";
export type Widget = "live" | "health" | "kpis" | "network" | "configs" | "usage" | "top" | "cores" | "events";
export const allWidgets: Widget[] = ["kpis", "live", "health", "network", "configs", "usage", "top", "cores", "events"];
/** the widgets before the list was remembered: any other one is new to a saved layout */
const firstWidgets: Widget[] = ["kpis", "live", "health", "network", "usage", "top", "cores", "events"];

export interface Prefs {
  theme: Theme;
  density: Density;
  font: FaFont;
  motion: "full" | "reduced";
  collapsed: boolean;
  favorites: string[];
  present: boolean; // presentation mode: sensitive values blurred
  focus: boolean; // focus mode: no sidebar or top bar
  quiet: boolean; // no toast for incoming alerts
  widgets: Widget[]; // overview widgets in order (hidden ones are left out)
  widgetsKnown: Widget[]; // the widgets that existed when the layout was saved
  lastRead: number; // last notification id the admin has seen
  sheetWidth: number;
  intro: "always" | "session" | "off";
}

const defaults: Prefs = {
  theme: "navy",
  density: "comfortable",
  font: "estedad",
  motion: "full",
  collapsed: false,
  favorites: [],
  present: false,
  focus: false,
  quiet: false,
  widgets: allWidgets,
  widgetsKnown: allWidgets,
  lastRead: 0,
  sheetWidth: 640,
  intro: "session",
};

const KEY = "dvx-prefs";

function load(): Prefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const p: Prefs = { ...defaults, ...JSON.parse(raw) };
      // a widget added since the layout was saved shows up after its neighbour
      const known = p.widgetsKnown ?? firstWidgets;
      for (const w of allWidgets.filter((x) => !known.includes(x))) {
        const before = allWidgets.slice(0, allWidgets.indexOf(w)).reverse().find((x) => p.widgets.includes(x));
        const list = p.widgets.filter((x) => x !== w);
        list.splice(before ? list.indexOf(before) + 1 : 0, 0, w);
        p.widgets = list;
      }
      p.widgetsKnown = allWidgets;
      return p;
    }
  } catch {
    /* storage blocked */
  }
  return defaults;
}

interface Ctx {
  prefs: Prefs;
  set: <K extends keyof Prefs>(k: K, v: Prefs[K]) => void;
  toggleFavorite: (id: string) => void;
  resolvedTheme: "navy" | "oled" | "light";
}

const PrefsCtx = createContext<Ctx>(null as any);
export const usePrefs = () => useContext(PrefsCtx);

function systemTheme(): "navy" | "light" {
  try {
    return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "navy";
  } catch {
    return "navy";
  }
}

export function PrefsProvider({ children }: { children: ReactNode }) {
  const [prefs, setPrefs] = useState<Prefs>(load);
  const [sys, setSys] = useState(systemTheme);

  useEffect(() => {
    try {
      localStorage.setItem(KEY, JSON.stringify(prefs));
    } catch {
      /* storage blocked */
    }
  }, [prefs]);

  useEffect(() => {
    let mq: MediaQueryList | null = null;
    try {
      mq = window.matchMedia("(prefers-color-scheme: light)");
    } catch {
      return;
    }
    const on = () => setSys(systemTheme());
    mq.addEventListener?.("change", on);
    return () => mq?.removeEventListener?.("change", on);
  }, []);

  const resolvedTheme = prefs.theme === "system" ? sys : prefs.theme;

  useEffect(() => {
    const el = document.documentElement;
    el.dataset.theme = resolvedTheme;
    el.dataset.density = prefs.density;
    el.dataset.font = prefs.font;
    el.dataset.motion = prefs.motion;
    el.dataset.present = prefs.present ? "on" : "off";
    const meta = document.querySelector('meta[name="theme-color"]');
    meta?.setAttribute("content", resolvedTheme === "light" ? "#f3f6fb" : resolvedTheme === "oled" ? "#000000" : "#080c13");
  }, [resolvedTheme, prefs.density, prefs.font, prefs.motion, prefs.present]);

  const set = useCallback(<K extends keyof Prefs>(k: K, v: Prefs[K]) => setPrefs((p) => ({ ...p, [k]: v })), []);
  const toggleFavorite = useCallback(
    (id: string) => setPrefs((p) => ({ ...p, favorites: p.favorites.includes(id) ? p.favorites.filter((x) => x !== id) : [...p.favorites, id] })),
    [],
  );
  const value = useMemo(() => ({ prefs, set, toggleFavorite, resolvedTheme }), [prefs, set, toggleFavorite, resolvedTheme]);
  return <PrefsCtx.Provider value={value}>{children}</PrefsCtx.Provider>;
}

/** Keyboard shortcut helper: ctrl/cmd + key. */
export function useHotkey(key: string, fn: () => void, opts: { mod?: boolean } = { mod: true }) {
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (opts.mod && !(e.ctrlKey || e.metaKey)) return;
      if (e.key.toLowerCase() !== key.toLowerCase()) return;
      e.preventDefault();
      fn();
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [key, fn, opts.mod]);
}
