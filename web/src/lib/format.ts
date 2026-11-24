import type { Group, Status } from "./api";
import type { Key, Lang } from "./i18n";

const unitsEn = ["B", "KB", "MB", "GB", "TB", "PB"];
const unitsFa = ["بایت", "کیلوبایت", "مگابایت", "گیگ", "ترابایت", "پتابایت"];

export function bytes(b: number, lang: Lang, digits = 1): string {
  const units = lang === "fa" ? unitsFa : unitsEn;
  if (!b || b < 0) return (lang === "fa" ? "۰ " : "0 ") + units[0];
  let i = 0;
  let v = b;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const d = i === 0 ? 0 : v >= 100 ? 0 : digits;
  const s = new Intl.NumberFormat(lang === "fa" ? "fa-IR" : "en-US", { maximumFractionDigits: d }).format(v);
  return s + " " + units[i];
}

export function speed(bps: number, lang: Lang): string {
  return bytes(bps, lang) + (lang === "fa" ? "/ث" : "/s");
}

export const GB = 1024 ** 3;

export function duration(sec: number, lang: Lang): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const nf = new Intl.NumberFormat(lang === "fa" ? "fa-IR" : "en-US");
  if (lang === "fa") {
    if (d > 0) return `${nf.format(d)} روز ${nf.format(h)} ساعت`;
    if (h > 0) return `${nf.format(h)} ساعت ${nf.format(m)} دقیقه`;
    return `${nf.format(m)} دقیقه`;
  }
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

/** "in 2 hours": a time still to come. */
export function until(unix: number, lang: Lang): string {
  const s = Math.max(0, unix - Math.floor(Date.now() / 1000));
  const rtf = new Intl.RelativeTimeFormat(lang === "fa" ? "fa" : "en", { numeric: "auto" });
  if (s < 60) return rtf.format(1, "minute");
  if (s < 3600) return rtf.format(Math.ceil(s / 60), "minute");
  if (s < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

export function ago(unix: number, lang: Lang): string {
  if (!unix) return lang === "fa" ? "هرگز" : "never";
  const s = Math.max(0, Math.floor(Date.now() / 1000) - unix);
  const rtf = new Intl.RelativeTimeFormat(lang === "fa" ? "fa" : "en", { numeric: "auto" });
  if (s < 60) return rtf.format(-s, "second");
  if (s < 3600) return rtf.format(-Math.floor(s / 60), "minute");
  if (s < 86400) return rtf.format(-Math.floor(s / 3600), "hour");
  return rtf.format(-Math.floor(s / 86400), "day");
}

export function date(unix: number, lang: Lang): string {
  if (!unix) return "—";
  return new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", { year: "numeric", month: "short", day: "numeric" }).format(new Date(unix * 1000));
}

export const groups: { id: Group; color: string; label: Key }[] = [
  { id: "reality", color: "var(--color-reality)", label: "lineReality" },
  { id: "cdn", color: "var(--color-cdn)", label: "lineCdn" },
  { id: "udp", color: "var(--color-udp)", label: "lineUdp" },
  { id: "classic", color: "var(--color-classic)", label: "lineClassic" },
];

export const groupColor = (g: Group) => groups.find((x) => x.id === g)?.color ?? "var(--color-ink2)";

export const statusMeta: Record<Status, { color: string; key: Key }> = {
  active: { color: "var(--color-online)", key: "active" },
  pending: { color: "var(--color-cdn)", key: "pending" },
  limited: { color: "var(--color-warn)", key: "limited" },
  expired: { color: "var(--color-bad)", key: "expired" },
  disabled: { color: "var(--color-ink3)", key: "disabled" },
  paused: { color: "var(--color-violet)", key: "paused" },
};

export function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  ta.remove();
  return Promise.resolve();
}

/** A country's name from its ISO code, in the panel's language. */
export function regionName(cc: string, lang: Lang): string {
  try {
    return new Intl.DisplayNames([lang === "fa" ? "fa" : "en"], { type: "region" }).of(cc.toUpperCase()) ?? cc;
  } catch {
    return cc;
  }
}

export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");
