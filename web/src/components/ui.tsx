import { createContext, useCallback, useContext, useEffect, useRef, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Loader2, X } from "lucide-react";
import { copyText, cx, statusMeta } from "../lib/format";
import { useI18n } from "../lib/i18n";
import type { Status } from "../lib/api";

// ---------------------------------------------------------------- buttons

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
        "relative inline-flex items-center justify-center gap-2 rounded-xl font-medium whitespace-nowrap select-none cursor-pointer",
        "transition-[background,color,box-shadow,border-color,transform,filter] duration-200 active:scale-[.97] disabled:opacity-45 disabled:pointer-events-none",
        size === "sm" && "h-8 px-3 text-[13px]",
        size === "md" && "h-10 px-4 text-sm",
        size === "lg" && "h-12 px-5 text-[15px]",
        variant === "primary" &&
          "brand-gradient text-white shadow-[0_10px_30px_-10px_rgba(47,140,255,.7),inset_0_1px_0_rgba(255,255,255,.25)] hover:brightness-110 hover:shadow-[0_14px_34px_-10px_rgba(47,140,255,.9),inset_0_1px_0_rgba(255,255,255,.3)]",
        variant === "ghost" && "bg-white/[.04] text-ink border border-white/10 hover:bg-white/[.08] hover:border-white/20",
        variant === "soft" && "bg-cdn/12 text-[#a9c8ff] border border-cdn/25 hover:bg-cdn/20",
        variant === "quiet" && "text-ink2 hover:text-ink hover:bg-white/[.06]",
        variant === "danger" && "bg-bad/12 text-bad border border-bad/35 hover:bg-bad/20",
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ label, className, children, tone, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; tone?: "danger" }) {
  return (
    <button
      {...rest}
      aria-label={label}
      title={label}
      className={cx(
        "inline-grid place-items-center size-9 rounded-xl text-ink2 cursor-pointer transition-colors disabled:opacity-40",
        tone === "danger" ? "hover:text-bad hover:bg-bad/10" : "hover:text-ink hover:bg-white/[.07]",
        className,
      )}
    >
      {children}
    </button>
  );
}

/** Two-step destructive button: first press arms it, second press confirms. */
export function ConfirmButton({ onConfirm, children, icon, size = "sm" }: { onConfirm: () => void; children: ReactNode; icon?: ReactNode; size?: "sm" | "md" }) {
  const { t } = useI18n();
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const id = setTimeout(() => setArmed(false), 3500);
    return () => clearTimeout(id);
  }, [armed]);
  return (
    <Button size={size} variant={armed ? "danger" : "quiet"} icon={icon} onClick={() => (armed ? (setArmed(false), onConfirm()) : setArmed(true))}>
      {armed ? t("confirmDelete") : children}
    </Button>
  );
}

// ---------------------------------------------------------------- inputs

export function Label({ children, hint }: { children: ReactNode; hint?: ReactNode }) {
  return (
    <span className="flex items-baseline justify-between gap-2 mb-1.5 text-[13px] text-ink2 font-medium">
      <span>{children}</span>
      {hint && <span className="text-[12px] text-ink3 font-normal">{hint}</span>}
    </span>
  );
}

const fieldCls =
  "w-full h-11 rounded-xl bg-[#070c1a]/80 border border-white/10 px-3.5 text-ink placeholder:text-ink3/80 outline-none transition-[border-color,box-shadow] duration-200 focus:border-cdn/70 focus:shadow-[0_0_0_4px_rgba(76,141,255,.14)] focus-visible:outline-none disabled:opacity-50";

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={cx(fieldCls, className)} />;
}

export function Textarea({ className, ...rest }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...rest} className={cx(fieldCls, "h-auto py-2.5 min-h-24", className)} />;
}

export function Select({ value, onChange, options, className }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; className?: string }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className={cx(fieldCls, "cursor-pointer", className)}>
      {options.map((o) => (
        <option key={o.value} value={o.value} className="bg-panel">
          {o.label}
        </option>
      ))}
    </select>
  );
}

export function Toggle({ checked, onChange, label, hint, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; hint?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cx("inline-flex items-start gap-3 cursor-pointer select-none", disabled && "opacity-50 pointer-events-none")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cx(
          "relative mt-0.5 h-6 w-[42px] rounded-full transition-[background,box-shadow] duration-200 shrink-0 cursor-pointer",
          checked ? "bg-online shadow-[0_0_14px_-2px_rgba(61,220,151,.6)]" : "bg-hair2",
        )}
      >
        <span
          className={cx(
            "absolute top-0.5 size-5 rounded-full bg-white shadow transition-[inset-inline-start] duration-200",
            checked ? "[inset-inline-start:20px]" : "[inset-inline-start:2px]",
          )}
        />
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

/** Segmented choice (quick picks such as 30/60/90 days). */
export function Segmented<T extends string | number>({ value, onChange, options, className }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode }[]; className?: string }) {
  return (
    <div className={cx("flex flex-wrap gap-1.5", className)}>
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={cx(
            "h-9 px-3.5 rounded-xl text-[13px] border transition-all duration-200 num cursor-pointer",
            o.value === value
              ? "bg-cdn/18 text-white border-cdn/60 shadow-[0_0_0_3px_rgba(76,141,255,.12)]"
              : "bg-white/[.03] text-ink2 border-white/10 hover:text-ink hover:border-white/25",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Pill tabs with counts. */
export function Tabs<T extends string>({ value, onChange, items, className }: { value: T; onChange: (v: T) => void; items: { id: T; label: ReactNode; count?: number; icon?: ReactNode }[]; className?: string }) {
  const { n } = useI18n();
  return (
    <div className={cx("flex gap-1 overflow-x-auto p-1 rounded-2xl glass w-fit max-w-full", className)} role="tablist">
      {items.map((it) => (
        <button
          key={it.id}
          role="tab"
          aria-selected={value === it.id}
          onClick={() => onChange(it.id)}
          className={cx(
            "inline-flex items-center gap-2 h-9 px-3.5 rounded-xl text-[13px] whitespace-nowrap transition-all duration-200 cursor-pointer",
            value === it.id ? "bg-white/[.1] text-white shadow-[inset_0_1px_0_rgba(255,255,255,.08)] font-semibold" : "text-ink2 hover:text-ink",
          )}
        >
          {it.icon}
          {it.label}
          {it.count !== undefined && (
            <span className={cx("num text-[11.5px] rounded-full px-1.5 min-w-5 text-center", value === it.id ? "bg-cdn/30 text-white" : "bg-white/[.06] text-ink3")}>{n(it.count)}</span>
          )}
        </button>
      ))}
    </div>
  );
}

export function CopyField({ value, className, mono = true }: { value: string; className?: string; mono?: boolean }) {
  const { t } = useI18n();
  const toast = useToast();
  const [done, setDone] = useState(false);
  return (
    <div className={cx("flex items-center gap-2 rounded-xl border border-white/10 bg-[#070c1a]/80 ps-3.5 pe-1 h-11", className)}>
      <bdi dir="ltr" className={cx("flex-1 min-w-0 truncate text-[12px] text-ink2 select-all", mono && "mono")}>
        {value}
      </bdi>
      <Button
        size="sm"
        variant="quiet"
        icon={done ? <Check className="size-4 text-online" /> : <Copy className="size-4" />}
        onClick={() =>
          copyText(value).then(() => {
            setDone(true);
            toast(t("copied"));
            setTimeout(() => setDone(false), 1400);
          })
        }
      >
        {t("copy")}
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------- data atoms

/** Thin bar; turns amber/red near the limit unless `plain`. */
export function Meter({ value, max, color, className, plain, height = 6 }: { value: number; max: number; color?: string; className?: string; plain?: boolean; height?: number }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  let bg = color ?? "linear-gradient(90deg, #1f6bff, #35b8ff)";
  if (!plain && max > 0) {
    if (pct >= 90) bg = "linear-gradient(90deg, #ff8a5c, #ff5d73)";
    else if (pct >= 75) bg = "linear-gradient(90deg, #f2c94c, #f2a93b)";
  }
  return (
    <div className={cx("rounded-full bg-white/[.06] overflow-hidden", className)} style={{ height }}>
      <div className="h-full rounded-full transition-[width] duration-700 ease-out" style={{ width: `${max > 0 ? Math.max(pct, value > 0 ? 2 : 0) : 0}%`, background: bg }} />
    </div>
  );
}

export function Dot({ color, pulse, size = 8 }: { color: string; pulse?: boolean; size?: number }) {
  return <span className={cx("relative inline-block rounded-full shrink-0", pulse && "pulse")} style={{ background: color, color, width: size, height: size }} />;
}

/** Circular gauge (0..100). */
export function Ring({ value, size = 72, stroke = 8, color = "url(#ring-brand)", children, track = "rgba(255,255,255,.07)" }: { value: number; size?: number; stroke?: number; color?: string; children?: ReactNode; track?: string }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(100, value));
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="-rotate-90" aria-hidden>
        <defs>
          <linearGradient id="ring-brand" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#1f6bff" />
            <stop offset="100%" stopColor="#35b8ff" />
          </linearGradient>
          <linearGradient id="ring-warn" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#f2c94c" />
            <stop offset="100%" stopColor="#f2a93b" />
          </linearGradient>
          <linearGradient id="ring-bad" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#ff8a5c" />
            <stop offset="100%" stopColor="#ff5d73" />
          </linearGradient>
          <linearGradient id="ring-ok" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#3ddc97" />
            <stop offset="100%" stopColor="#38c8e8" />
          </linearGradient>
        </defs>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={track} strokeWidth={stroke} />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke={color}
          strokeWidth={stroke}
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={c - (pct / 100) * c}
          style={{ transition: "stroke-dashoffset .8s cubic-bezier(.16,1,.3,1)" }}
        />
      </svg>
      <div className="absolute inset-0 grid place-items-center text-center">{children}</div>
    </div>
  );
}

export const ringColor = (pct: number) => (pct >= 90 ? "url(#ring-bad)" : pct >= 75 ? "url(#ring-warn)" : "url(#ring-brand)");

const avatarHues = [
  ["#4c8dff", "#9b7bff"],
  ["#f0474f", "#f2a93b"],
  ["#34b37a", "#38c8e8"],
  ["#9b7bff", "#f0479b"],
  ["#f2a93b", "#f0474f"],
  ["#38c8e8", "#4c8dff"],
];

/** Initials on a gradient picked from the name. */
export function Avatar({ name, size = 36, online }: { name: string; size?: number; online?: boolean }) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  const [a, b] = avatarHues[h % avatarHues.length];
  const letters = name.replace(/[^A-Za-z0-9]/g, "").slice(0, 2).toUpperCase() || "?";
  return (
    <span className="relative inline-grid place-items-center shrink-0 rounded-xl text-white font-semibold" style={{ width: size, height: size, background: `linear-gradient(135deg, ${a}, ${b})`, fontSize: size * 0.36 }}>
      <span dir="ltr">{letters}</span>
      {online !== undefined && (
        <span className={cx("absolute -bottom-0.5 [inset-inline-end:-2px] size-3 rounded-full border-2 border-[#0b1226]", online ? "bg-online glow-online" : "bg-ink3")} />
      )}
    </span>
  );
}

export function Badge({ children, color = "var(--color-ink2)", className, icon }: { children: ReactNode; color?: string; className?: string; icon?: ReactNode }) {
  return (
    <span
      className={cx("inline-flex items-center gap-1.5 h-6 px-2.5 rounded-full text-[12px] font-medium whitespace-nowrap border", className)}
      style={{ color, background: `color-mix(in oklab, ${color} 12%, transparent)`, borderColor: `color-mix(in oklab, ${color} 30%, transparent)` }}
    >
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
      <Loader2 className="size-7 animate-spin text-cdn" />
      {label && <span className="text-sm">{label}</span>}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cx("shimmer rounded-xl", className)} />;
}

export function Empty({ icon, title, hint, action }: { icon: ReactNode; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center text-center py-16 px-6">
      <div className="relative size-16 rounded-2xl grid place-items-center text-[#a9c8ff] mb-5 bg-cdn/10 border border-cdn/25 shadow-[0_0_40px_-10px_rgba(76,141,255,.6)]">{icon}</div>
      <div className="text-[17px] font-semibold">{title}</div>
      {hint && <p className="text-ink2 text-sm mt-1.5 max-w-sm">{hint}</p>}
      {action && <div className="mt-6">{action}</div>}
    </div>
  );
}

/** Glass card with an optional header (icon, title, subtitle, action). */
export function Panel({ title, sub, icon, action, children, className, pad = true }: { title?: ReactNode; sub?: ReactNode; icon?: ReactNode; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean }) {
  return (
    <section className={cx("glass edge rounded-[20px]", className)}>
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 px-5 pt-4.5 pb-1">
          <div className="flex items-center gap-3 min-w-0">
            {icon && <span className="grid place-items-center size-8 rounded-lg bg-white/[.05] border border-white/10 text-ink2 shrink-0">{icon}</span>}
            <div className="min-w-0">
              <h2 className="text-[15px] font-semibold truncate">{title}</h2>
              {sub && <div className="text-[12.5px] text-ink3 truncate">{sub}</div>}
            </div>
          </div>
          {action}
        </header>
      )}
      <div className={pad ? "p-5 pt-3.5" : ""}>{children}</div>
    </section>
  );
}

/** Headline number with an icon chip, a label and room for a chart. */
export function StatCard({ icon, label, value, sub, accent = "#4c8dff", children, className }: { icon: ReactNode; label: string; value: ReactNode; sub?: ReactNode; accent?: string; children?: ReactNode; className?: string }) {
  return (
    <div className={cx("glass edge rounded-[20px] p-4 min-w-0 flex flex-col", className)}>
      <div className="flex items-center gap-2.5">
        <span
          className="grid place-items-center size-9 rounded-xl shrink-0"
          style={{ color: accent, background: `color-mix(in oklab, ${accent} 14%, transparent)`, boxShadow: `inset 0 0 0 1px color-mix(in oklab, ${accent} 30%, transparent)` }}
        >
          {icon}
        </span>
        <span className="text-[12.5px] text-ink2 font-medium truncate">{label}</span>
      </div>
      <div className="mt-3 text-[24px] leading-none font-bold num tracking-tight">{value}</div>
      {sub && <div className="mt-2 text-[12px] text-ink3 num">{sub}</div>}
      {children && <div className="mt-auto pt-3">{children}</div>}
    </div>
  );
}

/** Label/value pair for detail grids. */
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

// ---------------------------------------------------------------- sheet (side panel)

export function Sheet({ open, onClose, title, sub, children, footer, wide }: { open: boolean; onClose: () => void; title: ReactNode; sub?: ReactNode; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const { lang, t } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    ref.current?.focus();
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [open, onClose]);
  if (!open) return null;
  // portal: animated page containers would otherwise trap position:fixed
  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="fade absolute inset-0 bg-[rgba(3,6,14,.66)] backdrop-blur-[2px]" onClick={onClose} />
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        style={{ ["--sheet-from" as any]: lang === "fa" ? "-48px" : "48px" }}
        className={cx(
          "sheet glass-strong absolute top-0 bottom-0 [inset-inline-end:0] w-full flex flex-col outline-none focus-visible:outline-none shadow-[0_0_80px_rgba(0,0,0,.6)] border-y-0 border-e-0",
          wide ? "sm:max-w-[680px]" : "sm:max-w-[500px]",
        )}
      >
        <header className="flex items-center justify-between gap-3 px-6 h-[68px] border-b border-white/[.07] shrink-0">
          <div className="min-w-0">
            <h2 className="text-[16px] font-semibold truncate">{title}</h2>
            {sub && <div className="text-[12.5px] text-ink3 truncate">{sub}</div>}
          </div>
          <IconButton label={t("cancel")} onClick={onClose}>
            <X className="size-5" />
          </IconButton>
        </header>
        <div className="flex-1 overflow-y-auto px-6 py-6">{children}</div>
        {footer && <footer className="px-6 py-4 border-t border-white/[.07] flex items-center justify-end gap-2 shrink-0 bg-black/10">{footer}</footer>}
      </div>
    </div>,
    document.body,
  );
}

// ---------------------------------------------------------------- toasts

type ToastFn = (msg: string, kind?: "ok" | "error") => void;
const ToastCtx = createContext<ToastFn>(() => {});
export const useToast = () => useContext(ToastCtx);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<{ id: number; msg: string; kind: "ok" | "error" }[]>([]);
  const push = useCallback<ToastFn>((msg, kind = "ok") => {
    const id = Date.now() + Math.random();
    setItems((x) => [...x.slice(-3), { id, msg, kind }]);
    setTimeout(() => setItems((x) => x.filter((i) => i.id !== id)), kind === "error" ? 6000 : 2600);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="fixed z-[60] bottom-24 sm:bottom-6 inset-x-0 flex flex-col items-center gap-2 pointer-events-none px-4" aria-live="polite">
        {items.map((i) => (
          <div
            key={i.id}
            role="status"
            className={cx(
              "rise pointer-events-auto max-w-md rounded-2xl px-4 py-3 text-sm shadow-[0_18px_40px_-14px_rgba(0,0,0,.8)] border backdrop-blur-xl flex items-center gap-2.5",
              i.kind === "error" ? "bg-[#2a1220]/95 border-bad/45 text-[#ffd5dc]" : "bg-[#121c38]/95 border-white/12 text-ink",
            )}
          >
            {i.kind === "error" ? <X className="size-4 text-bad shrink-0" /> : <Check className="size-4 text-online shrink-0" />}
            {i.msg}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
