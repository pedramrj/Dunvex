import { useState } from "react";
import { Plus, X } from "lucide-react";
import { cx } from "../lib/format";
import { useI18n } from "../lib/i18n";

// Tags: the owner's own labels on users ("vip", "family", "trial"…). The
// rules match the server's (store/tags.go): spaces collapsed, commas (Latin
// or Persian) split tags, at most MAX_TAG_LEN characters and MAX_TAGS a user.
export const MAX_TAG_LEN = 24;
export const MAX_TAGS = 12;

export function normalizeTag(t: string): string {
  const s = t.replace(/[,،]/g, " ").split(/\s+/).filter(Boolean).join(" ");
  return [...s].length > MAX_TAG_LEN ? [...s].slice(0, MAX_TAG_LEN).join("").trim() : s;
}

export const sameTag = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();
export const hasTag = (tags: string[] | undefined, tag: string) => (tags ?? []).some((x) => sameTag(x, tag));

// the theme's own accents, so a tag reads well in every theme
const palette = [
  "var(--primary2)",
  "var(--cyan)",
  "var(--color-violet)",
  "var(--color-udp)",
  "var(--color-classic)",
  "var(--color-reality)",
  "color-mix(in oklab, var(--color-violet) 55%, var(--color-reality))",
  "color-mix(in oklab, var(--cyan) 50%, var(--color-classic))",
];

/** The same tag always gets the same colour, whatever its case. */
export function tagColor(tag: string): string {
  let h = 0;
  const s = tag.toLowerCase();
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return palette[h % palette.length];
}

export function TagChip({ tag, onRemove, onClick, active, count, size = "md", title }: {
  tag: string; onRemove?: () => void; onClick?: () => void; active?: boolean; count?: number; size?: "sm" | "md"; title?: string;
}) {
  const { t, n } = useI18n();
  const color = tagColor(tag);
  const cls = cx(
    "inline-flex items-center gap-1 rounded-full border font-medium whitespace-nowrap max-w-full transition-[background-color,box-shadow] duration-150",
    size === "sm" ? "h-5 px-2 text-[11px]" : "h-7 px-2.5 text-[12.5px]",
    onClick && "cursor-pointer hover:brightness-110",
  );
  const style = {
    color,
    background: `color-mix(in oklab, ${color} ${active ? 26 : 11}%, transparent)`,
    borderColor: `color-mix(in oklab, ${color} ${active ? 70 : 30}%, transparent)`,
    boxShadow: active ? `0 0 0 3px color-mix(in oklab, ${color} 14%, transparent)` : undefined,
  };
  const body = (
    <>
      <span className="size-1.5 rounded-full shrink-0" style={{ background: color }} aria-hidden />
      <bdi className="truncate">{tag}</bdi>
      {count !== undefined && <span className="num opacity-70">{n(count)}</span>}
    </>
  );
  if (onClick)
    return <button type="button" onClick={onClick} aria-pressed={active} title={title} className={cls} style={style}>{body}</button>;
  return (
    <span className={cls} style={style} title={title}>
      {body}
      {onRemove && (
        <button type="button" onClick={onRemove} aria-label={`${t("tagRemove")} ${tag}`}
          className="-me-1 grid place-items-center size-5 rounded-full hover:bg-fg/10"><X className="size-3" /></button>
      )}
    </span>
  );
}

/** A user's tags in a row: the first `max`, then "+N". */
export function TagList({ tags, max = 3, onPick, active }: { tags: string[]; max?: number; onPick?: (tag: string) => void; active?: string[] }) {
  const { t, n } = useI18n();
  if (!tags.length) return null;
  const shown = tags.slice(0, max), more = tags.length - shown.length;
  return (
    <span className="flex flex-wrap items-center gap-1 min-w-0">
      {shown.map((tag) => (
        <TagChip key={tag} tag={tag} size="sm" active={hasTag(active, tag)}
          onClick={onPick ? () => onPick(tag) : undefined} title={onPick ? t("tagFilterBy") : undefined} />
      ))}
      {more > 0 && <span className="text-[11px] text-ink3 num" title={tags.slice(max).join(", ")}>+{n(more)}</span>}
    </span>
  );
}

/** Edits a list of tags: chips, a field (Enter or a comma adds) and the tags already in use as suggestions. */
export function TagInput({ value, onChange, suggestions, autoFocus }: { value: string[]; onChange: (v: string[]) => void; suggestions: string[]; autoFocus?: boolean }) {
  const { t, n } = useI18n();
  const [draft, setDraft] = useState("");
  const full = value.length >= MAX_TAGS;
  const add = (raw: string) => {
    const next = [...value];
    for (const part of raw.split(/[,،]/)) {
      const tag = normalizeTag(part);
      if (tag && !hasTag(next, tag) && next.length < MAX_TAGS) next.push(tag);
    }
    if (next.length !== value.length) onChange(next);
    setDraft("");
  };
  const q = draft.trim().toLowerCase();
  const sugg = suggestions.filter((s) => !hasTag(value, s) && (!q || s.toLowerCase().includes(q))).slice(0, 8);
  return (
    <div>
      <div className={cx("flex flex-wrap items-center gap-1.5 min-h-11 rounded-xl bg-rail border border-hair px-2 py-1.5 transition-[border-color,box-shadow] duration-150",
        "focus-within:border-primary2/70 focus-within:shadow-[0_0_0_4px_color-mix(in_oklab,var(--primary)_16%,transparent)]")}>
        {value.map((tag) => <TagChip key={tag} tag={tag} onRemove={() => onChange(value.filter((x) => x !== tag))} />)}
        {!full && (
          <input value={draft} maxLength={MAX_TAG_LEN + 1} dir="auto" spellCheck={false} autoFocus={autoFocus} aria-label={t("tags")}
            placeholder={value.length ? t("tagAddMore") : t("tagPlaceholder")}
            onChange={(e) => (/[,،]/.test(e.target.value) ? add(e.target.value) : setDraft(e.target.value))}
            onKeyDown={(e) => {
              if (e.key === "Enter") { e.preventDefault(); if (draft.trim()) add(draft); }
              else if (e.key === "Backspace" && !draft && value.length) onChange(value.slice(0, -1));
            }}
            onBlur={() => draft.trim() && add(draft)}
            className="flex-1 min-w-28 h-8 px-1.5 bg-transparent outline-none text-[14px] text-ink placeholder:text-ink3/80" />
        )}
      </div>
      {!full && sugg.length > 0 && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span className="text-[12px] text-ink3 me-0.5">{t("tagInUse")}</span>
          {sugg.map((s) => (
            <button key={s} type="button" onClick={() => add(s)}
              className="inline-flex items-center gap-1 h-7 px-2.5 rounded-full border border-dashed border-hair2 text-[12.5px] text-ink2 hover:text-ink hover:bg-fg/[.05]">
              <Plus className="size-3" /><bdi>{s}</bdi>
            </button>
          ))}
        </div>
      )}
      <p className="mt-2 text-[12px] text-ink3">{full ? t("tagFull").replace("{n}", n(MAX_TAGS)) : t("tagHint").replace("{n}", n(MAX_TAGS))}</p>
    </div>
  );
}
