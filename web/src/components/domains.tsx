// The Domains page's visual pieces, on ui.tsx parts and theme tokens only:
// a redesign restyles the page here. The page (pages/Domains.tsx) keeps the
// layout and the logic.

import type { DragEvent, KeyboardEvent, ReactNode } from "react";
import { CircleCheck, CircleDashed, CircleX, GripVertical, ShieldCheck, ShieldOff, ShieldQuestion, TriangleAlert } from "lucide-react";
import { cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Badge, Dot } from "./ui";

export type Role = "direct" | "cdn" | "sub";
export type Tone = "ok" | "warn" | "bad" | "idle" | "busy";

/** The colour of a tone, from the theme. */
export const toneColor = (t: Tone) =>
  t === "ok" ? "var(--ok)" : t === "warn" ? "var(--warn)" : t === "bad" ? "var(--bad)" : t === "busy" ? "var(--primary2)" : "var(--text3)";

/** The colour of a role, from the theme (CDN keeps its protocol-family colour). */
export const roleColor = (r: Role) => (r === "cdn" ? "var(--color-cdn)" : r === "sub" ? "var(--cyan)" : "var(--text2)");

export function RoleBadge({ role }: { role: Role }) {
  const { t } = useI18n();
  return <Badge color={roleColor(role)}>{t(("dmRole_" + role) as any)}</Badge>;
}

export function MainBadge() {
  const { t } = useI18n();
  return <Badge color="var(--primary2)">{t("dmMain")}</Badge>;
}

/** A state in one word, with its dot (the word, not the colour, carries the meaning). */
export function StateWord({ tone, children, className }: { tone: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={cx("inline-flex items-center gap-2 text-[12.5px] min-w-0", tone === "idle" ? "text-ink3" : "text-ink2", className)}>
      <Dot color={toneColor(tone)} size={7} pulse={tone === "busy"} />
      <span className="truncate">{children}</span>
    </span>
  );
}

/** The certificate state of a name, as an icon and a word. */
export function CertWord({ tone, children }: { tone: Tone; children: ReactNode }) {
  const Icon = tone === "ok" ? ShieldCheck : tone === "bad" ? ShieldOff : ShieldQuestion;
  return (
    <span className={cx("inline-flex items-center gap-1.5 text-[12.5px] min-w-0", tone === "idle" ? "text-ink3" : "text-ink2")}>
      <Icon className="size-4 shrink-0" style={{ color: toneColor(tone) }} aria-hidden />
      <span className="truncate">{children}</span>
    </span>
  );
}

/** A domain name: technical, so mono and left to right in both languages.
 * With truncate it stays on one line and loses its end (never its start). */
export function DomainName({ name, className, truncate }: { name: string; className?: string; truncate?: boolean }) {
  return <bdi dir="ltr" title={truncate ? name : undefined} className={cx("mono", truncate ? "block truncate text-start" : "break-all", className)}>{name}</bdi>;
}

/** One row of the ordered list of names. It is dragged by its handle; the
 * keyboard moves it with Alt+↑ / Alt+↓ (and the row's menu has the same). */
export function DomainRow({ name, label, badges, dns, cert, uses, menu, onOpen, dragging, over, onMove, drag }: {
  name: string; label?: string; badges: ReactNode; dns: ReactNode; cert: ReactNode; uses?: ReactNode; menu: ReactNode;
  onOpen: () => void; dragging?: boolean; over?: boolean; onMove?: (d: -1 | 1) => void;
  drag?: { onDragStart: (e: DragEvent) => void; onDragOver: (e: DragEvent) => void; onDrop: (e: DragEvent) => void; onDragEnd: () => void };
}) {
  const { t } = useI18n();
  const key = (e: KeyboardEvent) => {
    if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown") && onMove) {
      e.preventDefault();
      onMove(e.key === "ArrowUp" ? -1 : 1);
    }
  };
  return (
    <li onDragOver={drag?.onDragOver} onDrop={drag?.onDrop}
      className={cx("group relative flex items-stretch gap-1 rounded-2xl border transition-[background-color,border-color,opacity] duration-150",
        over ? "border-primary2/60 bg-primary/[.06]" : "border-hair hover:border-hair2 hover:bg-fg/[.025]", dragging && "opacity-45")}>
      {drag && (
        <span draggable onDragStart={drag.onDragStart} onDragEnd={drag.onDragEnd} title={t("dmDrag")} aria-hidden
          className="hidden sm:grid place-items-center w-7 shrink-0 cursor-grab active:cursor-grabbing text-ink3 opacity-60 group-hover:opacity-100">
          <GripVertical className="size-4" />
        </span>
      )}
      <button type="button" onClick={onOpen} onKeyDown={key}
        className="flex-1 min-w-0 text-start px-3 sm:ps-1 py-3 grid gap-x-5 gap-y-2 sm:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)] items-center rounded-2xl outline-none focus-visible:shadow-[0_0_0_3px_color-mix(in_oklab,var(--primary)_35%,transparent)]">
        <span className="min-w-0 flex flex-col gap-1">
          <span className="flex flex-wrap items-center gap-2 min-w-0">
            <DomainName name={name} className="text-[14px] font-medium text-ink" />
            {badges}
          </span>
          {(label || uses) && (
            <span className="flex flex-wrap items-center gap-1.5 text-[12px] text-ink3 min-w-0">
              {label && <span className="truncate">{label}</span>}
              {uses}
            </span>
          )}
        </span>
        <span className="min-w-0">{dns}</span>
        <span className="min-w-0">{cert}</span>
      </button>
      <span className="flex items-center pe-1.5 shrink-0">{menu}</span>
    </li>
  );
}

/** One DNS record of a name: its type, its value, and whose address it is. */
export function RecordLine({ type, value, mark, tone }: { type: string; value: string; mark?: string; tone?: Tone }) {
  return (
    <div className="flex items-center gap-3 py-1.5 min-w-0">
      <span className="mono text-[11px] text-ink3 w-12 shrink-0">{type}</span>
      <bdi dir="ltr" className="mono text-[12.5px] text-ink flex-1 min-w-0 break-all">{value}</bdi>
      {mark && <StateWord tone={tone ?? "idle"} className="shrink-0">{mark}</StateWord>}
    </div>
  );
}

/** Something that needs the owner, with what to do about it. */
export function Issue({ tone, title, detail, action }: { tone: Tone; title: ReactNode; detail?: ReactNode; action?: ReactNode }) {
  const Icon = tone === "bad" ? CircleX : TriangleAlert;
  return (
    <li className="flex flex-wrap sm:flex-nowrap items-start gap-3 py-3 first:pt-0 last:pb-0 border-b border-hair last:border-b-0">
      <Icon className="size-[18px] shrink-0 mt-0.5" style={{ color: toneColor(tone) }} aria-hidden />
      <div className="flex-1 min-w-0">
        <div className="text-[13.5px] text-ink leading-6">{title}</div>
        {detail && <div className="text-[12.5px] text-ink3 leading-6 mt-0.5">{detail}</div>}
      </div>
      {action && <div className="shrink-0 ms-auto">{action}</div>}
    </li>
  );
}

/** A name and whether the certificate covers it. */
export function CoverLine({ name, state, note }: { name: string; state: "in" | "missing" | "failed"; note?: string }) {
  const Icon = state === "in" ? CircleCheck : state === "failed" ? CircleX : CircleDashed;
  const tone: Tone = state === "in" ? "ok" : state === "failed" ? "bad" : "idle";
  return (
    <div className="flex items-start gap-2.5 py-1 min-w-0">
      <Icon className="size-4 shrink-0 mt-[3px]" style={{ color: toneColor(tone) }} aria-hidden />
      <div className="min-w-0">
        <DomainName name={name} className="text-[12.5px] text-ink" />
        {note && <div className="text-[12px] text-ink3 leading-5">{note}</div>}
      </div>
    </div>
  );
}

/** A fact of the status strip: a small label and its value. */
export function Fact({ label, children, tone }: { label: ReactNode; children: ReactNode; tone?: Tone }) {
  return (
    <div className="min-w-0 flex flex-col gap-1 py-1">
      <span className="text-[11.5px] text-ink3">{label}</span>
      <span className="text-[13.5px] font-medium text-ink flex items-center gap-2 min-w-0">
        {tone && <Dot color={toneColor(tone)} size={7} pulse={tone === "busy"} />}
        <span className="truncate">{children}</span>
      </span>
    </div>
  );
}

/** A group of the detail sheet: a title line and its content, divided by hairlines (no card in a card). */
export function SheetSection({ title, action, children }: { title: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="py-5 first:pt-0 border-b border-hair last:border-b-0">
      <div className="flex items-center justify-between gap-3 mb-3">
        <h3 className="text-[13.5px] font-semibold text-ink">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}
