import type { ReactNode } from "react";
import { BadgeCheck, Search, Terminal, Zap } from "lucide-react";
import { cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Input } from "./ui";

// The inbox kit: every visual piece of the Support page lives here, on the
// shared tokens and ui.tsx parts, so a panel-wide restyle changes the inbox
// in one place. The page itself only arranges them and holds the logic.

/** A search box with its icon (an Input from the kit). */
export function SearchField({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <label className="relative block">
      <Search className="size-4 text-ink3 absolute top-1/2 -translate-y-1/2 [inset-inline-start:12px] pointer-events-none" />
      <Input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={placeholder} dir="auto" className="!h-10 !ps-9 text-[13px]" />
    </label>
  );
}

/** Filters that each carry a count: the number above, the name under it. */
export function CountTabs<T extends string>({ value, onChange, items, label }: {
  value: T; onChange: (v: T) => void; label: string; items: { id: T; label: string; count: number; alert?: boolean }[];
}) {
  const { n } = useI18n();
  return (
    <div role="tablist" aria-label={label} className="grid gap-1 p-1 rounded-xl bg-fg/[.035] border border-hair" style={{ gridTemplateColumns: `repeat(${items.length}, minmax(0, 1fr))` }}>
      {items.map((x) => {
        const on = value === x.id;
        return (
          <button key={x.id} role="tab" aria-selected={on} onClick={() => onChange(x.id)}
            className={cx("h-11 rounded-lg flex flex-col items-center justify-center leading-tight transition-[background-color,color] duration-150",
              on ? "bg-fg/[.09] text-ink" : "text-ink3 hover:text-ink2 hover:bg-fg/[.04]")}>
            <span className={cx("num text-[14px]", on ? "font-bold" : "font-semibold", x.alert && x.count > 0 && "text-warn")}>{n(x.count)}</span>
            <span className="text-[11px] truncate max-w-full px-1">{x.label}</span>
          </button>
        );
      })}
    </div>
  );
}

/** How many messages wait unread. */
export function UnreadCount({ count }: { count: number }) {
  const { n } = useI18n();
  return <span className="shrink-0 num text-[11px] font-semibold rounded-full min-w-5 h-5 px-1.5 grid place-items-center bg-primary text-white">{n(count)}</span>;
}

/** One conversation in the list. */
export function InboxRow({ href, active, avatar, title, tag, time, preview, previewDir, unread, trailing }: {
  href: string; active: boolean; avatar: ReactNode; title: ReactNode; tag?: ReactNode; time: string;
  preview: ReactNode; previewDir: "rtl" | "ltr"; unread: boolean; trailing?: ReactNode;
}) {
  return (
    <a href={href} aria-current={active ? "true" : undefined}
      className={cx("relative flex gap-3 rounded-2xl px-3 py-2.5 transition-[background-color,border-color] duration-150 border",
        active ? "bg-primary/[.13] border-primary/30" : "border-transparent hover:bg-fg/[.045]")}>
      {avatar}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <bdi className={cx("truncate text-[13.5px] text-ink", unread ? "font-bold" : "font-semibold")}>{title}</bdi>
          {tag}
          <span className={cx("ms-auto shrink-0 num text-[11.5px]", unread ? "text-primary2 font-semibold" : "text-ink3")}>{time}</span>
        </span>
        <span className="mt-0.5 flex items-center gap-2">
          <span dir={previewDir} className={cx("truncate text-[12.5px] flex-1 text-start", unread ? "text-ink2" : "text-ink3")}>{preview}</span>
          {trailing}
        </span>
      </span>
    </a>
  );
}

/** A small outlined label inside a row ("No subscription"). */
export function RowTag({ children }: { children: ReactNode }) {
  return <span className="shrink-0 text-[10.5px] text-ink3 border border-hair rounded-full px-1.5 leading-4">{children}</span>;
}

/** The date between two days of messages. */
export function DaySeparator({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 my-4 first:mt-0" role="separator">
      <span className="h-px flex-1 bg-hair" />
      <span className="text-[11.5px] text-ink3 num">{label}</span>
      <span className="h-px flex-1 bg-hair" />
    </div>
  );
}

/** One message: "in" from the person (start side), "out" from the team (end side). */
export function MessageBubble({ side, grouped, media, text, meta }: { side: "in" | "out"; grouped: boolean; media?: ReactNode; text?: string; meta: ReactNode }) {
  const out = side === "out";
  return (
    <div className={cx("flex", out ? "justify-end" : "justify-start", grouped ? "mt-1" : "mt-3")}>
      <div className={cx("max-w-[min(560px,85%)] flex flex-col", out ? "items-end" : "items-start")}>
        <div className={cx("rounded-2xl border overflow-hidden", out ? "bg-primary/[.14] border-primary/30 rounded-ee-md" : "bg-panel2 border-hair rounded-ss-md")}>
          {media}
          {text && <p dir="auto" className="px-3.5 py-2 text-[14px] leading-[1.75] whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</p>}
        </div>
        <div className="mt-1 px-1 flex items-center gap-1.5 text-[11px] text-ink3">{meta}</div>
      </div>
    </div>
  );
}

/** A picture in a message, opening full size. */
export function MessagePhoto({ src, alt }: { src: string; alt: string }) {
  return (
    <a href={src} target="_blank" rel="noreferrer" className="block">
      <img src={src} alt={alt} className="block max-h-72 min-h-24 min-w-40 w-auto max-w-full object-contain bg-fg/[.04]" />
    </a>
  );
}

/** Where a person's message stands; the same three steps the bot shows as reactions (⚡ 👨‍💻 💯). */
export function Receipt({ state, label }: { state: "arrived" | "seen" | "answered"; label: string }) {
  const icon = state === "answered" ? <BadgeCheck className="size-3.5 text-online" />
    : state === "seen" ? <Terminal className="size-3.5 text-primary2" /> : <Zap className="size-3.5" />;
  return <span className="inline-flex items-center gap-1" title={label}>{icon}<span className="sr-only">{label}</span></span>;
}

/** A label and its value on one line, for the details beside a thread. */
export function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-2 border-b border-hair last:border-0 text-[12.5px]">
      <span className="text-ink3 shrink-0">{label}</span>
      <span className="text-ink text-end min-w-0 truncate">{children}</span>
    </div>
  );
}

/** A titled block in the details column. */
export function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="text-[12.5px] font-semibold text-ink2 mb-2">{title}</h3>
      {children}
    </section>
  );
}
