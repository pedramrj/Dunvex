import { createContext, useCallback, useContext, useEffect, useRef, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Loader2, X } from "lucide-react";
import { copyText, cx } from "../lib/format";
import { useI18n } from "../lib/i18n";

// ---------------------------------------------------------------- buttons

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "ghost" | "quiet" | "danger";
  size?: "sm" | "md";
  loading?: boolean;
  icon?: ReactNode;
};

export function Button({ variant = "ghost", size = "md", loading, icon, className, children, disabled, ...rest }: BtnProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cx(
        "inline-flex items-center justify-center gap-2 rounded-xl font-medium whitespace-nowrap select-none transition-[background,color,box-shadow,transform] duration-150 active:scale-[.98] disabled:opacity-45 disabled:pointer-events-none",
        size === "sm" ? "h-8 px-3 text-[13px]" : "h-10 px-4 text-sm",
        variant === "primary" && "bg-ink text-ground hover:bg-white shadow-[0_6px_18px_-8px_rgba(238,241,246,.55)]",
        variant === "ghost" && "bg-panel2 text-ink border border-hair2 hover:border-ink3 hover:bg-[#1a2a4d]",
        variant === "quiet" && "text-ink2 hover:text-ink hover:bg-panel2",
        variant === "danger" && "bg-[rgba(255,93,115,.12)] text-bad border border-[rgba(255,93,115,.35)] hover:bg-[rgba(255,93,115,.2)]",
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ label, className, children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      {...rest}
      aria-label={label}
      title={label}
      className={cx("inline-grid place-items-center size-8 rounded-lg text-ink2 hover:text-ink hover:bg-panel2 transition-colors disabled:opacity-40", className)}
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
    <span className="flex items-baseline justify-between gap-2 mb-1.5 text-[13px] text-ink2">
      <span>{children}</span>
      {hint && <span className="text-[12px] text-ink3">{hint}</span>}
    </span>
  );
}

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...rest}
      className={cx(
        "w-full h-10 rounded-xl bg-ground border border-hair2 px-3 text-ink placeholder:text-ink3 outline-none transition-colors focus:border-ink2 focus-visible:outline-none disabled:opacity-50",
        className,
      )}
    />
  );
}

export function Select({ value, onChange, options, className }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; className?: string }) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={cx("w-full h-10 rounded-xl bg-ground border border-hair2 px-3 text-ink outline-none focus:border-ink2", className)}
    >
      {options.map((o) => (
        <option key={o.value} value={o.value} className="bg-panel">
          {o.label}
        </option>
      ))}
    </select>
  );
}

export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cx("inline-flex items-center gap-3 cursor-pointer select-none", disabled && "opacity-50 pointer-events-none")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cx("relative h-6 w-10 rounded-full transition-colors shrink-0", checked ? "bg-online" : "bg-hair2")}
      >
        <span
          className={cx(
            "absolute top-0.5 size-5 rounded-full bg-ink shadow transition-[inset-inline-start] duration-200",
            checked ? "[inset-inline-start:18px]" : "[inset-inline-start:2px]",
          )}
        />
      </button>
      {label && <span className="text-sm text-ink">{label}</span>}
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
            "h-8 px-3 rounded-lg text-[13px] border transition-colors num",
            o.value === value ? "bg-ink text-ground border-ink" : "bg-transparent text-ink2 border-hair2 hover:text-ink hover:border-ink3",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function CopyField({ value, className }: { value: string; className?: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const [done, setDone] = useState(false);
  return (
    <div className={cx("flex items-center gap-2 rounded-xl border border-hair2 bg-ground ps-3 pe-1 h-10", className)}>
      <bdi dir="ltr" className="flex-1 min-w-0 truncate text-[12px] text-ink2 font-mono select-all">
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

export function Meter({ value, max, color = "var(--color-ink)", className, plain }: { value: number; max: number; color?: string; className?: string; plain?: boolean }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const c = plain ? color : pct >= 90 ? "var(--color-bad)" : pct >= 75 ? "var(--color-warn)" : color;
  return (
    <div className={cx("h-1.5 rounded-full bg-hair overflow-hidden", className)}>
      <div className="h-full rounded-full transition-[width] duration-500" style={{ width: `${pct}%`, background: c }} />
    </div>
  );
}

export function Dot({ color, pulse }: { color: string; pulse?: boolean }) {
  return <span className={cx("relative inline-block size-2 rounded-full shrink-0", pulse && "pulse")} style={{ background: color, color }} />;
}

export function Spinner() {
  return (
    <div className="grid place-items-center py-16 text-ink3">
      <Loader2 className="size-6 animate-spin" />
    </div>
  );
}

export function Empty({ icon, title, hint, action }: { icon: ReactNode; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center text-center py-16 px-6">
      <div className="size-14 rounded-2xl border border-hair2 grid place-items-center text-ink2 mb-4">{icon}</div>
      <div className="text-base font-semibold">{title}</div>
      {hint && <p className="text-ink2 text-sm mt-1 max-w-sm">{hint}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

export function Panel({ title, action, children, className, pad = true }: { title?: ReactNode; action?: ReactNode; children: ReactNode; className?: string; pad?: boolean }) {
  return (
    <section className={cx("rounded-2xl bg-panel border border-hair", className)}>
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 px-5 pt-4 pb-1">
          <h2 className="text-[15px] font-semibold">{title}</h2>
          {action}
        </header>
      )}
      <div className={pad ? "p-5 pt-3" : ""}>{children}</div>
    </section>
  );
}

// ---------------------------------------------------------------- sheet (side panel)

export function Sheet({ open, onClose, title, children, footer, wide }: { open: boolean; onClose: () => void; title: ReactNode; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const { lang, t } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    ref.current?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  // portal: animated page containers would otherwise trap position:fixed
  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-[rgba(5,9,20,.62)]" onClick={onClose} />
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        style={{ ["--sheet-from" as any]: lang === "fa" ? "-40px" : "40px" }}
        className={cx(
          "sheet absolute top-0 bottom-0 [inset-inline-end:0] w-full bg-rail border-s border-hair flex flex-col outline-none focus-visible:outline-none shadow-[0_0_60px_rgba(0,0,0,.5)]",
          wide ? "sm:max-w-[640px]" : "sm:max-w-[480px]",
        )}
      >
        <header className="flex items-center justify-between gap-3 px-5 h-16 border-b border-hair shrink-0">
          <h2 className="text-base font-semibold truncate">{title}</h2>
          <IconButton label={t("cancel")} onClick={onClose}>
            <X className="size-5" />
          </IconButton>
        </header>
        <div className="flex-1 overflow-y-auto px-5 py-5">{children}</div>
        {footer && <footer className="px-5 py-4 border-t border-hair flex items-center justify-end gap-2 shrink-0">{footer}</footer>}
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
      <div className="fixed z-[60] bottom-20 sm:bottom-6 inset-x-0 flex flex-col items-center gap-2 pointer-events-none px-4">
        {items.map((i) => (
          <div
            key={i.id}
            role="status"
            className={cx(
              "rise pointer-events-auto max-w-md rounded-xl px-4 py-2.5 text-sm shadow-[0_10px_30px_-10px_rgba(0,0,0,.6)] border",
              i.kind === "error" ? "bg-[#2a1220] border-[rgba(255,93,115,.45)] text-[#ffd5dc]" : "bg-panel2 border-hair2 text-ink",
            )}
          >
            {i.msg}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
