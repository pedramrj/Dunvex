import { useState } from "react";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { Button, Input, Label } from "../components/ui";

export function Login({ onDone }: { onDone: () => void }) {
  const { t, lang, setLang } = useI18n();
  const [username, setUser] = useState("");
  const [password, setPass] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    api("login", { body: { username, password } })
      .then(onDone)
      .catch((e) => setErr(e.status === 401 ? t("wrongLogin") : e.message))
      .finally(() => setBusy(false));
  };

  return (
    <div className="min-h-dvh grid lg:grid-cols-[1.1fr_1fr]">
      {/* the map motif: four lines meeting at one interchange */}
      <div className="relative hidden lg:block overflow-hidden bg-rail border-e border-hair">
        <svg viewBox="0 0 600 800" className="absolute inset-0 w-full h-full" preserveAspectRatio="xMidYMid slice" aria-hidden>
          <path d="M-20 250 H200 L300 350 V800" fill="none" stroke="var(--color-reality)" strokeWidth={14} strokeLinejoin="round" />
          <path d="M620 180 H420 L320 280 V350" fill="none" stroke="var(--color-cdn)" strokeWidth={14} strokeLinejoin="round" />
          <path d="M-20 520 H180 L280 420" fill="none" stroke="var(--color-udp)" strokeWidth={14} strokeLinejoin="round" />
          <path d="M620 560 H440 L340 460 V-20" fill="none" stroke="var(--color-classic)" strokeWidth={14} strokeLinejoin="round" />
          {[[200, 250], [420, 180], [180, 520], [440, 560], [300, 640], [340, 120]].map(([x, y]) => (
            <circle key={`${x}-${y}`} cx={x} cy={y} r={11} fill="var(--color-rail)" stroke="#eef1f6" strokeWidth={5} />
          ))}
          <rect x={250} y={360} width={100} height={100} rx={50} fill="var(--color-rail)" stroke="#eef1f6" strokeWidth={9} />
        </svg>
        <div className="absolute bottom-10 inset-x-10">
          <div className="text-[40px] font-bold leading-tight">{t("brand")}</div>
          <p className="text-ink2 mt-2 max-w-sm">{lang === "fa" ? "همهٔ خط‌ها از یک ایستگاه." : "Every line, one interchange."}</p>
        </div>
      </div>

      <div className="flex flex-col justify-center px-6 py-12">
        <form onSubmit={submit} className="rise w-full max-w-sm mx-auto">
          <h1 className="text-2xl font-bold">{t("signIn")}</h1>
          <p className="text-ink2 text-sm mt-1 mb-8">Dunvex · Pedram</p>
          <label className="block mb-4">
            <Label>{t("username")}</Label>
            <Input value={username} onChange={(e) => setUser(e.target.value)} autoComplete="username" dir="ltr" autoFocus required />
          </label>
          <label className="block mb-6">
            <Label>{t("password")}</Label>
            <Input type="password" value={password} onChange={(e) => setPass(e.target.value)} autoComplete="current-password" dir="ltr" required />
          </label>
          {err && <div className="mb-4 rounded-xl border border-[rgba(255,93,115,.4)] bg-[rgba(255,93,115,.08)] px-3 py-2 text-sm text-[#ffc2cc]">{err}</div>}
          <Button variant="primary" className="w-full" loading={busy} type="submit">
            {t("login")}
          </Button>
          <button type="button" onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="mt-6 text-ink3 hover:text-ink text-[13px] w-full">
            {lang === "fa" ? "English" : "فارسی"}
          </button>
        </form>
      </div>
    </div>
  );
}
