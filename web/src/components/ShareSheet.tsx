// What opens right after a user is made: the link, its QR and a message ready
// to send. Making the user ends in what the owner sends to the customer.
import { Check, Copy, ExternalLink, UserPlus } from "lucide-react";
import { qrURL, type User } from "../lib/api";
import { bytes, copyText } from "../lib/format";
import { useI18n } from "../lib/i18n";
import { Button, CopyField, Sheet, useToast } from "./ui";

function message(u: User, t: ReturnType<typeof useI18n>["t"], lang: "fa" | "en", n: (v: number) => string) {
  const vol = u.dataLimit > 0 ? bytes(u.dataLimit, lang) : t("unlimited");
  const time = u.durationDays > 0 ? `${n(u.durationDays)} ${t("days")}${u.expireAt === 0 ? " " + t("msgFromFirst") : ""}` : t("never");
  return [t("msgReady"), `${t("name")}: ${u.name}`, `${t("msgVolume")}: ${vol} · ${t("msgTime")}: ${time}`, t("shareLink") + ":", u.subUrl, t("msgGuide")].join("\n");
}

export function ShareSheet({ users, onClose, onOpen, onNext }: { users: User[] | null; onClose: () => void; onOpen: (id: number) => void; onNext: () => void }) {
  const { t, lang, n } = useI18n();
  const toast = useToast();
  if (!users || !users.length) return null;
  const one = users.length === 1 ? users[0] : null;
  const copy = (s: string) => copyText(s).then(() => toast(t("copied")));
  return (
    <Sheet open onClose={onClose} wide
      title={<span className="inline-flex items-center gap-3"><span className="size-8 rounded-full grid place-items-center bg-online/15 text-online"><Check className="size-4" /></span>{one ? <span><bdi dir="ltr">{one.name}</bdi> {t("shareReady")}</span> : t("shareMany").replace("{n}", n(users.length))}</span>}
      footer={<div className="flex flex-wrap gap-2 w-full">
        <Button variant="soft" icon={<UserPlus className="size-4" />} onClick={onNext}>{t("nextUser")}</Button>
        {one && <Button variant="quiet" onClick={() => onOpen(one.id)}>{t("openUser")}</Button>}
        <span className="ms-auto" /><Button onClick={onClose}>{t("close")}</Button>
      </div>}>
      {one ? (
        <div className="flex flex-col gap-5">
          <div className="grid sm:grid-cols-[200px_minmax(0,1fr)] gap-5 items-start">
            <div className="rounded-2xl bg-white p-2.5 justify-self-center sensitive"><img src={qrURL(one.subUrl)} alt="QR" className="size-[180px] block" /></div>
            <div className="min-w-0">
              <div className="ss mb-1.5">SUBSCRIPTION</div>
              <CopyField value={one.subUrl} />
              <div className="mt-3 flex flex-wrap gap-2">
                <Button size="sm" variant="primary" icon={<Copy className="size-4" />} onClick={() => copy(one.subUrl)}>{t("copyLink")}</Button>
                <a className="inline-flex" href={one.subUrl} target="_blank" rel="noreferrer"><Button size="sm" icon={<ExternalLink className="size-4" />}>{t("openUserPage")}</Button></a>
              </div>
              <p className="mt-3 text-[13px] text-ink3">{t("shareHint")}</p>
            </div>
          </div>
          <div>
            <div className="flex items-center gap-2 mb-2"><b className="font-semibold">{t("shareMsg")}</b><span className="ms-auto" /><Button size="sm" icon={<Copy className="size-4" />} onClick={() => copy(message(one, t, lang, n))}>{t("copyMsg")}</Button></div>
            <pre className="field-recess whitespace-pre-wrap text-[13.5px] leading-7 px-4 py-3 font-sans sensitive" dir="auto">{message(one, t, lang, n)}</pre>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2"><span className="text-ink2 text-[13.5px]">{t("shareManyHint")}</span><span className="ms-auto" />
            <Button size="sm" variant="primary" icon={<Copy className="size-4" />} onClick={() => copy(users.map((u) => `${u.name}\n${u.subUrl}`).join("\n\n"))}>{t("copyAll")}</Button></div>
          <ul className="mach rounded-2xl divide-y divide-[var(--border)]">
            {users.map((u) => (
              <li key={u.id} className="flex items-center gap-3 px-4 py-2.5">
                <bdi dir="ltr" className="font-semibold w-36 truncate">{u.name}</bdi>
                <CopyField value={u.subUrl} className="flex-1 min-w-0" />
              </li>
            ))}
          </ul>
        </div>
      )}
    </Sheet>
  );
}
