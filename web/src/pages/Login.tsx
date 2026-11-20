import { useState } from "react";
import { ArrowLeft, ArrowRight, KeyRound, Languages, Lock, ShieldCheck, User } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { Button, Input, Label } from "../components/ui";
import { Logo } from "../components/Logo";

export function Login({ onDone }: { onDone: () => void }) {
  const { t, lang, setLang } = useI18n();
  const [username, setUser] = useState("");
  const [password, setPass] = useState("");
  const [code, setCode] = useState("");
  const [need2fa, setNeed2fa] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const Arrow = lang === "fa" ? ArrowLeft : ArrowRight;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    fetchLogin({ username, password, code: need2fa ? code : "" })
      .then(onDone)
      .catch((e: ApiError & { need2fa?: boolean }) => {
        if (e.need2fa) {
          if (need2fa) setErr(t("enterCode"));
          setNeed2fa(true);
          setCode("");
          return;
        }
        setErr(e.status === 401 ? t("wrongLogin") : e.message);
      })
      .finally(() => setBusy(false));
  };

  return (
    <div className="relative min-h-dvh overflow-hidden">
      <div className="aurora" aria-hidden />
      <MetroArt />
      <div className="relative z-[1] min-h-dvh grid lg:grid-cols-[1.15fr_1fr]">
        {/* the logo stage */}
        <div className="flex flex-col items-center justify-center px-8 pt-14 pb-6 lg:py-12">
          <div className="relative">
            <div className="absolute inset-0 -z-10 blur-3xl rounded-full bg-[#2f8cff]/25 scale-75" aria-hidden />
            <Logo part="full" className="w-[230px] sm:w-[300px] lg:w-[420px] drop-shadow-[0_24px_60px_rgba(47,140,255,.28)]" />
          </div>
          <p className="hidden lg:block text-ink2 mt-2 max-w-sm text-center text-[15px]">
            {lang === "fa" ? "همهٔ پروتکل‌ها، یک پنل. ساده، سریع و دقیق." : "Every protocol, one panel. Simple, fast and precise."}
          </p>
        </div>

        {/* the form */}
        <div className="flex flex-col justify-center px-5 pb-12 lg:py-12">
          <form onSubmit={submit} className="rise glass-strong edge w-full max-w-[400px] mx-auto rounded-[26px] p-7 sm:p-8 shadow-[0_40px_100px_-40px_rgba(0,0,0,.9)]">
            <div className="flex items-center gap-3 mb-7">
              <span className="grid place-items-center size-11 rounded-2xl brand-gradient text-white shadow-[0_10px_30px_-10px_rgba(47,140,255,.8)]">
                {need2fa ? <ShieldCheck className="size-5" /> : <Lock className="size-5" />}
              </span>
              <div>
                <h1 className="text-[20px] font-bold leading-tight">{need2fa ? t("twoFactor") : t("signIn")}</h1>
                <p className="text-ink3 text-[12.5px]">{need2fa ? t("enterCode") : "Dunvex · Powered by Pedram"}</p>
              </div>
            </div>

            {!need2fa ? (
              <>
                <label className="block mb-4">
                  <Label>{t("username")}</Label>
                  <div className="relative">
                    <User className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
                    <Input value={username} onChange={(e) => setUser(e.target.value)} autoComplete="username" dir="ltr" autoFocus required className="ps-10" />
                  </div>
                </label>
                <label className="block mb-6">
                  <Label>{t("password")}</Label>
                  <div className="relative">
                    <KeyRound className="size-4 text-ink3 absolute top-3.5 [inset-inline-start:14px] pointer-events-none" />
                    <Input type="password" value={password} onChange={(e) => setPass(e.target.value)} autoComplete="current-password" dir="ltr" required className="ps-10" />
                  </div>
                </label>
              </>
            ) : (
              <label className="block mb-6">
                <Label>{t("code")}</Label>
                <Input value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" dir="ltr" autoFocus required
                  className="mono text-center text-[24px] tracking-[.5em] h-14" placeholder="••••••" />
              </label>
            )}

            {err && <div role="alert" className="mb-4 rounded-xl border border-bad/40 bg-bad/[.08] px-3 py-2.5 text-sm text-[#ffc2cc]">{err}</div>}
            <Button variant="primary" size="lg" className="w-full" loading={busy} type="submit" icon={<Arrow className="size-4" />}>
              {t("login")}
            </Button>
            {need2fa && (
              <button type="button" onClick={() => { setNeed2fa(false); setErr(""); }} className="mt-4 text-ink3 hover:text-ink text-[13px] w-full">{t("back")}</button>
            )}
          </form>
          <button type="button" onClick={() => setLang(lang === "fa" ? "en" : "fa")} className="mt-6 mx-auto inline-flex items-center gap-2 text-ink3 hover:text-ink text-[13px] h-10 px-3 rounded-xl">
            <Languages className="size-4" />
            {lang === "fa" ? "English" : "فارسی"}
          </button>
        </div>
      </div>
    </div>
  );
}

// login needs the extra `need2fa` flag that api() drops
async function fetchLogin(body: { username: string; password: string; code: string }) {
  try {
    return await api("login", { body });
  } catch (e) {
    const err = e as ApiError & { need2fa?: boolean; data?: any };
    if (err.status === 401 && err.data?.need2fa) err.need2fa = true;
    throw err;
  }
}

/** Faint metro lines behind the login: the four protocol families meeting. */
function MetroArt() {
  return (
    <svg viewBox="0 0 1200 800" className="absolute inset-0 w-full h-full opacity-[.16] pointer-events-none" preserveAspectRatio="xMidYMid slice" aria-hidden>
      <path d="M-20 260 H340 L440 360 V820" fill="none" stroke="var(--color-reality)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M1220 180 H760 L660 280 V360" fill="none" stroke="var(--color-cdn)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M-20 560 H300 L400 460" fill="none" stroke="var(--color-udp)" strokeWidth={10} strokeLinejoin="round" />
      <path d="M1220 600 H780 L680 500 V-20" fill="none" stroke="var(--color-classic)" strokeWidth={10} strokeLinejoin="round" />
    </svg>
  );
}
