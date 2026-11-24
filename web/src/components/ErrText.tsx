import { useState } from "react";
import { AlertTriangle } from "lucide-react";
import { readable, type ErrAbout } from "../lib/errors";
import { useI18n } from "../lib/i18n";
import { cx } from "../lib/format";

/** A raw server error as a sentence with its fix; the original stays one click away. */
export function ErrText({ raw, className, compact, about }: { raw: string; className?: string; compact?: boolean; about?: ErrAbout }) {
  const { lang, t } = useI18n();
  const [open, setOpen] = useState(false);
  const r = readable(raw, lang, about);
  return (
    <div className={cx("text-[13px] leading-6", className)}>
      {r.known ? <span>{r.text}{r.fix && <span className="text-ink2">. {r.fix}</span>}</span> : <bdi dir="ltr" className="mono text-[12px] [overflow-wrap:anywhere]">{r.raw}</bdi>}
      {r.known && !compact && (
        <button type="button" className="link-btn ms-2 text-[12px]" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? t("errHide") : t("errDetails")}</button>
      )}
      {open && <bdi dir="ltr" className="block mt-1 mono text-[11.5px] text-ink3 [overflow-wrap:anywhere]">{r.raw}</bdi>}
    </div>
  );
}

/** The core errors of a server as a list of sentences, one per core. */
export function CoreErrors({ errors, className }: { errors: [string, string][]; className?: string }) {
  if (!errors.length) return null;
  return (
    <div className={cx("mach rounded-2xl border-bad/40 px-4 py-3 flex gap-3", className)} role="alert">
      <span className="led rd mt-2" />
      <div className="flex-1 min-w-0 flex flex-col gap-1.5">
        {errors.map(([core, msg]) => <ErrText key={core} raw={msg.includes(core) ? msg : `${core}: ${msg}`} />)}
      </div>
      <AlertTriangle className="size-4 text-bad shrink-0 mt-1" />
    </div>
  );
}
