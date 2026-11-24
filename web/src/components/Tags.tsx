import { useState } from "react";
import { Plus } from "lucide-react";
import { cx } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Chip, fieldShellCls } from "./ui";

// Tags: the owner's own labels on users ("vip", "family", "trial"…). The
// rules match the server's (store/tags.go): spaces collapsed, commas (Latin
// or Persian) split tags, at most MAX_TAG_LEN characters and MAX_TAGS a user.
// Everything here is built from the kit's Chip, so the look lives in styles.css.
export const MAX_TAG_LEN = 24;
export const MAX_TAGS = 12;

export function normalizeTag(t: string): string {
  const s = t.replace(/[,،]/g, " ").split(/\s+/).filter(Boolean).join(" ");
  return [...s].length > MAX_TAG_LEN ? [...s].slice(0, MAX_TAG_LEN).join("").trim() : s;
}

export const sameTag = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();
export const hasTag = (tags: string[] | undefined, tag: string) => (tags ?? []).some((x) => sameTag(x, tag));

/** A tag as a code token: "#vip". Clickable when onClick is given, removable with onRemove. */
export function TagChip({ tag, onRemove, onClick, active, count, size = "md", title }: {
  tag: string; onRemove?: () => void; onClick?: () => void; active?: boolean; count?: number; size?: "sm" | "md"; title?: string;
}) {
  const { t } = useI18n();
  return (
    <Chip className="chip-tag" size={size} active={active} count={count} title={title} onClick={onClick}
      onRemove={onRemove} removeLabel={`${t("tagRemove")} ${tag}`}>
      <span className="chip-hash" aria-hidden>#</span>
      <bdi className="truncate">{tag}</bdi>
    </Chip>
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
        <TagChip key={tag} tag={tag} size="sm" active={onPick ? hasTag(active, tag) : undefined}
          onClick={onPick ? () => onPick(tag) : undefined} title={onPick ? t("tagFilterBy") : undefined} />
      ))}
      {more > 0 && <span className="chip-count text-[11px] px-0.5" title={tags.slice(max).join(", ")}>+{n(more)}</span>}
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
      <div className={cx(fieldShellCls, "flex flex-wrap items-center gap-1.5")}>
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
            <Chip key={s} tone="add" className="chip-tag" icon={<Plus />} onClick={() => add(s)}><bdi>{s}</bdi></Chip>
          ))}
        </div>
      )}
      <p className="mt-2 text-[12px] text-ink3">{full ? t("tagFull").replace("{n}", n(MAX_TAGS)) : t("tagHint").replace("{n}", n(MAX_TAGS))}</p>
    </div>
  );
}
