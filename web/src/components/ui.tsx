import { createContext, forwardRef, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, Check, Copy, Eye, EyeOff, GripVertical, Loader2, RefreshCw, X } from "lucide-react";
import { copyText, cx, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { usePrefs } from "../lib/prefs";
import { eventText } from "../lib/events";
import type { Status } from "../lib/api";

// ================================================================= buttons

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "ghost" | "quiet" | "danger" | "soft";
  size?: "sm" | "md" | "lg";
  loading?: boolean;
  icon?: ReactNode;
};

export function Button({ variant = "ghost", size = "md", loading, icon, className, children, disabled, ...rest }: BtnProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cx(
        "relative inline-flex items-center justify-center gap-2 rounded-xl font-medium whitespace-nowrap select-none",
        "transition-[background-color,color,box-shadow,border-color,transform,filter] duration-150 ease-out active:scale-[.97] disabled:opacity-45 disabled:pointer-events-none",
        size === "sm" && "h-8 px-3 text-[13px]",
        size === "md" && "h-10 px-4 text-sm",
        size === "lg" && "h-12 px-5 text-[15px]",
        variant === "primary" &&
          "sheen brand-gradient text-white shadow-[0_8px_24px_-10px_color-mix(in_oklab,var(--primary)_80%,transparent),inset_0_1px_0_rgba(255,255,255,.22)] hover:brightness-110",
        variant === "ghost" && "bg-fg/[.035] text-ink border border-hair hover:bg-fg/[.07] hover:border-hair2",
        variant === "soft" && "bg-primary/12 text-primary2 border border-primary/30 hover:bg-primary/20",
        variant === "quiet" && "text-ink2 hover:text-ink hover:bg-fg/[.06]",
        variant === "danger" && "bg-bad/12 text-bad border border-bad/35 hover:bg-bad/20",
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ label, className, children, tone, tip = true, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; tone?: "danger"; tip?: boolean }) {
  return (
    <button
      {...rest}
      aria-label={label}
      data-tip={tip ? label : undefined}
      className={cx(
        "tip inline-grid place-items-center size-9 rounded-xl text-ink2 transition-colors duration-150 disabled:opacity-40",
        tone === "danger" ? "hover:text-bad hover:bg-bad/10" : "hover:text-ink hover:bg-fg/[.07]",
        className,
      )}
    >
      {children}
    </button>
  );
}

/** Opens a confirmation dialog before a destructive action. */
export function ConfirmButton({ onConfirm, children, icon, size = "sm", title, body }: { onConfirm: () => void; children: ReactNode; icon?: ReactNode; size?: "sm" | "md"; title?: string; body?: string }) {
  const confirm = useConfirm();
  const { t } = useI18n();
  return (
    <Button size={size} variant="quiet" className="hover:!text-bad hover:!bg-bad/10" icon={icon}
      onClick={async () => { if (await confirm({ title: title ?? String(children), body: body ?? t("confirmDelete"), danger: true })) onConfirm(); }}>
      {children}
    </Button>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="inline-grid place-items-center min-w-5 h-5 px-1.5 rounded-md border border-hair2 bg-fg/[.04] text-[10.5px] text-ink3 mono">{children}</kbd>;
}

// ================================================================= inputs

export function Label({ children, hint }: { children: ReactNode; hint?: ReactNode }) {
  return (
    <span className="flex items-baseline justify-between gap-2 mb-1.5 text-[13px] text-ink2 font-medium">
      <span>{children}</span>
      {hint && <span className="text-[12px] text-ink3 font-normal">{hint}</span>}
    </span>
  );
}

const fieldCls =
  "w-full h-11 rounded-xl bg-rail border border-hair px-3.5 text-ink placeholder:text-ink3/80 outline-none transition-[border-color,box-shadow] duration-150 focus:border-primary2/70 focus:shadow-[0_0_0_4px_color-mix(in_oklab,var(--primary)_16%,transparent)] focus-visible:outline-none disabled:opacity-50";

/** The look of a field for a box that holds other things (chips and an input):
 * the same surface as Input, lit when anything inside has focus. */
export const fieldShellCls =
  "w-full min-h-11 rounded-xl bg-rail border border-hair px-2 py-1.5 transition-[border-color,box-shadow] duration-150 focus-within:border-primary2/70 focus-within:shadow-[0_0_0_4px_color-mix(in_oklab,var(--primary)_16%,transparent)]";

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  // panel fields are names, addresses, ports and codes: no spellcheck squiggles
  return <input spellCheck={false} {...rest} className={cx(fieldCls, className)} />;
}

export const Textarea = forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea({ className, ...rest }, ref) {
  return <textarea ref={ref} {...rest} className={cx(fieldCls, "h-auto py-2.5 min-h-24", className)} />;
});

export function Select({ value, onChange, options, className }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; className?: string }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className={cx(fieldCls, className)}>
      {options.map((o) => (
        <option key={o.value} value={o.value} className="bg-panel text-ink">
          {o.label}
        </option>
      ))}
    </select>
  );
}

export function Toggle({ checked, onChange, label, hint, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; hint?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cx("inline-flex items-start gap-3 select-none", disabled && "opacity-50 pointer-events-none")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cx(
          "relative mt-0.5 h-6 w-[42px] rounded-full transition-[background-color,box-shadow] duration-200 shrink-0",
          checked ? "bg-primary shadow-[0_0_14px_-2px_color-mix(in_oklab,var(--primary)_70%,transparent)]" : "bg-hair2",
        )}
      >
        <span className={cx("absolute top-0.5 size-5 rounded-full bg-white shadow transition-[inset-inline-start] duration-200 ease-out", checked ? "[inset-inline-start:20px]" : "[inset-inline-start:2px]")} />
      </button>
      {(label || hint) && (
        <span>
          {label && <span className="block text-sm text-ink">{label}</span>}
          {hint && <span className="block text-[12.5px] text-ink3 mt-0.5">{hint}</span>}
        </span>
      )}
    </label>
  );
}

export function Checkbox({ checked, onChange, label, indeterminate }: { checked: boolean; onChange: (v: boolean) => void; label: string; indeterminate?: boolean }) {
  return (
    <button type="button" role="checkbox" aria-checked={indeterminate ? "mixed" : checked} aria-label={label} onClick={() => onChange(!checked)}
      className={cx("grid place-items-center size-[18px] rounded-md border transition-colors duration-150", checked || indeterminate ? "bg-primary border-primary text-white" : "border-hair2 hover:border-ink3 bg-rail")}>
      {indeterminate ? <span className="w-2 h-0.5 bg-white rounded" /> : checked ? <Check className="size-3.5" strokeWidth={3} /> : null}
    </button>
  );
}

export function Segmented<T extends string | number>({ value, onChange, options, className }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode }[]; className?: string }) {
  return (
    <div className={cx("flex flex-wrap gap-1.5", className)}>
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={cx(
            "h-9 px-3.5 rounded-xl text-[13px] border transition-[background-color,border-color,color,box-shadow] duration-150 num",
            o.value === value ? "bg-primary/15 text-ink border-primary/60 shadow-[0_0_0_3px_color-mix(in_oklab,var(--primary)_14%,transparent)]" : "bg-fg/[.025] text-ink2 border-hair hover:text-ink hover:border-hair2",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Pill tabs with a sliding highlight and counts. */
export function Tabs<T extends string>({ value, onChange, items, className }: { value: T; onChange: (v: T) => void; items: { id: T; label: ReactNode; count?: number; icon?: ReactNode }[]; className?: string }) {
  const { n } = useI18n();
  const wrap = useRef<HTMLDivElement>(null);
  const [pill, setPill] = useState<{ x: number; w: number } | null>(null);
  // measured again whenever a tab changes width (a count arriving, fonts loading): in RTL the
  // first tab moves when the bar grows, so measuring once left the pill under the wrong tab
  useLayoutEffect(() => {
    const w = wrap.current;
    if (!w) return;
    const measure = () => {
      const el = w.querySelector<HTMLElement>(`[data-tab="${CSS.escape(String(value))}"]`);
      if (el) setPill((p) => (p && p.x === el.offsetLeft && p.w === el.offsetWidth ? p : { x: el.offsetLeft, w: el.offsetWidth }));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(w);
    w.querySelectorAll("[data-tab]").forEach((b) => ro.observe(b));
    return () => ro.disconnect();
  }, [value, items.length]);
  return (
    <div ref={wrap} className={cx("relative flex gap-1 overflow-x-auto p-1 rounded-2xl glass w-fit max-w-full", className)} role="tablist">
      {pill && <span className="absolute top-1 bottom-1 rounded-xl bg-fg/[.08] shadow-[inset_0_1px_0_var(--edge)] transition-[left,width] duration-250 ease-out" style={{ left: pill.x, width: pill.w }} aria-hidden />}
      {items.map((it) => (
        <button
          key={it.id}
          data-tab={it.id}
          role="tab"
          aria-selected={value === it.id}
          onClick={() => onChange(it.id)}
          className={cx("relative inline-flex items-center gap-2 h-9 px-3.5 rounded-xl text-[13px] whitespace-nowrap transition-colors duration-150", value === it.id ? "text-ink font-semibold" : "text-ink2 hover:text-ink")}
        >
          {it.icon}
          {it.label}
          {it.count !== undefined && <span className={cx("num text-[11.5px] rounded-full px-1.5 min-w-5 text-center", value === it.id ? "bg-primary/25 text-ink" : "bg-fg/[.06] text-ink3")}>{n(it.count)}</span>}
        </button>
      ))}
    </div>
  );
}

export function CopyField({ value, className, mono = true, sensitive = true }: { value: string; className?: string; mono?: boolean; sensitive?: boolean }) {
  const { t } = useI18n();
  const toast = useToast();
  const [done, setDone] = useState(false);
  return (
    <div className={cx("flex items-center gap-2 rounded-xl border border-hair bg-rail ps-3.5 pe-1 h-11", className)}>
      <bdi dir="ltr" translate="no" className={cx("flex-1 min-w-0 truncate text-[12px] text-ink2 select-all", mono && "mono", sensitive && "sensitive")}>
        {value}
      </bdi>
      <Button size="sm" variant="quiet" icon={done ? <Check className="size-4 text-online" /> : <Copy className="size-4" />}
        onClick={() => copyText(value).then(() => { setDone(true); toast(t("copied")); setTimeout(() => setDone(false), 1400); })}>
        {t("copy")}
      </Button>
    </div>
  );
}

/** Secret value, masked until revealed. */
export function Masked({ value, className }: { value: string; className?: string }) {
  const [show, setShow] = useState(false);
  const { t } = useI18n();
  return (
    <span className={cx("inline-flex items-center gap-1.5 min-w-0", className)}>
      <bdi dir="ltr" className="mono text-[12px] truncate sensitive">{show ? value : "•".repeat(Math.min(24, Math.max(8, value.length)))}</bdi>
      <IconButton label={show ? t("hide") : t("show")} className="size-7" onClick={() => setShow(!show)}>{show ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}</IconButton>
    </span>
  );
}

// ================================================================= numbers

/** A panel event's message, in Persian when the interface is Persian. */
export function EventMsg({ message, className, truncate }: { message: string; className?: string; truncate?: boolean }) {
  const { lang } = useI18n();
  const { text, dir } = eventText(message, lang);
  return (
    <span className={cx(truncate ? "truncate" : "break-words", className)} dir={dir} title={truncate ? text : undefined} style={{ textAlign: lang === "fa" ? "right" : "left" }}>
      {text}
    </span>
  );
}

const LATN = "0123456789";
const PERS = "۰۱۲۳۴۵۶۷۸۹";

/** Rolling number in the current language's digits. Each digit rolls on its own. */
export function Num({ value, digits = 0, className, suffix }: { value: number; digits?: number; className?: string; suffix?: string }) {
  const { lang } = useI18n();
  const fa = lang === "fa";
  const text = new Intl.NumberFormat(fa ? "fa-IR" : "en-US", { maximumFractionDigits: digits, minimumFractionDigits: 0 }).format(
    Number.isFinite(value) ? value : 0,
  );
  const tail = suffix === "%" && fa ? "٪" : suffix;
  const chars = [...text];
  return (
    <span className={cx("num roll", className)}>
      <span className="sr-only">{text + (tail ?? "")}</span>
      {chars.map((c, i) => {
        const key = chars.length - i; // keyed from the right so the ones column stays put
        const set = LATN.includes(c) ? LATN : PERS.includes(c) ? PERS : "";
        if (!set) return <span key={"s" + key} aria-hidden>{c}</span>;
        const n = set.indexOf(c);
        return (
          <span key={"d" + key} className="roll-d" aria-hidden>
            <span className="roll-ph">{c}</span>
            <span className="roll-col" style={{ transform: `translateY(${-n * 10}%)` }}>
              {[...set].map((d) => <span key={d}>{d}</span>)}
            </span>
          </span>
        );
      })}
      {tail && <span aria-hidden>{tail}</span>}
    </span>
  );
}

const unitsEn = ["B", "KB", "MB", "GB", "TB", "PB"];
const unitsFa = ["بایت", "کیلوبایت", "مگابایت", "گیگ", "ترابایت", "پتابایت"];

/** Byte size with a rolling number and its unit. */
export function Bytes({ value, rate, className }: { value: number; rate?: boolean; className?: string }) {
  const { lang } = useI18n();
  let v = Math.max(0, value || 0);
  let i = 0;
  while (v >= 1024 && i < unitsEn.length - 1) {
    v /= 1024;
    i++;
  }
  const unit = (lang === "fa" ? unitsFa : unitsEn)[i] + (rate ? (lang === "fa" ? "/ث" : "/s") : "");
  return (
    <span className={cx("inline-flex items-baseline gap-1 whitespace-nowrap", className)}>
      <Num value={v} digits={i === 0 ? 0 : v >= 100 ? 0 : 1} />
      <span className="text-[.62em] font-medium text-ink3">{unit}</span>
    </span>
  );
}

// ================================================================= data atoms

export function Meter({ value, max, color, className, plain, height = 6 }: { value: number; max: number; color?: string; className?: string; plain?: boolean; height?: number }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  let bg = color ?? "linear-gradient(90deg, var(--primary), var(--cyan))";
  if (!plain && max > 0) {
    if (pct >= 90) bg = "linear-gradient(90deg, color-mix(in oklab, var(--bad) 70%, var(--warn)), var(--bad))";
    else if (pct >= 75) bg = "linear-gradient(90deg, color-mix(in oklab, var(--warn) 70%, #fff), var(--warn))";
  }
  return (
    <div className={cx("rounded-full bg-fg/[.07] overflow-hidden", className)} style={{ height }} role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full transition-[width] duration-700 ease-out" style={{ width: `${max > 0 ? Math.max(pct, value > 0 ? 2 : 0) : 0}%`, background: bg }} />
    </div>
  );
}

export function Dot({ color, pulse, size = 8 }: { color: string; pulse?: boolean; size?: number }) {
  return <span className={cx("relative inline-block rounded-full shrink-0", pulse && "pulse")} style={{ background: color, color, width: size, height: size }} />;
}

/** Circular gauge (0..100). */
export function Ring({ value, size = 72, stroke = 8, color = "url(#ring-brand)", children, track = "color-mix(in oklab, var(--fg) 8%, transparent)" }: { value: number; size?: number; stroke?: number; color?: string; children?: ReactNode; track?: string }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(100, value));
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="-rotate-90" aria-hidden>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={track} strokeWidth={stroke} />
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth={stroke} strokeLinecap="round" strokeDasharray={c} strokeDashoffset={c - (pct / 100) * c}
          style={{ transition: "stroke-dashoffset .9s cubic-bezier(.23,1,.32,1)" }} />
      </svg>
      <div className="absolute inset-0 grid place-items-center text-center">{children}</div>
    </div>
  );
}

/** Shared SVG gradients for rings (rendered once in the shell). */
export function RingDefs() {
  return (
    <svg width="0" height="0" className="absolute" aria-hidden>
      <defs>
        <linearGradient id="ring-brand" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stopColor="var(--primary)" /><stop offset="100%" stopColor="var(--cyan)" /></linearGradient>
        <linearGradient id="ring-warn" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stopColor="color-mix(in oklab, var(--warn) 70%, #fff)" /><stop offset="100%" stopColor="var(--warn)" /></linearGradient>
        <linearGradient id="ring-bad" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stopColor="color-mix(in oklab, var(--bad) 70%, var(--warn))" /><stop offset="100%" stopColor="var(--bad)" /></linearGradient>
        <linearGradient id="ring-ok" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stopColor="var(--ok)" /><stop offset="100%" stopColor="var(--cyan)" /></linearGradient>
      </defs>
    </svg>
  );
}

export const ringColor = (pct: number) => (pct >= 90 ? "url(#ring-bad)" : pct >= 75 ? "url(#ring-warn)" : "url(#ring-brand)");

// deep pairs so the white initials stay readable on every one
const avatarHues = [
  ["#2c66eb", "#0c7c7f"],
  ["#c9394a", "#a8560c"],
  ["#0b7a5c", "#0c7c7f"],
  ["#6a4fd6", "#2c66eb"],
  ["#a8560c", "#c9394a"],
  ["#2f5fd0", "#6a4fd6"],
];

export function Avatar({ name, size = 36, online }: { name: string; size?: number; online?: boolean }) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  const [a, b] = avatarHues[h % avatarHues.length];
  const letters = name.replace(/[^A-Za-z0-9]/g, "").slice(0, 2).toUpperCase() || "?";
  return (
    <span className="relative inline-grid place-items-center shrink-0 rounded-xl text-white font-semibold" style={{ width: size, height: size, background: `linear-gradient(135deg, ${a}, ${b})`, fontSize: size * 0.36 }}>
      {online && <span className="spin-ring rounded-[14px]" aria-hidden />}
      <span dir="ltr">{letters}</span>
      {online !== undefined && <span className={cx("absolute -bottom-0.5 [inset-inline-end:-2px] size-3 rounded-full border-2 border-panel", online ? "bg-online glow-online" : "bg-ink3")} />}
    </span>
  );
}

export function Badge({ children, color = "var(--text2)", className, icon }: { children: ReactNode; color?: string; className?: string; icon?: ReactNode }) {
  return (
    <span className={cx("inline-flex items-center gap-1.5 h-6 px-2.5 rounded-full text-[12px] font-medium whitespace-nowrap border", className)}
      style={{ color, background: `color-mix(in oklab, ${color} 12%, transparent)`, borderColor: `color-mix(in oklab, ${color} 32%, transparent)` }}>
      {icon}
      {children}
    </span>
  );
}

export function StatusBadge({ status }: { status: Status }) {
  const { t } = useI18n();
  const m = statusMeta[status];
  return (
    <Badge color={m.color}>
      <Dot color={m.color} size={6} pulse={status === "active"} />
      {t(m.key)}
    </Badge>
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="grid place-items-center py-20 text-ink3 gap-3">
      <Loader2 className="size-7 animate-spin text-primary2" />
      {label && <span className="text-sm">{label}</span>}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cx("shimmer rounded-xl", className)} />;
}

export function Empty({ icon, title, hint, action }: { icon: ReactNode; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center text-center py-14 px-6">
      <div className="relative size-16 rounded-2xl grid place-items-center text-primary2 mb-5 bg-primary/10 border border-primary/25 shadow-[0_0_40px_-10px_color-mix(in_oklab,var(--primary)_60%,transparent)]">{icon}</div>
      <div className="text-[16px] font-semibold">{title}</div>
      {hint && <p className="text-ink2 text-sm mt-1.5 max-w-sm">{hint}</p>}
      {action && <div className="mt-6">{action}</div>}
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  const { t } = useI18n();
  return (
    <div role="alert" className="flex flex-wrap items-center gap-3 rounded-2xl border border-bad/35 bg-bad/[.07] px-4 py-3 text-[13.5px]">
      <AlertTriangle className="size-5 text-bad shrink-0" />
      <span className="flex-1 min-w-0 break-words">{message}</span>
      {onRetry && <Button size="sm" variant="ghost" icon={<RefreshCw className="size-3.5" />} onClick={onRetry}>{t("retry")}</Button>}
    </div>
  );
}

/** Card with an optional header; the light follows the cursor (spot). */
export function Panel({ title, sub, icon, action, children, className, pad = true, id }: { title?: ReactNode; sub?: ReactNode; icon?: ReactNode; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean; id?: string }) {
  return (
    <section id={id} className={cx("spot glass edge rounded-[18px] min-w-0", className)}>
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 px-[var(--pad)] pt-[calc(var(--pad)*.85)] pb-1">
          <div className="flex items-center gap-3 min-w-0">
            {icon && <span className="grid place-items-center size-8 rounded-lg bg-fg/[.05] border border-hair text-ink2 shrink-0">{icon}</span>}
            <div className="min-w-0">
              <h2 className="text-[14.5px] font-semibold truncate">{title}</h2>
              {sub && <div className="text-[12px] text-ink3 truncate">{sub}</div>}
            </div>
          </div>
          {action}
        </header>
      )}
      <div className={pad ? "p-[var(--pad)] pt-[calc(var(--pad)*.7)]" : ""}>{children}</div>
    </section>
  );
}

/** KPI readout: an LED in the accent colour, the label, the value, a coloured sub-line, the icon in a recessed well,
 *  and an optional full-width spark along the bottom. */
export function StatCard({ icon, label, value, sub, accent = "var(--primary2)", children, foot, className, trend, onClick, tip }: {
  icon: ReactNode; label: string; value: ReactNode; sub?: ReactNode; accent?: string; children?: ReactNode; foot?: ReactNode; className?: string; trend?: number | null; onClick?: () => void; tip?: string;
}) {
  const { pc } = useI18n();
  const Tag = onClick ? "button" : "div";
  return (
    <Tag onClick={onClick} title={tip} style={{ ["--k" as string]: accent }}
      className={cx("kpi mach relative rounded-[14px] min-w-0 flex flex-col text-start overflow-hidden", onClick && "transition-[border-color,transform] duration-150 hover:border-[color-mix(in_oklab,var(--k)_60%,transparent)] active:scale-[.99]", className)}>
      <div className="flex items-start gap-3 w-full px-4 pt-3.5 pb-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="kpi-led" aria-hidden />
            <span className="text-[12.5px] text-ink2 font-medium truncate">{label}</span>
            {trend !== undefined && trend !== null && Number.isFinite(trend) && (
              <span className={cx("ms-auto text-[11px] font-semibold num rounded-full px-2 py-0.5 shrink-0", trend >= 0 ? "text-online bg-online/12" : "text-bad bg-bad/12")} dir="ltr">
                {trend >= 0 ? "▲" : "▼"} {pc(Math.abs(trend), 0)}
              </span>
            )}
          </div>
          <div className="mt-2.5 text-[23px] sm:text-[24px] leading-none font-semibold tracking-tight">{value}</div>
          {sub && <div className="kpi-sub mt-2 text-[12px] num truncate">{sub}</div>}
        </div>
        <span className="kpi-icon grid place-items-center size-9 rounded-[10px] shrink-0">{icon}</span>
      </div>
      {children && <div className="mt-auto w-full px-[calc(var(--pad)*.85)] pb-[calc(var(--pad)*.85)]">{children}</div>}
      {foot && <div className={cx("w-full", !children && "mt-auto")}>{foot}</div>}
    </Tag>
  );
}

/** Half-circle gauge with ticks; the arc takes the colour of the zone it reaches. */
export function ArcGauge({ value, label, sub }: { value: number; label: ReactNode; sub?: ReactNode }) {
  const id = useId().replace(/:/g, "");
  const v = Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0));
  const R = 80, L = Math.PI * R;
  const ticks = Array.from({ length: 21 }, (_, i) => {
    const a = Math.PI + (i / 20) * Math.PI;
    const r1 = i % 5 === 0 ? 89 : 92, r2 = 97;
    return { x1: 100 + Math.cos(a) * r1, y1: 100 + Math.sin(a) * r1, x2: 100 + Math.cos(a) * r2, y2: 100 + Math.sin(a) * r2, on: i / 20 <= v / 100 };
  });
  const ang = Math.PI + (v / 100) * Math.PI;
  const tip = { x: 100 + Math.cos(ang) * R, y: 100 + Math.sin(ang) * R };
  const tone = ringColor(v);
  return (
    <div className="relative w-full max-w-[240px] mx-auto">
      <svg viewBox="0 0 200 112" className="w-full block" aria-hidden>
        <defs>
          <linearGradient id={`g${id}`} gradientUnits="userSpaceOnUse" x1="20" y1="0" x2="180" y2="0">
            <stop offset="0%" stopColor="var(--cyan)" />
            <stop offset="55%" stopColor="var(--primary2)" />
            <stop offset="78%" stopColor="var(--warn)" />
            <stop offset="100%" stopColor="var(--bad)" />
          </linearGradient>
          <filter id={`f${id}`} x="-20%" y="-20%" width="140%" height="140%"><feGaussianBlur stdDeviation="3" /></filter>
        </defs>
        {ticks.map((k, i) => <line key={i} x1={k.x1} y1={k.y1} x2={k.x2} y2={k.y2} stroke={k.on ? tone : "var(--border2)"} strokeWidth={i % 5 === 0 ? 2 : 1.2} strokeLinecap="round" opacity={k.on ? 0.9 : 0.6} />)}
        <path d="M20,100 A80,80 0 0 1 180,100" fill="none" stroke="color-mix(in oklab, var(--fg) 8%, transparent)" strokeWidth={12} strokeLinecap="round" />
        <path d="M20,100 A80,80 0 0 1 180,100" fill="none" stroke={`url(#g${id})`} strokeWidth={12} strokeLinecap="round" filter={`url(#f${id})`} opacity={0.55}
          strokeDasharray={`${(v / 100) * L} ${L}`} className="gauge-arc" />
        <path d="M20,100 A80,80 0 0 1 180,100" fill="none" stroke={`url(#g${id})`} strokeWidth={12} strokeLinecap="round"
          strokeDasharray={`${(v / 100) * L} ${L}`} className="gauge-arc" />
        {v > 0 && <circle cx={tip.x} cy={tip.y} r={7.5} fill="var(--surface)" stroke={tone} strokeWidth={3} style={{ filter: `drop-shadow(0 0 6px ${tone})` }} />}
      </svg>
      <div className="absolute inset-x-0 bottom-0 flex flex-col items-center leading-none">
        <span className="text-[30px] font-bold tracking-tight"><Num value={v} suffix="%" /></span>
        <span className="mt-1.5 text-[12.5px] font-semibold" style={{ color: tone }}>{label}</span>
        {sub && <span className="mt-1 text-[11px] text-ink3 num">{sub}</span>}
      </div>
    </div>
  );
}

export interface TimelineItem { id: string | number; icon: React.ElementType; color: string; title: ReactNode; meta?: ReactNode; tag?: ReactNode }

/** Vertical timeline: glowing nodes joined by a fading line. */
export function Timeline({ items, dense }: { items: TimelineItem[]; dense?: boolean }) {
  return (
    <ol className="relative">
      {items.map((it, i) => (
        <li key={it.id} className={cx("relative flex items-start gap-3", i < items.length - 1 && (dense ? "pb-3" : "pb-4"))}>
          {i < items.length - 1 && (
            <span aria-hidden className="absolute top-8 bottom-0 [inset-inline-start:15.5px] w-px"
              style={{ background: `linear-gradient(color-mix(in oklab, ${it.color} 60%, transparent), color-mix(in oklab, var(--border) 70%, transparent))` }} />
          )}
          <span className="relative z-[1] grid place-items-center size-8 rounded-full shrink-0 border"
            style={{ color: it.color, borderColor: `color-mix(in oklab, ${it.color} 55%, transparent)`, background: `color-mix(in oklab, ${it.color} 12%, var(--surface))`, boxShadow: `0 0 16px -3px color-mix(in oklab, ${it.color} 70%, transparent)` }}>
            <it.icon className="size-3.5" aria-hidden />
          </span>
          <div className="min-w-0 flex-1 pt-1">
            <div className="text-[13px] leading-6">{it.title}</div>
            {it.meta && <div className="text-[11.5px] text-ink3">{it.meta}</div>}
          </div>
          {it.tag && <div className="shrink-0 pt-1">{it.tag}</div>}
        </li>
      ))}
    </ol>
  );
}

export function KV({ label, children, className }: { label: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cx("min-w-0", className)}>
      <div className="text-[11.5px] text-ink3 mb-0.5">{label}</div>
      <div className="text-[13.5px] font-medium truncate">{children}</div>
    </div>
  );
}

export function SectionTitle({ children, action, color }: { children: ReactNode; action?: ReactNode; color?: string }) {
  return (
    <div className="flex items-center justify-between gap-3 mb-3">
      <h3 className="flex items-center gap-2.5 text-[13.5px] font-semibold text-ink">
        {color && <i className="h-1.5 w-6 rounded-full" style={{ background: color, boxShadow: `0 0 12px ${color}` }} />}
        {children}
      </h3>
      {action}
    </div>
  );
}

// ================================================================= chips

/** The kit's small label or toggle: quick filters, tags, picks. Its look is the
 * `.chip` block in styles.css, so a restyle happens there, not here.
 * With onClick it is a button (a toggle when `active` is given); otherwise a label,
 * optionally with a remove button. */
export function Chip({ children, icon, count, active, onClick, onRemove, removeLabel, size = "md", tone, title, disabled, className }: {
  children: ReactNode; icon?: ReactNode; count?: number; active?: boolean; onClick?: () => void; onRemove?: () => void; removeLabel?: string;
  size?: "sm" | "md"; tone?: "add"; title?: string; disabled?: boolean; className?: string;
}) {
  const { n } = useI18n();
  const inner = (
    <>
      {icon && <span className="chip-icon" aria-hidden>{icon}</span>}
      {children}
      {count !== undefined && <span className="chip-count">{n(count)}</span>}
    </>
  );
  if (onClick)
    return (
      <button type="button" className={cx("chip", className)} data-size={size} data-tone={tone} aria-pressed={active} title={title} disabled={disabled} onClick={onClick}>
        {inner}
      </button>
    );
  return (
    <span className={cx("chip", className)} data-size={size} data-tone={tone} data-active={active || undefined} title={title}>
      {inner}
      {onRemove && <button type="button" className="chip-x" onClick={onRemove} aria-label={removeLabel}><X /></button>}
    </span>
  );
}

/** A select that sits in a row of chips (a filter); lit while it narrows something. */
export function ChipSelect({ value, onChange, options, label, icon, active }: {
  value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; label: string; icon?: ReactNode; active?: boolean;
}) {
  return (
    <label className="inline-flex items-center gap-1.5">
      {icon && <span className="chip-icon" aria-hidden>{icon}</span>}
      <span className="sr-only">{label}</span>
      <select value={value} onChange={(e) => onChange(e.target.value)} className="chip chip-select" data-active={active || undefined}>
        {options.map((o) => <option key={o.value} value={o.value} className="bg-panel text-ink">{o.label}</option>)}
      </select>
    </label>
  );
}

// ================================================================= menus

export interface MenuItem {
  label: string;
  icon?: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  hint?: string;
  divider?: boolean;
}

function MenuList({ items, onClose }: { items: MenuItem[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>("button")?.focus();
  }, []);
  return (
    <div ref={ref} role="menu" className="pop glass-strong rounded-2xl p-1.5 min-w-52 shadow-[0_24px_60px_-20px_rgba(0,0,0,.6)]"
      onKeyDown={(e) => {
        const btns = [...(ref.current?.querySelectorAll<HTMLElement>("button") ?? [])];
        const i = btns.indexOf(document.activeElement as HTMLElement);
        if (e.key === "ArrowDown") { e.preventDefault(); btns[(i + 1) % btns.length]?.focus(); }
        if (e.key === "ArrowUp") { e.preventDefault(); btns[(i - 1 + btns.length) % btns.length]?.focus(); }
      }}>
      {items.map((it, i) =>
        it.divider ? <div key={i} className="my-1 h-px bg-hair" /> : (
          <button key={i} role="menuitem" onClick={() => { onClose(); it.onClick?.(); }}
            className={cx("w-full flex items-center gap-2.5 h-9 px-2.5 rounded-xl text-[13px] text-start outline-none transition-colors duration-100", it.danger ? "text-bad hover:bg-bad/10 focus:bg-bad/10" : "text-ink hover:bg-fg/[.06] focus:bg-fg/[.06]")}>
            <span className="text-ink3 [&>svg]:size-4">{it.icon}</span>
            <span className="flex-1">{it.label}</span>
            {it.hint && <span className="text-[11px] text-ink3">{it.hint}</span>}
          </button>
        ),
      )}
    </div>
  );
}

/** Dropdown menu anchored to its trigger. */
export function Menu({ trigger, items, align = "end" }: { trigger: (open: () => void) => ReactNode; items: MenuItem[]; align?: "start" | "end" }) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const anchor = useRef<HTMLSpanElement>(null);
  const { lang } = useI18n();
  const open = () => {
    const r = anchor.current?.getBoundingClientRect();
    if (!r) return;
    const rtl = lang === "fa";
    const atEnd = align === "end" ? !rtl : rtl;
    setPos({ x: atEnd ? r.right : r.left, y: r.bottom + 6 });
  };
  return (
    <>
      <span ref={anchor} className="inline-flex">{trigger(open)}</span>
      {pos && <Floating x={pos.x} y={pos.y} alignRight={(align === "end") !== (lang === "fa")} onClose={() => setPos(null)}><MenuList items={items} onClose={() => setPos(null)} /></Floating>}
    </>
  );
}

/** Right-click menu around any content. */
export function ContextMenu({ items, children, className }: { items: MenuItem[]; children: ReactNode; className?: string }) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  return (
    <div className={className} onContextMenu={(e) => { e.preventDefault(); setPos({ x: e.clientX, y: e.clientY }); }}>
      {children}
      {pos && <Floating x={pos.x} y={pos.y} onClose={() => setPos(null)}><MenuList items={items} onClose={() => setPos(null)} /></Floating>}
    </div>
  );
}

function Floating({ x, y, alignRight, onClose, children }: { x: number; y: number; alignRight?: boolean; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [style, setStyle] = useState<React.CSSProperties>({ left: x, top: y, visibility: "hidden" });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const w = el.offsetWidth, h = el.offsetHeight;
    let left = alignRight ? x - w : x;
    let top = y;
    left = Math.max(8, Math.min(left, window.innerWidth - w - 8));
    if (top + h > window.innerHeight - 8) top = Math.max(8, y - h - 12);
    setStyle({ left, top });
  }, [x, y, alignRight]);
  useEffect(() => {
    const close = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) onClose(); };
    setTimeout(() => window.addEventListener("mousedown", close), 0);
    window.addEventListener("scroll", onClose, true);
    return () => { window.removeEventListener("mousedown", close); window.removeEventListener("scroll", onClose, true); };
  }, [onClose]);
  useEscape(true, onClose);
  return createPortal(<div ref={ref} className="fixed z-[70]" style={style}>{children}</div>, document.body);
}

// ================================================================= dialogs

interface ConfirmOpts { title: string; body?: string; danger?: boolean; confirmLabel?: string }
const ConfirmCtx = createContext<(o: ConfirmOpts) => Promise<boolean>>(async () => false);
export const useConfirm = () => useContext(ConfirmCtx);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [req, setReq] = useState<(ConfirmOpts & { resolve: (v: boolean) => void }) | null>(null);
  const ask = useCallback((o: ConfirmOpts) => new Promise<boolean>((resolve) => setReq({ ...o, resolve })), []);
  const done = (v: boolean) => { req?.resolve(v); setReq(null); };
  return (
    <ConfirmCtx.Provider value={ask}>
      {children}
      <Dialog open={!!req} onClose={() => done(false)} title={req?.title ?? ""} danger={req?.danger}
        footer={<ConfirmFooter danger={req?.danger} label={req?.confirmLabel} onCancel={() => done(false)} onOk={() => done(true)} />}>
        {req?.body && <p className="text-ink2 text-[14px] leading-relaxed">{req.body}</p>}
      </Dialog>
    </ConfirmCtx.Provider>
  );
}

function ConfirmFooter({ danger, label, onCancel, onOk }: { danger?: boolean; label?: string; onCancel: () => void; onOk: () => void }) {
  const { t } = useI18n();
  return (
    <>
      {/* destructive confirmations start on Cancel so Enter never deletes by accident */}
      <Button variant="quiet" onClick={onCancel} autoFocus={danger}>{t("cancel")}</Button>
      <Button variant={danger ? "danger" : "primary"} onClick={onOk} autoFocus={!danger}>{label ?? (danger ? t("delete") : t("ok"))}</Button>
    </>
  );
}

// ================================================================= layered Escape
// Menus, dialogs and sheets stack; Escape closes only the one on top.
const escStack: (() => void)[] = [];
let escBound = false;
export function useEscape(active: boolean, onClose: () => void) {
  const cb = useRef(onClose);
  cb.current = onClose;
  useEffect(() => {
    if (!active) return;
    const h = () => cb.current();
    escStack.push(h);
    if (!escBound) {
      escBound = true;
      window.addEventListener("keydown", (e) => {
        if (e.key !== "Escape" || !escStack.length) return;
        e.preventDefault();
        escStack[escStack.length - 1]();
      });
    }
    return () => {
      const i = escStack.lastIndexOf(h);
      if (i >= 0) escStack.splice(i, 1);
    };
  }, [active]);
}

/** Centered modal dialog. */
export function Dialog({ open, onClose, title, children, footer, danger, wide }: { open: boolean; onClose: () => void; title: ReactNode; children?: ReactNode; footer?: ReactNode; danger?: boolean; wide?: boolean }) {
  useEscape(open, onClose);
  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-[65] flex items-center justify-center p-4">
      <div className="fade absolute inset-0 bg-[rgba(3,6,12,.6)] backdrop-blur-[3px]" onClick={onClose} />
      {/* flex, not grid: a grid track would grow to the content's width and push the dialog off a phone screen */}
      <div role="alertdialog" aria-modal="true" className={cx("pop relative glass-strong rounded-[22px] w-full min-w-0 max-h-full overflow-y-auto p-6", wide ? "max-w-2xl" : "max-w-md")} style={{ ["--origin" as any]: "center" }}>
        <div className="flex items-start gap-3">
          {danger && <span className="grid place-items-center size-10 rounded-xl bg-bad/12 text-bad shrink-0"><AlertTriangle className="size-5" /></span>}
          <div className="min-w-0 flex-1">
            <h2 className="text-[17px] font-semibold mb-2">{title}</h2>
            {children}
          </div>
        </div>
        {footer && <div className="flex justify-end gap-2 mt-6">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

// ================================================================= sheet (resizable side panel)

export function Sheet({ open, onClose, title, sub, children, footer, wide }: { open: boolean; onClose: () => void; title: ReactNode; sub?: ReactNode; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const { lang, t } = useI18n();
  const { prefs, set } = usePrefs();
  const ref = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(wide ? prefs.sheetWidth : 500);
  useEscape(open, onClose);
  useEffect(() => {
    if (!open) return;
    ref.current?.focus();
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open, onClose]);
  if (!open) return null;
  const startResize = (e: React.PointerEvent) => {
    if (!wide) return;
    e.preventDefault();
    const x0 = e.clientX, w0 = w;
    const move = (ev: PointerEvent) => {
      const d = lang === "fa" ? ev.clientX - x0 : x0 - ev.clientX;
      setW(Math.max(440, Math.min(window.innerWidth - 80, w0 + d)));
    };
    const up = () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); setW((v) => (set("sheetWidth", v), v)); };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="fade absolute inset-0 bg-[rgba(3,6,12,.55)] backdrop-blur-[2px]" onClick={onClose} />
      <div ref={ref} tabIndex={-1} role="dialog" aria-modal="true"
        style={{ ["--sheet-from" as any]: lang === "fa" ? "-40px" : "40px", width: `min(100%, ${w}px)` }}
        className="sheet glass-strong absolute top-0 bottom-0 [inset-inline-end:0] flex flex-col outline-none focus-visible:outline-none border-y-0 border-e-0 rounded-none">
        {wide && (
          <div onPointerDown={startResize} className="hidden sm:grid absolute top-0 bottom-0 [inset-inline-start:-6px] w-3 cursor-col-resize place-items-center group z-10" aria-label="resize">
            <GripVertical className="size-4 text-ink3 opacity-0 group-hover:opacity-100 transition-opacity" />
          </div>
        )}
        <header className="flex items-center justify-between gap-3 px-6 h-[68px] border-b border-hair shrink-0">
          <div className="min-w-0">
            <h2 className="text-[16px] font-semibold truncate">{title}</h2>
            {sub && <div className="text-[12.5px] text-ink3 truncate">{sub}</div>}
          </div>
          <IconButton label={t("close")} onClick={onClose}><X className="size-5" /></IconButton>
        </header>
        <div className="flex-1 overflow-y-auto overscroll-contain px-6 py-6">{children}</div>
        {footer && <footer className="px-6 py-4 border-t border-hair flex items-center justify-end gap-2 shrink-0 bg-fg/[.015]">{footer}</footer>}
      </div>
    </div>,
    document.body,
  );
}

// ================================================================= toasts

type ToastFn = (msg: string, kind?: "ok" | "error" | "info") => void;
const ToastCtx = createContext<ToastFn>(() => {});
export const useToast = () => useContext(ToastCtx);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<{ id: number; msg: string; kind: "ok" | "error" | "info" }[]>([]);
  const push = useCallback<ToastFn>((msg, kind = "ok") => {
    const id = Date.now() + Math.random();
    setItems((x) => [...x.slice(-3), { id, msg, kind }]);
    setTimeout(() => setItems((x) => x.filter((i) => i.id !== id)), kind === "error" ? 6000 : 2800);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="fixed z-[75] bottom-24 sm:bottom-6 inset-x-0 flex flex-col items-center gap-2 pointer-events-none px-4" aria-live="polite">
        {items.map((i) => (
          <div key={i.id} role="status"
            className={cx("rise pointer-events-auto max-w-md rounded-2xl px-4 py-3 text-sm border backdrop-blur-xl flex items-center gap-2.5 glass-strong",
              i.kind === "error" && "!border-bad/45")}>
            {i.kind === "error" ? <X className="size-4 text-bad shrink-0" /> : i.kind === "info" ? <span className="size-2 rounded-full bg-primary2 shrink-0" /> : <Check className="size-4 text-online shrink-0" />}
            {i.msg}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
